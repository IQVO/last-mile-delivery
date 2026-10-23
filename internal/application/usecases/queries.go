package usecases

import (
	"context"

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

// Execute returns the waves ordered by inductCutoffAt then waveId. A blank
// station or a service date that is not YYYY-MM-DD is shared.ErrValidation.
func (q *ListWaves) Execute(ctx context.Context, stationNodeID, serviceDate string) ([]views.Wave, error) {
	if !shared.ValidID(stationNodeID) || !shared.ValidDate(serviceDate) {
		return nil, shared.Invalid("stationNodeId and a YYYY-MM-DD serviceDate are required")
	}
	waves, err := q.Waves.List(ctx, repository.WaveFilter{StationNodeID: stationNodeID, ServiceDate: serviceDate})
	if err != nil {
		return nil, err
	}
	out := make([]views.Wave, 0, len(waves))
	for _, w := range waves {
		out = append(out, views.WaveFrom(w))
	}
	return out, nil
}

// GetWaveStops lists the stops of a wave.
type GetWaveStops struct{ Deps }

// NewGetWaveStops builds the query.
func NewGetWaveStops(d Deps) *GetWaveStops { return &GetWaveStops{d} }

// Execute groups the packages inducted in the wave by stopKey, in the order of
// each stop's first induction, with the postal prefix when the label data has it
// (G11). An unknown wave is ports.ErrNotFound.
func (q *GetWaveStops) Execute(ctx context.Context, id wave.ID) ([]views.WaveStop, error) {
	w, err := q.Waves.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	out := []views.WaveStop{}
	index := map[string]int{}
	for _, in := range w.Inductions() {
		i, ok := index[in.StopKey]
		if !ok {
			i = len(out)
			index[in.StopKey] = i
			out = append(out, views.WaveStop{StopKey: in.StopKey, TrackingNumbers: []string{}})
		}
		out[i].TrackingNumbers = append(out[i].TrackingNumbers, in.TrackingNumber)
		if out[i].DestinationPostalPrefix == "" {
			prefix, err := q.postalPrefix(ctx, in.TrackingNumber)
			if err != nil {
				return nil, err
			}
			out[i].DestinationPostalPrefix = prefix
		}
	}
	return out, nil
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
