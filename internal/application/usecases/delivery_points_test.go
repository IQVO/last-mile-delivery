package usecases_test

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/claudioed/last-mile-delivery/internal/application/ports"
	"github.com/claudioed/last-mile-delivery/internal/application/usecases"
	"github.com/claudioed/last-mile-delivery/internal/domain/deliverypoint"
	"github.com/claudioed/last-mile-delivery/internal/domain/route"
	"github.com/claudioed/last-mile-delivery/internal/domain/shared"
)

type faultyPoints struct {
	ports.DeliveryPointRepository
	f *faults
}

func (r faultyPoints) Get(ctx context.Context, key string) (*deliverypoint.Profile, error) {
	if err := r.f.hit(); err != nil {
		return nil, err
	}
	return r.DeliveryPointRepository.Get(ctx, key)
}

func (r faultyPoints) Save(ctx context.Context, p *deliverypoint.Profile) error {
	if err := r.f.hit(); err != nil {
		return err
	}
	return r.DeliveryPointRepository.Save(ctx, p)
}

// seedStrike stores a profile of the stop key that holds one counted strike of
// an earlier route, so the next counted failure there is the second.
func (e *env) seedStrike(t *testing.T, stopKey, routeID, tn string) {
	t.Helper()
	p, err := deliverypoint.New(stopKey)
	noErr(t, err)
	at := t0.Add(-24 * time.Hour)
	_, err = p.Observe(deliverypoint.Observation{RouteID: routeID, TrackingNumber: tn, Outcome: route.OutcomeFailed, Detail: route.DetailNotHome, At: at}, at)
	noErr(t, err)
	noErr(t, e.deps.DeliveryPoints.Save(context.Background(), p))
}

func (e *env) attemptWith(tn string, o route.Outcome, d route.Detail, at time.Time) error {
	reason := ""
	if o == route.OutcomeFailed {
		reason = "recipient absent"
	}
	_, err := usecases.NewRecordAttempt(e.deps).Execute(context.Background(), usecases.RecordAttemptCommand{
		RouteID: "route-1", TrackingNumber: tn, Outcome: o, Detail: d, FailureReason: reason, At: at,
	})
	return err
}

func (e *env) profile(t *testing.T, stopKey string) *deliverypoint.Profile {
	t.Helper()
	p, err := e.deps.DeliveryPoints.Get(context.Background(), stopKey)
	noErr(t, err)
	return p
}

func TestAPackageWithNoLabelDataHasNoProfile(t *testing.T) {
	e := dispatchedRoute(t, true)
	e.start(t)
	at := t0.Add(8 * time.Hour)
	noErr(t, e.attemptWith("TRK-3", route.OutcomeFailed, route.DetailNotHome, at)) // TRK-3 is its own stop: pkg:TRK-3
	if got := e.store.Counts().DeliveryPoints; got != 0 {
		t.Fatalf("profiles = %d, want none for a pkg: stop", got)
	}
	for _, n := range e.eventNames() {
		if n == "DeliveryPointFlagged:pkg:TRK-3" {
			t.Fatal("a pkg: stop must publish nothing")
		}
	}
}

func TestAFirstCountedFailureCreatesTheProfileWithoutAFlag(t *testing.T) {
	e := dispatchedRoute(t, true)
	e.start(t)
	at := t0.Add(8 * time.Hour)
	noErr(t, e.attemptWith("TRK-1", route.OutcomeFailed, route.DetailNotHome, at))
	noErr(t, e.attemptWith("TRK-2", route.OutcomeFailed, route.DetailNotHome, at.Add(time.Minute))) // same visit, same stop
	p := e.profile(t, "k-a")
	if p.Attempts() != 2 || p.FailedNotHome() != 2 || p.OpenStrikes() != 1 || p.Flagged() || p.Version() != 2 {
		t.Fatalf("profile = %+v", p.Snapshot())
	}
	for _, n := range e.eventNames() {
		if n == "DeliveryPointFlagged:k-a" {
			t.Fatal("one visit is one strike: no flag")
		}
	}
}

func TestAnUnreportedFailureIsTalliedAndNeverCounted(t *testing.T) {
	e := dispatchedRoute(t, true)
	e.start(t)
	noErr(t, e.attemptWith("TRK-1", route.OutcomeFailed, "", t0.Add(8*time.Hour)))
	p := e.profile(t, "k-a")
	if p.FailedOther() != 1 || p.OpenStrikes() != 0 || p.Flagged() {
		t.Fatalf("profile = %+v", p.Snapshot())
	}
}

func TestTheSecondCountedVisitFlagsInTheSameTransaction(t *testing.T) {
	e := dispatchedRoute(t, true)
	e.seedStrike(t, "k-a", "route-0", "TRK-0")
	m := &recordingMetrics{}
	e.deps.Metrics = m
	e.start(t)
	before := len(e.eventNames())
	noErr(t, e.attemptWith("TRK-1", route.OutcomeFailed, route.DetailNotHome, t0.Add(8*time.Hour)))
	names := e.eventNames()[before:]
	if !reflect.DeepEqual(names, []string{"DeliveryAttempted:route-1", "DeliveryWaveCapacityChanged:wave-2", "DeliveryPointFlagged:k-a"}) {
		t.Fatalf("outbox events = %v", names)
	}
	ev := e.store.Outbox().Events()[len(e.store.Outbox().Events())-1].(deliverypoint.DeliveryPointFlagged)
	if !reflect.DeepEqual(ev.TrackingNumbers, []string{"TRK-0", "TRK-1"}) || ev.Failures != 2 || ev.Reason != deliverypoint.CategoryNotHome {
		t.Fatalf("event = %+v", ev)
	}
	if !reflect.DeepEqual(m.calls, []string{"failed", "flagged/NOT_HOME"}) {
		t.Fatalf("metric calls = %v", m.calls)
	}
	if p := e.profile(t, "k-a"); !p.Flagged() || p.OpenStrikes() != 2 {
		t.Fatalf("profile = %+v", p.Snapshot())
	}
}

func TestADeliveryAtAFlaggedPointEndsTheEpisode(t *testing.T) {
	e := dispatchedRoute(t, true)
	e.seedStrike(t, "k-a", "route-0", "TRK-0")
	e.start(t)
	at := t0.Add(8 * time.Hour)
	noErr(t, e.attemptWith("TRK-1", route.OutcomeFailed, route.DetailAccessProblem, at))
	// TRK-2 shares the stop and the route: the visit already failed, so a
	// delivery there is not a recovery.
	noErr(t, e.attemptWith("TRK-2", route.OutcomeDelivered, route.DetailSafePlaceUsed, at.Add(time.Minute)))
	p := e.profile(t, "k-a")
	if !p.Flagged() || p.SafePlaceUsed() != 1 || p.FlagReason() != deliverypoint.CategoryAccessProblem {
		t.Fatalf("profile = %+v", p.Snapshot())
	}
}

func TestAnInvalidDetailIsRefusedAndChangesNothing(t *testing.T) {
	e := dispatchedRoute(t, true)
	e.start(t)
	e.wantUnchanged(t, func() {
		wantIs(t, e.attemptWith("TRK-1", route.OutcomeFailed, route.DetailSafePlaceUsed, t0.Add(8*time.Hour)), shared.ErrValidation)
	})
}
