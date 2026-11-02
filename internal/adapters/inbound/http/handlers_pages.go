package http

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/claudioed/last-mile-delivery/internal/adapters/inbound/http/page"
	"github.com/claudioed/last-mile-delivery/internal/application/repository"
	"github.com/claudioed/last-mile-delivery/internal/application/usecases"
	"github.com/claudioed/last-mile-delivery/internal/application/views"
	"github.com/claudioed/last-mile-delivery/internal/domain/wave"
)

// pageParams reads limit and cursor of a collection GET and rejects a query
// parameter the collection does not declare (REST guideline 4.2). It writes the
// 400 problem and reports false when the request is refused.
func pageParams(w http.ResponseWriter, r *http.Request, filters ...string) (int, *page.Position, bool) {
	if name := page.Unknown(r, filters...); name != "" {
		writeProblem(w, r, unknownQueryParameter, "the query parameter "+name+" is not declared by this collection")
		return 0, nil, false
	}
	limit, err := page.Limit(r)
	if err != nil {
		writeProblem(w, r, invalidLimit, err.Error())
		return 0, nil, false
	}
	after, err := page.After(r)
	if err != nil {
		writeProblem(w, r, invalidCursor, err.Error())
		return 0, nil, false
	}
	return limit, after, true
}

func (s *Server) handleListWaves(w http.ResponseWriter, r *http.Request) {
	limit, pos, ok := pageParams(w, r, "stationNodeId", "serviceDate")
	if !ok {
		return
	}
	station, date := r.URL.Query().Get("stationNodeId"), r.URL.Query().Get("serviceDate")
	if station == "" {
		writeError(w, r, validationError("stationNodeId is required"))
		return
	}
	if _, err := time.Parse(time.DateOnly, date); err != nil {
		writeError(w, r, validationError("serviceDate is required and must be a date (YYYY-MM-DD)"))
		return
	}
	var after *repository.WaveAfter
	if pos != nil {
		at, err := time.Parse(time.RFC3339Nano, pos.Sort)
		if err != nil {
			writeProblem(w, r, invalidCursor, page.ErrInvalidCursor.Error())
			return
		}
		after = &repository.WaveAfter{InductCutoffAt: at, WaveID: pos.ID}
	}
	rows, err := s.ListWaves.Page(r.Context(), station, date, after, page.FetchSize(limit))
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, mapPage(page.Of(rows, limit, func(v views.Wave) page.Position {
		return page.Position{Sort: v.InductCutoffAt.UTC().Format(time.RFC3339Nano), ID: v.WaveID}
	}), toWaveBody))
}

func (s *Server) handleGetWaveStops(w http.ResponseWriter, r *http.Request) {
	limit, pos, ok := pageParams(w, r)
	if !ok {
		return
	}
	var after *usecases.StopAfter
	if pos != nil {
		at, err := time.Parse(time.RFC3339Nano, pos.Sort)
		if err != nil {
			writeProblem(w, r, invalidCursor, page.ErrInvalidCursor.Error())
			return
		}
		after = &usecases.StopAfter{FirstInductedAt: at, StopKey: pos.ID}
	}
	rows, err := s.GetWaveStops.Page(r.Context(), wave.ID(chi.URLParam(r, "waveId")), after, page.FetchSize(limit))
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, mapPage(page.Of(rows, limit, func(v views.WaveStop) page.Position {
		return page.Position{Sort: v.FirstInductedAt.UTC().Format(time.RFC3339Nano), ID: v.StopKey}
	}), toWaveStopBody))
}

// mapPage converts the items of a page to their wire bodies, keeping the cursor.
func mapPage[T, U any](p page.Page[T], f func(T) U) page.Page[U] {
	items := make([]U, 0, len(p.Items))
	for _, it := range p.Items {
		items = append(items, f(it))
	}
	return page.Page[U]{Items: items, NextCursor: p.NextCursor}
}

func toWaveStopBody(st views.WaveStop) waveStopBody {
	return waveStopBody{StopKey: st.StopKey, DestinationPostalPrefix: st.DestinationPostalPrefix, TrackingNumbers: nonNil(st.TrackingNumbers)}
}
