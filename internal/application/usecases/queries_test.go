package usecases_test

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/claudioed/last-mile-delivery/internal/application/ports"
	"github.com/claudioed/last-mile-delivery/internal/application/usecases"
	"github.com/claudioed/last-mile-delivery/internal/application/views"
	"github.com/claudioed/last-mile-delivery/internal/domain/route"
	"github.com/claudioed/last-mile-delivery/internal/domain/shared"
)

func TestListWavesOrdersByCutoffAndFilters(t *testing.T) {
	e := newEnv(t)
	e.station(t, "node-ds-01")
	e.wave(t, 5, 1) // wave-1
	e.wave(t, 2, 1) // wave-2, earlier cutoff
	noErr(t, e.deps.Stations.Save(context.Background(), views.Station{NodeID: "node-ds-02", NodeType: views.DeliveryStation}))
	_, err := usecases.NewScheduleWave(e.deps).Execute(context.Background(), usecases.ScheduleWaveCommand{
		StationNodeID: "node-ds-02", ServiceDate: "2026-10-24", InductCutoffAt: t0.Add(time.Hour),
		WindowStart: t0.Add(2 * time.Hour), WindowEnd: t0.Add(3 * time.Hour), StopCapacity: 1,
	})
	noErr(t, err)
	q := usecases.NewListWaves(e.deps)
	got, err := q.Execute(context.Background(), "node-ds-01", "2026-10-24")
	noErr(t, err)
	if len(got) != 2 || got[0].WaveID != "wave-2" || got[1].WaveID != "wave-1" {
		t.Fatalf("waves = %+v", got)
	}
	for _, c := range []struct{ station, date string }{{"node-ds-01", "2026-10-25"}, {"node-x", "2026-10-24"}} {
		got, err := q.Execute(context.Background(), c.station, c.date)
		noErr(t, err)
		if got == nil || len(got) != 0 {
			t.Fatalf("%v: want an empty non-nil list, got %#v", c, got)
		}
	}
	for _, c := range []struct{ station, date string }{{"", "2026-10-24"}, {"node-ds-01", ""}, {"node-ds-01", "24/10/2026"}} {
		_, err := q.Execute(context.Background(), c.station, c.date)
		wantIs(t, err, shared.ErrValidation)
	}
}

func TestGetWaveStopsGroupsByStopKey(t *testing.T) {
	e := newEnv(t)
	e.station(t, "node-ds-01")
	e.wave(t, 2, 5)
	e.label(t, "lb-1", "TRK-1", "k-a")
	e.label(t, "lb-3", "TRK-3", "k-b")
	e.label(t, "lb-2", "TRK-2", "k-a")
	e.arrive(t, "ev-1", "TRK-1", "TRK-3", "TRK-2", "TRK-4") // TRK-4 has no label data
	got, err := usecases.NewGetWaveStops(e.deps).Execute(context.Background(), "wave-1")
	noErr(t, err)
	want := []views.WaveStop{
		{StopKey: "k-a", DestinationPostalPrefix: "01310", TrackingNumbers: []string{"TRK-1", "TRK-2"}},
		{StopKey: "k-b", DestinationPostalPrefix: "01310", TrackingNumbers: []string{"TRK-3"}},
		{StopKey: "pkg:TRK-4", TrackingNumbers: []string{"TRK-4"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("stops = %+v, want %+v", got, want)
	}
	_, err = usecases.NewGetWaveStops(e.deps).Execute(context.Background(), "wave-404")
	wantIs(t, err, ports.ErrNotFound)
	e.wave(t, 3, 1)
	empty, err := usecases.NewGetWaveStops(e.deps).Execute(context.Background(), "wave-2")
	noErr(t, err)
	if empty == nil || len(empty) != 0 {
		t.Fatalf("a wave with no package must give an empty non-nil list, got %#v", empty)
	}
}

func TestGetRouteShowsDerivedStopOutcomes(t *testing.T) {
	e := dispatchedRoute(t, false)
	e.start(t)
	_, err := e.attempt("TRK-1", route.OutcomeDelivered, "", t0.Add(8*time.Hour))
	noErr(t, err)
	_, err = e.attempt("TRK-2", route.OutcomeFailed, "absent", t0.Add(8*time.Hour))
	noErr(t, err)
	got, err := usecases.NewGetRoute(e.deps).Execute(context.Background(), "route-1")
	noErr(t, err)
	if got.State != "OUT_FOR_DELIVERY" || got.StopCount != 2 || len(got.Attempts) != 2 || got.WaveID != "wave-1" || got.DSPID != "dsp-1" {
		t.Fatalf("route = %+v", got)
	}
	if got.Stops[0].Outcome != "FAILED" || got.Stops[1].Outcome != "" {
		t.Fatalf("stops = %+v, want stop 1 FAILED (mixed) and stop 2 undecided", got.Stops)
	}
	_, err = usecases.NewGetRoute(e.deps).Execute(context.Background(), "route-404")
	wantIs(t, err, ports.ErrNotFound)
}
