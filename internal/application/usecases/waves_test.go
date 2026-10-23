package usecases_test

import (
	"context"
	"testing"
	"time"

	"github.com/claudioed/last-mile-delivery/internal/application/ports"
	"github.com/claudioed/last-mile-delivery/internal/application/usecases"
	"github.com/claudioed/last-mile-delivery/internal/application/views"
	"github.com/claudioed/last-mile-delivery/internal/domain/history"
	"github.com/claudioed/last-mile-delivery/internal/domain/shared"
	"github.com/claudioed/last-mile-delivery/internal/domain/wave"
)

func TestScheduleWave(t *testing.T) {
	e := newEnv(t)
	e.station(t, "node-ds-01")
	v := e.wave(t, 2, 3)
	if v.WaveID != "wave-1" || v.State != "OPEN" || v.StopCapacity != 3 || v.AssignedStops != 0 || v.RemainingStops != 3 ||
		v.StationNodeID != "node-ds-01" || v.ServiceDate != "2026-10-24" || !v.InductCutoffAt.Equal(t0.Add(2*time.Hour)) {
		t.Fatalf("view = %+v", v)
	}
	e.wantEvents(t, "DeliveryWaveScheduled:wave-1")
}

func TestScheduleWaveRefusals(t *testing.T) {
	e := newEnv(t)
	cmd := usecases.ScheduleWaveCommand{
		StationNodeID: "node-ds-01", ServiceDate: "2026-10-24", InductCutoffAt: t0.Add(time.Hour),
		WindowStart: t0.Add(2 * time.Hour), WindowEnd: t0.Add(3 * time.Hour), StopCapacity: 1,
	}
	uc := usecases.NewScheduleWave(e.deps)
	e.wantUnchanged(t, func() {
		_, err := uc.Execute(context.Background(), cmd)
		wantIs(t, err, usecases.ErrUnknownStation)
	})
	noErr(t, e.deps.Stations.Save(context.Background(), views.Station{NodeID: "node-fc-01", NodeType: "FULFILLMENT_CENTER"}))
	e.wantUnchanged(t, func() {
		cmd.StationNodeID = "node-fc-01"
		_, err := uc.Execute(context.Background(), cmd)
		wantIs(t, err, usecases.ErrUnknownStation)
	})
	e.station(t, "node-ds-01")
	e.wantUnchanged(t, func() {
		cmd.StationNodeID = "node-ds-01"
		cmd.InductCutoffAt = t0.Add(5 * time.Hour)
		_, err := uc.Execute(context.Background(), cmd)
		wantIs(t, err, wave.ErrInvalidWaveTimes)
	})
}

func TestScheduleWaveInductsWaitingPackages(t *testing.T) {
	e := newEnv(t)
	e.station(t, "node-ds-01")
	e.arrive(t, "ev-1", "TRK-1", "TRK-2")
	e.wantEvents(t, "PackageInductionDeferred:TRK-1", "PackageInductionDeferred:TRK-2")
	v := e.wave(t, 2, 1)
	if v.AssignedStops != 1 || v.RemainingStops != 0 {
		t.Fatalf("the sweep must fill the new wave: %+v", v)
	}
	e.wantEvents(t, "PackageInductionDeferred:TRK-1", "PackageInductionDeferred:TRK-2",
		"DeliveryWaveScheduled:wave-1", "DeliveryWaveCapacityChanged:wave-1")
	assertStatus(t, e, "TRK-1", history.StatusInducted)
	assertStatus(t, e, "TRK-2", history.StatusAtStation)
	// A later wave takes the package that still waits, with no second deferral.
	e.wave(t, 3, 1)
	assertStatus(t, e, "TRK-2", history.StatusInducted)
}

func assertStatus(t *testing.T, e *env, tn string, want history.Status) {
	t.Helper()
	h, err := e.deps.Histories.Get(context.Background(), tn)
	noErr(t, err)
	if h.Status() != want {
		t.Fatalf("%s status = %s, want %s", tn, h.Status(), want)
	}
}

func TestInductOnArrivalPicksTheEarliestEligibleWave(t *testing.T) {
	e := newEnv(t)
	e.station(t, "node-ds-01")
	e.wave(t, 3, 5) // wave-1, later cutoff
	e.wave(t, 2, 1) // wave-2, earlier cutoff, one stop
	e.arrive(t, "ev-1", "TRK-1", "TRK-2")
	// TRK-1 takes the only stop of the earlier wave-2, TRK-2 falls to wave-1.
	if _, ok := e.loadWave(t, "wave-2").Induction("TRK-1"); !ok {
		t.Fatal("TRK-1 must join the earliest wave")
	}
	if _, ok := e.loadWave(t, "wave-1").Induction("TRK-2"); !ok {
		t.Fatal("TRK-2 must fall through the full wave to the next")
	}
	e.wantEvents(t, "DeliveryWaveScheduled:wave-1", "DeliveryWaveScheduled:wave-2",
		"DeliveryWaveCapacityChanged:wave-2", "DeliveryWaveCapacityChanged:wave-1")
	if n := e.store.Counts().InboundPackages; n != 2 {
		t.Fatalf("inbound packages = %d, want 2", n)
	}
}

func TestInductOnArrivalGroupsPackagesByStopKey(t *testing.T) {
	e := newEnv(t)
	e.station(t, "node-ds-01")
	e.wave(t, 2, 1)
	e.label(t, "lb-1", "TRK-1", "k-a")
	e.label(t, "lb-2", "TRK-2", "k-a")
	e.arrive(t, "ev-1", "TRK-1", "TRK-2")
	w := e.loadWave(t, "wave-1")
	if w.AssignedStops() != 1 || len(w.Inductions()) != 2 {
		t.Fatalf("one stop of two packages expected: assigned=%d inductions=%d", w.AssignedStops(), len(w.Inductions()))
	}
	e.wantEvents(t, "DeliveryWaveScheduled:wave-1", "DeliveryWaveCapacityChanged:wave-1")
}

func TestInductOnArrivalDefersWhenNoWaveTakesThePackage(t *testing.T) {
	cases := []struct {
		name  string
		setup func(*testing.T, *env)
		at    time.Time
	}{
		{"no wave at all", func(*testing.T, *env) {}, t0},
		{"cutoff already passed", func(t *testing.T, e *env) { e.wave(t, 2, 5) }, t0.Add(3 * time.Hour)},
		{"wave full", func(t *testing.T, e *env) { e.wave(t, 2, 0) }, t0},
		{"wave closed", func(t *testing.T, e *env) {
			e.wave(t, 2, 5)
			_, err := usecases.NewCloseInduction(e.deps).Execute(context.Background(), "wave-1")
			noErr(t, err)
		}, t0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := newEnv(t)
			e.station(t, "node-ds-01")
			e.label(t, "lb-1", "TRK-1", "k-a")
			c.setup(t, e)
			cmd := e.arrival("ev-1", "TRK-1")
			cmd.ArrivedAt = c.at
			noErr(t, usecases.NewInductOnArrival(e.deps).Execute(context.Background(), cmd))
			assertStatus(t, e, "TRK-1", history.StatusAtStation)
			names := e.eventNames()
			if names[len(names)-1] != "PackageInductionDeferred:TRK-1" {
				t.Fatalf("events = %v, want a final PackageInductionDeferred", names)
			}
		})
	}
}

func TestInductOnArrivalIgnores(t *testing.T) {
	e := newEnv(t)
	e.station(t, "node-ds-01")
	e.wave(t, 2, 5)
	uc := usecases.NewInductOnArrival(e.deps)
	run := func(mutate func(*usecases.LoadArrivedCommand)) {
		cmd := e.arrival("ev-x", "TRK-9")
		mutate(&cmd)
		noErr(t, uc.Execute(context.Background(), cmd))
	}
	before := len(e.eventNames())
	run(func(c *usecases.LoadArrivedCommand) { c.EventID, c.NodeType = "ev-a", "SORTATION_CENTER" })
	run(func(c *usecases.LoadArrivedCommand) {
		c.EventID, c.NodeType, c.DestinationNodeID = "ev-b", "", "node-unknown"
	})
	run(func(c *usecases.LoadArrivedCommand) { c.EventID, c.DestinationNodeID = "ev-c", "" })
	run(func(c *usecases.LoadArrivedCommand) { c.EventID, c.TrackingNumbers = "ev-d", []string{"", "a b"} })
	if len(e.eventNames()) != before || e.store.Counts().Histories != 0 || e.store.Counts().InboundPackages != 0 {
		t.Fatalf("ignored arrivals must change nothing but the inbox: %+v", e.store.Counts())
	}
	if e.store.Counts().ProcessedEvents != 4 {
		t.Fatalf("processed events = %d, want 4", e.store.Counts().ProcessedEvents)
	}
}

func TestInductOnArrivalUsesTheStationViewWhenNodeTypeIsAbsent(t *testing.T) {
	e := newEnv(t)
	e.station(t, "node-ds-01")
	e.wave(t, 2, 5)
	cmd := e.arrival("ev-1", "TRK-1")
	cmd.NodeType = ""
	noErr(t, usecases.NewInductOnArrival(e.deps).Execute(context.Background(), cmd))
	assertStatus(t, e, "TRK-1", history.StatusInducted)
}

func TestInductOnArrivalIsIdempotent(t *testing.T) {
	e := newEnv(t)
	e.station(t, "node-ds-01")
	e.wave(t, 2, 5)
	e.arrive(t, "ev-1", "TRK-1")
	counts := e.store.Counts()
	events := len(e.eventNames())
	e.arrive(t, "ev-1", "TRK-1") // the same CloudEvents id
	if e.store.Counts() != counts || len(e.eventNames()) != events {
		t.Fatal("a redelivered event must change nothing")
	}
	arrival := e.arrival("ev-2", "TRK-1") // another event for an already inducted package
	noErr(t, usecases.NewInductOnArrival(e.deps).Execute(context.Background(), arrival))
	if len(e.eventNames()) != events || len(e.loadWave(t, "wave-1").Inductions()) != 1 {
		t.Fatal("an inducted package must not be inducted twice")
	}
}

func TestInductOnArrivalZeroInstantUsesTheClock(t *testing.T) {
	e := newEnv(t)
	e.station(t, "node-ds-01")
	e.wave(t, 2, 5)
	cmd := e.arrival("ev-1", "TRK-1")
	cmd.ArrivedAt = time.Time{}
	noErr(t, usecases.NewInductOnArrival(e.deps).Execute(context.Background(), cmd))
	got, err := e.deps.Inbound.Get(context.Background(), "load-ev-1", "TRK-1")
	noErr(t, err)
	if !got.ArrivedAt.Equal(t0) || got.StationNodeID != "node-ds-01" || got.DepartureID != "dep-1" {
		t.Fatalf("inbound = %+v", got)
	}
}

func TestCloseInductionAndDispatch(t *testing.T) {
	e := newEnv(t)
	e.station(t, "node-ds-01")
	e.wave(t, 2, 5)
	ctx := context.Background()
	v, err := usecases.NewCloseInduction(e.deps).Execute(ctx, "wave-1")
	noErr(t, err)
	if v.State != "INDUCTION_CLOSED" {
		t.Fatalf("state = %s", v.State)
	}
	e.wantUnchanged(t, func() {
		_, err := usecases.NewCloseInduction(e.deps).Execute(ctx, "wave-1")
		wantIs(t, err, shared.ErrIllegalTransition)
		_, err = usecases.NewCloseInduction(e.deps).Execute(ctx, "wave-404")
		wantIs(t, err, ports.ErrNotFound)
		_, err = usecases.NewDispatchWave(e.deps).Execute(ctx, "wave-404")
		wantIs(t, err, ports.ErrNotFound)
	})
	// No route to run: dispatch completes the wave at once (A9).
	v, err = usecases.NewDispatchWave(e.deps).Execute(ctx, "wave-1")
	noErr(t, err)
	if v.State != "COMPLETED" {
		t.Fatalf("state = %s, want COMPLETED", v.State)
	}
	e.wantUnchanged(t, func() {
		_, err := usecases.NewDispatchWave(e.deps).Execute(ctx, "wave-1")
		wantIs(t, err, shared.ErrIllegalTransition)
	})
	e.wave(t, 3, 1)
	e.wantUnchanged(t, func() {
		_, err := usecases.NewDispatchWave(e.deps).Execute(ctx, "wave-2")
		wantIs(t, err, shared.ErrIllegalTransition)
	})
}

func TestDispatchReinductsPackagesNoRouteHolds(t *testing.T) {
	e := newEnv(t)
	e.station(t, "node-ds-01")
	e.wave(t, 2, 5)
	e.partner(t, 5)
	e.arrive(t, "ev-1", "TRK-1", "TRK-2")
	e.closeAndPlan(t, "wave-1", "dsp-1", stop(1, "TRK-1"))
	// No other wave: the leftover is deferred and the wave keeps its route.
	v, err := usecases.NewDispatchWave(e.deps).Execute(context.Background(), "wave-1")
	noErr(t, err)
	if v.State != "DISPATCHED" {
		t.Fatalf("state = %s, want DISPATCHED while a route is open", v.State)
	}
	assertStatus(t, e, "TRK-1", history.StatusInducted)
	assertStatus(t, e, "TRK-2", history.StatusAtStation)
	names := e.eventNames()
	if names[len(names)-1] != "PackageInductionDeferred:TRK-2" {
		t.Fatalf("events = %v", names)
	}
}

func TestDispatchLeftoverMovesToTheNextWave(t *testing.T) {
	e := newEnv(t)
	e.station(t, "node-ds-01")
	e.wave(t, 2, 5)
	e.wave(t, 30, 5)
	e.partner(t, 5)
	e.arrive(t, "ev-1", "TRK-1", "TRK-2")
	e.closeAndPlan(t, "wave-1", "dsp-1", stop(1, "TRK-1"))
	_, err := usecases.NewDispatchWave(e.deps).Execute(context.Background(), "wave-1")
	noErr(t, err)
	if _, ok := e.loadWave(t, "wave-2").Induction("TRK-2"); !ok {
		t.Fatal("the leftover must be re-inducted onto the next wave")
	}
	assertStatus(t, e, "TRK-2", history.StatusInducted)
}

func TestCancelWaveReinductsItsPackages(t *testing.T) {
	e := newEnv(t)
	e.station(t, "node-ds-01")
	e.wave(t, 2, 5)
	e.wave(t, 30, 5)
	e.arrive(t, "ev-1", "TRK-1")
	if _, ok := e.loadWave(t, "wave-1").Induction("TRK-1"); !ok {
		t.Fatal("setup: TRK-1 must be in wave-1")
	}
	v, err := usecases.NewCancelWave(e.deps).Execute(context.Background(), usecases.CancelWaveCommand{WaveID: "wave-1", Reason: "station closed"})
	noErr(t, err)
	if v.State != "CANCELLED" || v.CancelReason != "station closed" {
		t.Fatalf("view = %+v", v)
	}
	if _, ok := e.loadWave(t, "wave-2").Induction("TRK-1"); !ok {
		t.Fatal("the package must move onto the next wave")
	}
	names := e.eventNames()
	if got := names[len(names)-2:]; got[0] != "DeliveryWaveCancelled:wave-1" || got[1] != "DeliveryWaveCapacityChanged:wave-2" {
		t.Fatalf("events = %v", names)
	}
	assertStatus(t, e, "TRK-1", history.StatusInducted)
	// The last open wave: its package is deferred.
	_, err = usecases.NewCancelWave(e.deps).Execute(context.Background(), usecases.CancelWaveCommand{WaveID: "wave-2", Reason: "again"})
	noErr(t, err)
	assertStatus(t, e, "TRK-1", history.StatusAtStation)
	names = e.eventNames()
	if got := names[len(names)-2:]; got[0] != "DeliveryWaveCancelled:wave-2" || got[1] != "PackageInductionDeferred:TRK-1" {
		t.Fatalf("events = %v", names)
	}
}

func TestCancelWaveRefusals(t *testing.T) {
	e := newEnv(t)
	e.station(t, "node-ds-01")
	e.wave(t, 2, 5)
	uc := usecases.NewCancelWave(e.deps)
	e.wantUnchanged(t, func() {
		_, err := uc.Execute(context.Background(), usecases.CancelWaveCommand{WaveID: "wave-1", Reason: ""})
		wantIs(t, err, shared.ErrValidation)
		_, err = uc.Execute(context.Background(), usecases.CancelWaveCommand{WaveID: "wave-404", Reason: "x"})
		wantIs(t, err, ports.ErrNotFound)
	})
	_, err := usecases.NewCloseInduction(e.deps).Execute(context.Background(), "wave-1")
	noErr(t, err)
	e.wantUnchanged(t, func() {
		_, err := uc.Execute(context.Background(), usecases.CancelWaveCommand{WaveID: "wave-1", Reason: "late"})
		wantIs(t, err, shared.ErrIllegalTransition)
	})
}
