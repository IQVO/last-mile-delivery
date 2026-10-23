package usecases_test

import (
	"context"
	"testing"
	"time"

	"github.com/claudioed/last-mile-delivery/internal/application/ports"
	"github.com/claudioed/last-mile-delivery/internal/application/usecases"
	"github.com/claudioed/last-mile-delivery/internal/domain/history"
	"github.com/claudioed/last-mile-delivery/internal/domain/partner"
	"github.com/claudioed/last-mile-delivery/internal/domain/route"
	"github.com/claudioed/last-mile-delivery/internal/domain/shared"
	"github.com/claudioed/last-mile-delivery/internal/domain/wave"
)

// dispatchedRoute builds: wave-1 (and wave-2 when later is true) at node-ds-01,
// partner dsp-1 (van of 2), TRK-1 and TRK-2 sharing stop k-a, TRK-3 alone, and
// route-1 [stop 1: TRK-1 TRK-2, stop 2: TRK-3] planned in wave-1, dispatched.
func dispatchedRoute(t *testing.T, later bool) *env {
	t.Helper()
	e := newEnv(t)
	e.station(t, "node-ds-01")
	e.wave(t, 2, 5)
	if later {
		e.wave(t, 48, 5)
	}
	e.partner(t, 2)
	e.label(t, "lb-1", "TRK-1", "k-a")
	e.label(t, "lb-2", "TRK-2", "k-a")
	e.arrive(t, "ev-1", "TRK-1", "TRK-2", "TRK-3")
	e.closeAndPlan(t, "wave-1", "dsp-1", stop(1, "TRK-1", "TRK-2"), stop(2, "TRK-3"))
	_, err := usecases.NewDispatchWave(e.deps).Execute(context.Background(), "wave-1")
	noErr(t, err)
	return e
}

func (e *env) start(t *testing.T) {
	t.Helper()
	_, err := usecases.NewStartRoute(e.deps).Execute(context.Background(), "route-1")
	noErr(t, err)
}

func (e *env) attempt(tn string, o route.Outcome, reason string, at time.Time) (usecases.RecordAttemptCommand, error) {
	cmd := usecases.RecordAttemptCommand{RouteID: "route-1", TrackingNumber: tn, Outcome: o, FailureReason: reason, At: at}
	_, err := usecases.NewRecordAttempt(e.deps).Execute(context.Background(), cmd)
	return cmd, err
}

func TestPlanRouteSavesARoutePlannedEvent(t *testing.T) {
	e := dispatchedRoute(t, false)
	r := e.loadRoute(t, "route-1")
	if r.State() != route.StatePlanned || r.StationNodeID() != "node-ds-01" || len(r.Packages()) != 3 {
		t.Fatalf("route = %+v", r.Snapshot())
	}
	found := false
	for _, n := range e.eventNames() {
		found = found || n == "RoutePlanned:route-1"
	}
	if !found {
		t.Fatalf("events = %v, want RoutePlanned:route-1", e.eventNames())
	}
}

func TestPlanRouteRefusals(t *testing.T) {
	e := newEnv(t)
	e.station(t, "node-ds-01")
	e.wave(t, 2, 5)
	e.partner(t, 1)
	e.arrive(t, "ev-1", "TRK-1", "TRK-2")
	ctx := context.Background()
	_, err := usecases.NewCloseInduction(e.deps).Execute(ctx, "wave-1")
	noErr(t, err)
	plan := func(waveID, dsp string, stops ...route.Stop) error {
		_, err := usecases.NewPlanRoute(e.deps).Execute(ctx, usecases.PlanRouteCommand{WaveID: wave.ID(waveID), DSPID: partner.ID(dsp), Stops: stops})
		return err
	}
	e.wantUnchanged(t, func() {
		wantIs(t, plan("wave-404", "dsp-1", stop(1, "TRK-1")), ports.ErrNotFound)
		wantIs(t, plan("wave-1", "dsp-404", stop(1, "TRK-1")), ports.ErrNotFound)
		wantIs(t, plan("wave-1", "dsp-1", stop(1, "TRK-1"), stop(2, "TRK-2")), route.ErrRouteOverCapacity)
		wantIs(t, plan("wave-1", "dsp-1", stop(1, "TRK-9")), route.ErrPackageNotInducted)
	})
	noErr(t, plan("wave-1", "dsp-1", stop(1, "TRK-1")))
	e.wantUnchanged(t, func() {
		wantIs(t, plan("wave-1", "dsp-1", stop(1, "TRK-1")), route.ErrPackageAlreadyRouted)
	})
}

func TestStartRoute(t *testing.T) {
	e := newEnv(t)
	e.station(t, "node-ds-01")
	e.wave(t, 2, 5)
	e.partner(t, 2)
	e.arrive(t, "ev-1", "TRK-1")
	e.closeAndPlan(t, "wave-1", "dsp-1", stop(1, "TRK-1"))
	ctx := context.Background()
	e.wantUnchanged(t, func() {
		_, err := usecases.NewStartRoute(e.deps).Execute(ctx, "route-1") // wave not dispatched
		wantIs(t, err, shared.ErrIllegalTransition)
		_, err = usecases.NewStartRoute(e.deps).Execute(ctx, "route-404")
		wantIs(t, err, ports.ErrNotFound)
	})
	_, err := usecases.NewDispatchWave(e.deps).Execute(ctx, "wave-1")
	noErr(t, err)
	v, err := usecases.NewStartRoute(e.deps).Execute(ctx, "route-1")
	noErr(t, err)
	if v.State != "OUT_FOR_DELIVERY" {
		t.Fatalf("state = %s", v.State)
	}
	names := e.eventNames()
	if names[len(names)-1] != "PackageOutForDelivery:route-1" {
		t.Fatalf("events = %v", names)
	}
	e.wantUnchanged(t, func() {
		_, err := usecases.NewStartRoute(e.deps).Execute(ctx, "route-1")
		wantIs(t, err, shared.ErrIllegalTransition)
	})
}

func TestRecordAttemptDeliveredCompletesTheRouteAndTheWave(t *testing.T) {
	e := dispatchedRoute(t, false)
	e.start(t)
	for i, tn := range []string{"TRK-1", "TRK-2", "TRK-3"} {
		_, err := e.attempt(tn, route.OutcomeDelivered, "", t0.Add(8*time.Hour))
		noErr(t, err)
		wantRoute := route.StateOutForDelivery
		if i == 2 {
			wantRoute = route.StateCompleted
		}
		if got := e.loadRoute(t, "route-1").State(); got != wantRoute {
			t.Fatalf("after %d attempts the route is %s, want %s", i+1, got, wantRoute)
		}
	}
	if got := e.loadWave(t, "wave-1").State(); got != wave.StateCompleted {
		t.Fatalf("wave = %s, want COMPLETED when its last route completes", got)
	}
	for _, tn := range []string{"TRK-1", "TRK-2", "TRK-3"} {
		assertStatus(t, e, tn, history.StatusDelivered)
	}
	names := e.eventNames()
	if got := names[len(names)-3:]; got[0] != "DeliveryAttempted:route-1" || got[1] != "DeliveryAttempted:route-1" || got[2] != "DeliveryAttempted:route-1" {
		t.Fatalf("events = %v", names)
	}
}

func TestRecordAttemptFailedReinductsOntoTheNextWave(t *testing.T) {
	e := dispatchedRoute(t, true)
	e.start(t)
	v, err := usecases.NewRecordAttempt(e.deps).Execute(context.Background(), usecases.RecordAttemptCommand{
		RouteID: "route-1", TrackingNumber: "TRK-3", Outcome: route.OutcomeFailed, FailureReason: "recipient absent", At: t0.Add(8 * time.Hour),
	})
	noErr(t, err)
	if v.Outcome != "FAILED" || v.FailureReason != "recipient absent" || v.RouteID != "route-1" || v.TrackingNumber != "TRK-3" {
		t.Fatalf("view = %+v", v)
	}
	if _, ok := e.loadWave(t, "wave-2").Induction("TRK-3"); !ok {
		t.Fatal("a failed package must be re-inducted onto the next wave")
	}
	h, err := e.deps.Histories.Get(context.Background(), "TRK-3")
	noErr(t, err)
	if h.Status() != history.StatusInducted || h.FailedAttempts() != 1 {
		t.Fatalf("history = %+v", h.Snapshot())
	}
	names := e.eventNames()
	if got := names[len(names)-2:]; got[0] != "DeliveryAttempted:route-1" || got[1] != "DeliveryWaveCapacityChanged:wave-2" {
		t.Fatalf("events = %v", names)
	}
}

func TestRecordAttemptFailedWithNoWaveDefers(t *testing.T) {
	e := dispatchedRoute(t, false)
	e.start(t)
	_, err := e.attempt("TRK-3", route.OutcomeFailed, "absent", t0.Add(8*time.Hour))
	noErr(t, err)
	assertStatus(t, e, "TRK-3", history.StatusAtStation)
	names := e.eventNames()
	if got := names[len(names)-2:]; got[0] != "DeliveryAttempted:route-1" || got[1] != "PackageInductionDeferred:TRK-3" {
		t.Fatalf("events = %v", names)
	}
}

func TestThirdFailureInitiatesTheReturnAndDoesNotReinduct(t *testing.T) {
	e := dispatchedRoute(t, true)
	ctx := context.Background()
	for i := 0; i < history.MaxFailedAttempts-1; i++ { // two earlier failed attempts
		h, err := e.deps.Histories.Get(ctx, "TRK-3")
		noErr(t, err)
		_, err = h.RecordFailure(true, t0)
		noErr(t, err)
		noErr(t, e.deps.Histories.Save(ctx, h))
	}
	e.start(t)
	_, err := e.attempt("TRK-3", route.OutcomeFailed, "absent", t0.Add(8*time.Hour))
	noErr(t, err)
	assertStatus(t, e, "TRK-3", history.StatusReturnInitiated)
	if _, ok := e.loadWave(t, "wave-2").Induction("TRK-3"); ok {
		t.Fatal("a package returned to sender is not re-inducted")
	}
	names := e.eventNames()
	if got := names[len(names)-2:]; got[0] != "DeliveryAttempted:route-1" || got[1] != "PackageReturnInitiated:TRK-3" {
		t.Fatalf("events = %v", names)
	}
}

func TestRecordAttemptRefusals(t *testing.T) {
	e := dispatchedRoute(t, false)
	ctx := context.Background()
	at := t0.Add(8 * time.Hour)
	e.wantUnchanged(t, func() {
		_, err := e.attempt("TRK-1", route.OutcomeDelivered, "", at) // route not started
		wantIs(t, err, route.ErrRouteNotStarted)
		_, err = usecases.NewRecordAttempt(e.deps).Execute(ctx, usecases.RecordAttemptCommand{RouteID: "route-404", TrackingNumber: "TRK-1", Outcome: route.OutcomeDelivered, At: at})
		wantIs(t, err, ports.ErrNotFound)
	})
	e.start(t)
	e.wantUnchanged(t, func() {
		_, err := e.attempt("TRK-9", route.OutcomeDelivered, "", at)
		wantIs(t, err, route.ErrPackageNotOnRoute)
		_, err = e.attempt("TRK-1", route.OutcomeFailed, "", at)
		wantIs(t, err, shared.ErrValidation)
	})
	_, err := e.attempt("TRK-1", route.OutcomeDelivered, "", at)
	noErr(t, err)
	e.wantUnchanged(t, func() {
		_, err := e.attempt("TRK-1", route.OutcomeDelivered, "", at)
		wantIs(t, err, route.ErrAlreadyDelivered)
	})
}

func TestADeliveredPackageIsNotDeliveredAgainOnAnotherRoute(t *testing.T) {
	e := dispatchedRoute(t, false)
	e.start(t)
	ctx := context.Background()
	h, err := e.deps.Histories.Get(ctx, "TRK-1")
	noErr(t, err)
	noErr(t, h.RecordDelivered()) // delivered through some other route
	noErr(t, e.deps.Histories.Save(ctx, h))
	e.wantUnchanged(t, func() {
		_, err := e.attempt("TRK-1", route.OutcomeDelivered, "", t0.Add(8*time.Hour))
		wantIs(t, err, shared.ErrAlreadyDelivered)
	})
}

func TestRecordAttemptFailsWithoutAPackageHistory(t *testing.T) {
	e := dispatchedRoute(t, false)
	e.start(t)
	// A history lost from under a started route is corruption, not a client error.
	flaky := e.deps
	flaky.Histories = missingHistories{e.deps.Histories}
	_, err := usecases.NewRecordAttempt(flaky).Execute(context.Background(), usecases.RecordAttemptCommand{
		RouteID: "route-1", TrackingNumber: "TRK-1", Outcome: route.OutcomeDelivered, At: t0.Add(8 * time.Hour),
	})
	wantIs(t, err, ports.ErrNotFound)
}

type missingHistories struct{ ports.HistoryRepository }

func (missingHistories) Get(context.Context, string) (*history.History, error) {
	return nil, ports.ErrNotFound
}

func TestWaveStaysDispatchedWhileAnotherRouteIsOpen(t *testing.T) {
	e := newEnv(t)
	e.station(t, "node-ds-01")
	e.wave(t, 2, 5)
	e.partner(t, 2)
	e.arrive(t, "ev-1", "TRK-1", "TRK-2")
	e.closeAndPlan(t, "wave-1", "dsp-1", stop(1, "TRK-1"))
	_, err := usecases.NewPlanRoute(e.deps).Execute(context.Background(), usecases.PlanRouteCommand{WaveID: "wave-1", DSPID: "dsp-1", Stops: []route.Stop{stop(1, "TRK-2")}})
	noErr(t, err)
	_, err = usecases.NewDispatchWave(e.deps).Execute(context.Background(), "wave-1")
	noErr(t, err)
	e.start(t)
	_, err = e.attempt("TRK-1", route.OutcomeDelivered, "", t0.Add(8*time.Hour))
	noErr(t, err)
	if got := e.loadWave(t, "wave-1").State(); got != wave.StateDispatched {
		t.Fatalf("wave = %s, want DISPATCHED while route-2 is open", got)
	}
	if got := e.loadRoute(t, "route-1").State(); got != route.StateCompleted {
		t.Fatalf("route-1 = %s", got)
	}
}
