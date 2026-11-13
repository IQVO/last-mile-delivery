package usecases_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/claudioed/last-mile-delivery/internal/application/ports"
	"github.com/claudioed/last-mile-delivery/internal/application/usecases"
	"github.com/claudioed/last-mile-delivery/internal/application/views"
)

// countingAreaMetrics counts the CarrierAreaSkipped calls.
type countingAreaMetrics struct {
	mu    sync.Mutex
	count int
}

var _ ports.AreaMetrics = (*countingAreaMetrics)(nil)

func (m *countingAreaMetrics) CarrierAreaSkipped(context.Context) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.count++
}

func (m *countingAreaMetrics) skipped() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.count
}

// carrierAreaEnv is one in-memory service with a station, an OPEN wave and a
// labelled package bound for prefix "01310", the prefix the tests assign.
func carrierAreaEnv(t *testing.T) *env {
	t.Helper()
	e := newEnv(t)
	noErr(t, e.deps.Stations.Save(context.Background(), views.Station{NodeID: "node-ds-01", NodeType: views.DeliveryStation, RegionID: "region-se"}))
	cutoff := t0.Add(2 * time.Hour)
	_, err := usecases.NewScheduleWave(e.deps).Execute(context.Background(), usecases.ScheduleWaveCommand{
		StationNodeID: "node-ds-01", ServiceDate: "2026-10-24", InductCutoffAt: cutoff, WindowStart: cutoff.Add(time.Hour), WindowEnd: cutoff.Add(5 * time.Hour), StopCapacity: 5,
	})
	noErr(t, err)
	noErr(t, e.deps.PackageStops.Save(context.Background(), views.PackageStop{TrackingNumber: "TRK-1", StopKey: "k1:abc", DestinationPostalPrefix: "01310"}))
	return e
}

// assignArea stores the assignment the tests run under; version rises so the
// save always applies.
func (e *env) assignArea(t *testing.T, version int64, mode string) {
	t.Helper()
	_, err := e.deps.ServiceAreas.Save(context.Background(), views.ServiceArea{
		PostalPrefix: "01310", StationNodeID: "node-ds-01", RegionID: "region-se", AssignmentVersion: version, DeliveryMode: mode,
	})
	noErr(t, err)
}

func (e *env) loadArrives(t *testing.T, trackingNumber string) {
	t.Helper()
	err := usecases.NewInductOnArrival(e.deps).Execute(context.Background(), usecases.LoadArrivedCommand{
		EventID: "evt-" + trackingNumber, EventType: "LoadArrived", LoadID: "load-1", DepartureID: "dep-1",
		DestinationNodeID: "node-ds-01", NodeType: views.DeliveryStation, TrackingNumbers: []string{trackingNumber}, ArrivedAt: t0,
	})
	noErr(t, err)
}

// TestArriveNewRefusesCarrierAreas proves the exclusion of ADR 0015: a package
// whose prefix is assigned CARRIER_HANDOFF raises nothing, keeps no history
// row, takes no wave stop, and is counted once per arrival.
func TestArriveNewRefusesCarrierAreas(t *testing.T) {
	e := carrierAreaEnv(t)
	m := &countingAreaMetrics{}
	e.deps.AreaMetrics = m
	e.assignArea(t, 1, views.CarrierHandoff)
	outboxBefore := len(e.store.Outbox().Events())

	e.loadArrives(t, "TRK-1")
	if got := len(e.store.Outbox().Events()); got != outboxBefore {
		t.Fatalf("outbox events = %d, want the %d of the setup: a refused package raises nothing", got, outboxBefore)
	}
	if n := e.store.Counts().Histories; n != 0 {
		t.Fatalf("history rows = %d, want none: the package never enters the last mile", n)
	}
	if n := e.store.Counts().Waves; n != 1 {
		t.Fatalf("stored aggregates = %d, want only the scheduled wave", n)
	}
	if got := m.skipped(); got != 1 {
		t.Fatalf("carrier_area.skipped = %d, want 1", got)
	}
	if _, err := e.deps.Histories.Get(context.Background(), "TRK-1"); err == nil {
		t.Fatal("a refused package must not be in the history read model")
	}

	// A second load for the same package (a new event) re-counts: the
	// arrival is the signal, and the absent history row keeps it from
	// ever inducting.
	noErr(t, usecases.NewInductOnArrival(e.deps).Execute(context.Background(), usecases.LoadArrivedCommand{
		EventID: "evt-TRK-1-again", EventType: "LoadArrived", LoadID: "load-2", DepartureID: "dep-2",
		DestinationNodeID: "node-ds-01", NodeType: views.DeliveryStation, TrackingNumbers: []string{"TRK-1"}, ArrivedAt: t0,
	}))
	if got := m.skipped(); got != 2 {
		t.Fatalf("carrier_area.skipped after a second load = %d, want 2", got)
	}
	if n := e.store.Counts().Histories; n != 0 {
		t.Fatalf("history rows = %d, want none even after two loads", n)
	}
}

// TestArriveNewInductsNetworkRouteAreas proves the waves of a NETWORK_ROUTE
// prefix (an absent mode, or an explicit one) are untouched by the exclusion.
func TestArriveNewInductsNetworkRouteAreas(t *testing.T) {
	for _, tc := range []struct {
		name string
		mode string
	}{
		{"an area without a mode", ""},
		{"an explicit NETWORK_ROUTE area", views.NetworkRoute},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := carrierAreaEnv(t)
			m := &countingAreaMetrics{}
			e.deps.AreaMetrics = m
			e.assignArea(t, 1, tc.mode)

			e.loadArrives(t, "TRK-1")
			if got := m.skipped(); got != 0 {
				t.Fatalf("carrier_area.skipped = %d, want 0", got)
			}
			h, err := e.deps.Histories.Get(context.Background(), "TRK-1")
			noErr(t, err)
			if h.Status() != "INDUCTED" {
				t.Fatalf("status = %q, want INDUCTED", h.Status())
			}
		})
	}
}

// TestArriveNewInductsWithoutAnAssignment proves refusal needs a known
// CARRIER_HANDOFF declaration: a prefix with no area row inducts as today.
func TestArriveNewInductsWithoutAnAssignment(t *testing.T) {
	e := carrierAreaEnv(t)
	m := &countingAreaMetrics{}
	e.deps.AreaMetrics = m

	e.loadArrives(t, "TRK-1")
	if got := m.skipped(); got != 0 {
		t.Fatalf("carrier_area.skipped = %d, want 0", got)
	}
	if _, err := e.deps.Histories.Get(context.Background(), "TRK-1"); err != nil {
		t.Fatalf("the package must induct as today: %v", err)
	}
}

// TestRetryWaitingRefusesCarrierAreas proves the flip case of ADR 0015: a
// package already waiting at the station when its area flips to
// CARRIER_HANDOFF stops being retried, raises nothing and is counted.
func TestRetryWaitingRefusesCarrierAreas(t *testing.T) {
	e := carrierAreaEnv(t)
	// A first arrival with no OPEN wave (the cutoff already passed) defers
	// the package: it waits AT_STATION.
	err := usecases.NewInductOnArrival(e.deps).Execute(context.Background(), usecases.LoadArrivedCommand{
		EventID: "evt-1", EventType: "LoadArrived", LoadID: "load-1", DepartureID: "dep-1",
		DestinationNodeID: "node-ds-01", NodeType: views.DeliveryStation, TrackingNumbers: []string{"TRK-1"},
		ArrivedAt: t0.Add(3 * time.Hour),
	})
	noErr(t, err)
	h, err := e.deps.Histories.Get(context.Background(), "TRK-1")
	noErr(t, err)
	if h.Status() != "AT_STATION" {
		t.Fatalf("setup: status = %q, want AT_STATION (waiting)", h.Status())
	}

	m := &countingAreaMetrics{}
	e.deps.AreaMetrics = m
	e.assignArea(t, 1, views.CarrierHandoff)

	// Scheduling the next day's wave sweeps the waiting packages
	// (InductDeferredOnSchedule); the exclusion refuses the flipped prefix.
	_, err = usecases.NewScheduleWave(e.deps).Execute(context.Background(), usecases.ScheduleWaveCommand{
		StationNodeID: "node-ds-01", ServiceDate: "2026-10-25", InductCutoffAt: t0.Add(26 * time.Hour),
		WindowStart: t0.Add(27 * time.Hour), WindowEnd: t0.Add(31 * time.Hour), StopCapacity: 5,
	})
	noErr(t, err)
	h, err = e.deps.Histories.Get(context.Background(), "TRK-1")
	noErr(t, err)
	if h.Status() != "AT_STATION" {
		t.Fatalf("status = %q, want the waiting package refused (AT_STATION)", h.Status())
	}
	if got := m.skipped(); got != 1 {
		t.Fatalf("carrier_area.skipped = %d, want 1", got)
	}
}
