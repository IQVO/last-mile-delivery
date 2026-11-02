package usecases

import (
	"context"
	"sort"
	"time"

	"github.com/claudioed/last-mile-delivery/internal/application/repository"
	"github.com/claudioed/last-mile-delivery/internal/application/views"
	"github.com/claudioed/last-mile-delivery/internal/domain/route"
	"github.com/claudioed/last-mile-delivery/internal/domain/shared"
	"github.com/claudioed/last-mile-delivery/internal/domain/wave"
)

// ListWaves lists the waves of a station on a service date.
type ListWaves struct{ Deps }

// NewListWaves builds the query.
func NewListWaves(d Deps) *ListWaves { return &ListWaves{d} }

// maxCompletePages bounds the loop that Execute and the stop listing run over
// their pages (500 rows each), so a tool answer stays complete up to 10 000 rows.
const maxCompletePages = 20

// completePageSize is the page size of that loop.
const completePageSize = 500

// Page returns at most limit waves after the keyset position (nil is the first
// page), ordered by inductCutoffAt then waveId. A blank station or a service
// date that is not YYYY-MM-DD is shared.ErrValidation. The caller asks for
// limit+1 rows to learn whether another page exists.
func (q *ListWaves) Page(ctx context.Context, stationNodeID, serviceDate string, after *repository.WaveAfter, limit int) ([]views.Wave, error) {
	if !shared.ValidID(stationNodeID) || !shared.ValidDate(serviceDate) {
		return nil, shared.Invalid("stationNodeId and a YYYY-MM-DD serviceDate are required")
	}
	waves, err := q.Waves.List(ctx, repository.WaveFilter{StationNodeID: stationNodeID, ServiceDate: serviceDate, After: after, Limit: limit})
	if err != nil {
		return nil, err
	}
	out := make([]views.Wave, 0, len(waves))
	for _, w := range waves {
		out = append(out, views.WaveFrom(w))
	}
	return out, nil
}

// Execute returns every wave of the station on the date (up to 10 000, the
// bound of the MCP tool and the A2A skill), walking the pages of Page.
func (q *ListWaves) Execute(ctx context.Context, stationNodeID, serviceDate string) ([]views.Wave, error) {
	out := []views.Wave{}
	var after *repository.WaveAfter
	for range maxCompletePages {
		rows, err := q.Page(ctx, stationNodeID, serviceDate, after, completePageSize+1)
		if err != nil {
			return nil, err
		}
		more := len(rows) > completePageSize
		if more {
			rows = rows[:completePageSize]
		}
		out = append(out, rows...)
		if !more {
			break
		}
		last := rows[len(rows)-1]
		after = &repository.WaveAfter{InductCutoffAt: last.InductCutoffAt, WaveID: last.WaveID}
	}
	return out, nil
}

// GetWaveStops lists the stops of a wave.
type GetWaveStops struct{ Deps }

// NewGetWaveStops builds the query.
func NewGetWaveStops(d Deps) *GetWaveStops { return &GetWaveStops{d} }

// StopAfter is the keyset position of a page of stops: the first induction of
// the last stop returned and its stopKey.
type StopAfter struct {
	FirstInductedAt time.Time
	StopKey         string
}

// Page groups the packages inducted in the wave by stopKey and returns at most
// limit stops after the position (nil is the first page), ordered by the first
// induction of each stop then stopKey (G11), with the postal prefix when the
// label data has it. The caller asks for limit+1 stops to learn whether another
// page exists. An unknown wave is ports.ErrNotFound.
func (q *GetWaveStops) Page(ctx context.Context, id wave.ID, after *StopAfter, limit int) ([]views.WaveStop, error) {
	w, err := q.Waves.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	stops := groupStops(w.Inductions())
	out := []views.WaveStop{}
	for _, st := range stops {
		if len(out) == limit {
			break
		}
		if after != nil && !stopAfter(st, *after) {
			continue
		}
		for _, tn := range st.TrackingNumbers {
			prefix, err := q.postalPrefix(ctx, tn)
			if err != nil {
				return nil, err
			}
			if prefix != "" {
				st.DestinationPostalPrefix = prefix
				break
			}
		}
		out = append(out, st)
	}
	return out, nil
}

// Execute returns every stop of the wave (up to 10 000), walking the pages of
// Page; it is what the MCP tool get_wave_stops returns.
func (q *GetWaveStops) Execute(ctx context.Context, id wave.ID) ([]views.WaveStop, error) {
	out := []views.WaveStop{}
	var after *StopAfter
	for range maxCompletePages {
		rows, err := q.Page(ctx, id, after, completePageSize+1)
		if err != nil {
			return nil, err
		}
		more := len(rows) > completePageSize
		if more {
			rows = rows[:completePageSize]
		}
		out = append(out, rows...)
		if !more {
			break
		}
		last := rows[len(rows)-1]
		after = &StopAfter{FirstInductedAt: last.FirstInductedAt, StopKey: last.StopKey}
	}
	return out, nil
}

// groupStops groups inductions by stopKey ordered by (first induction, stopKey).
func groupStops(inductions []wave.Induction) []views.WaveStop {
	index := map[string]int{}
	out := []views.WaveStop{}
	for _, in := range inductions {
		i, ok := index[in.StopKey]
		if !ok {
			i = len(out)
			index[in.StopKey] = i
			out = append(out, views.WaveStop{StopKey: in.StopKey, TrackingNumbers: []string{}, FirstInductedAt: in.At})
		}
		out[i].TrackingNumbers = append(out[i].TrackingNumbers, in.TrackingNumber)
		if in.At.Before(out[i].FirstInductedAt) {
			out[i].FirstInductedAt = in.At
		}
	}
	sort.SliceStable(out, func(a, b int) bool {
		if !out[a].FirstInductedAt.Equal(out[b].FirstInductedAt) {
			return out[a].FirstInductedAt.Before(out[b].FirstInductedAt)
		}
		return out[a].StopKey < out[b].StopKey
	})
	return out
}

func stopAfter(st views.WaveStop, a StopAfter) bool {
	return st.FirstInductedAt.After(a.FirstInductedAt) || (st.FirstInductedAt.Equal(a.FirstInductedAt) && st.StopKey > a.StopKey)
}

func (q *GetWaveStops) postalPrefix(ctx context.Context, trackingNumber string) (string, error) {
	ps, err := q.PackageStops.Get(ctx, trackingNumber)
	ok, err := found(err)
	if err != nil || !ok {
		return "", err
	}
	return ps.DestinationPostalPrefix, nil
}

// GetRoute reads one route.
type GetRoute struct{ Deps }

// NewGetRoute builds the query.
func NewGetRoute(d Deps) *GetRoute { return &GetRoute{d} }

// Execute returns the route with its stops and attempts, or ports.ErrNotFound.
func (q *GetRoute) Execute(ctx context.Context, id route.ID) (views.Route, error) {
	r, err := q.Routes.Get(ctx, id)
	if err != nil {
		return views.Route{}, err
	}
	return views.RouteFrom(r), nil
}
