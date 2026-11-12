package deliverypoint

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/claudioed/last-mile-delivery/internal/domain/route"
	"github.com/claudioed/last-mile-delivery/internal/domain/shared"
)

func TestALaterParcelOfAnotherOrderIsToldOnItsOwn(t *testing.T) {
	p := fresh(t)
	notHome(t, p, "route-1", "TRK-1", day0)
	flagged(t, notHome(t, p, "route-2", "TRK-2", days(1)))
	ev := flagged(t, notHome(t, p, "route-3", "TRK-3", days(2)))
	if !reflect.DeepEqual(ev.TrackingNumbers, []string{"TRK-3"}) || ev.Failures != 3 || !ev.FlaggedAt.Equal(days(2)) {
		t.Fatalf("event = %+v", ev)
	}
	if !p.FlaggedAt().Equal(days(1)) {
		t.Fatalf("the flag opened at the first pair, not at %v", p.FlaggedAt())
	}
}

func TestOnePackageIsToldOnce(t *testing.T) {
	p := fresh(t)
	notHome(t, p, "route-1", "TRK-1", day0)
	flagged(t, notHome(t, p, "route-2", "TRK-2", days(1)))
	// TRK-1 fails again on a new route: a new strike, but the package was told.
	wantNoEvent(t, notHome(t, p, "route-3", "TRK-1", days(2)))
	if p.OpenStrikes() != 3 || !p.Flagged() {
		t.Fatalf("snapshot = %+v", p.Snapshot())
	}
}

func TestAPackageThatFailsTwiceBeforeTheFlagIsInOneEvent(t *testing.T) {
	p := fresh(t)
	notHome(t, p, "route-1", "TRK-1", day0)
	ev := flagged(t, notHome(t, p, "route-2", "TRK-1", days(1)))
	if !reflect.DeepEqual(ev.TrackingNumbers, []string{"TRK-1"}) || ev.Failures != 2 {
		t.Fatalf("event = %+v", ev)
	}
}

func TestTheFlagReopensWhenTheSamePackageFailsAfterAnExpiredEpisode(t *testing.T) {
	p := fresh(t)
	notHome(t, p, "route-1", "TRK-1", day0)
	flagged(t, notHome(t, p, "route-2", "TRK-2", days(1)))
	// Day 61 drops the strike of day 0 only: one told strike (TRK-2) remains and
	// a fresh strike of the told package makes two, with nobody new to tell.
	wantNoEvent(t, notHome(t, p, "route-3", "TRK-2", days(61)))
	if !p.Flagged() || p.OpenStrikes() != 2 {
		t.Fatalf("a restorable state needs the flag open with two strikes: %+v", p.Snapshot())
	}
	if _, err := Restore(p.Snapshot()); err != nil {
		t.Fatalf("Restore: %v", err)
	}
}

func TestRingOverflowDropsTheOldestStrike(t *testing.T) {
	p := fresh(t)
	for i := 1; i <= 6; i++ {
		notHome(t, p, "route-"+string(rune('0'+i)), "TRK-"+string(rune('0'+i)), days(i))
	}
	snap := p.Snapshot()
	if len(snap.Strikes) != MaxStrikes || snap.Strikes[0].RouteID != "route-2" || snap.Strikes[MaxStrikes-1].RouteID != "route-6" {
		t.Fatalf("strikes = %+v", snap.Strikes)
	}
	if p.FailedNotHome() != 6 || p.Attempts() != 6 {
		t.Fatalf("counters keep the history the ring drops: %+v", snap)
	}
	if _, err := Restore(snap); err != nil {
		t.Fatalf("Restore: %v", err)
	}
}

func TestADeliveryEndsTheEpisode(t *testing.T) {
	p := fresh(t)
	notHome(t, p, "route-1", "TRK-1", day0)
	flagged(t, notHome(t, p, "route-2", "TRK-2", days(1)))
	wantNoEvent(t, delivered(t, p, "route-3", "TRK-3", days(2)))
	if p.Flagged() || p.OpenStrikes() != 0 || p.Delivered() != 1 || p.Attempts() != 3 {
		t.Fatalf("snapshot = %+v", p.Snapshot())
	}
	// The next failure starts a new episode: one strike, no flag.
	wantNoEvent(t, notHome(t, p, "route-4", "TRK-4", days(3)))
	if p.Flagged() || p.OpenStrikes() != 1 {
		t.Fatalf("snapshot = %+v", p.Snapshot())
	}
}

func TestADeliveryOnAFailedRouteIsNotARecovery(t *testing.T) {
	p := fresh(t)
	notHome(t, p, "route-1", "TRK-1", day0)
	flagged(t, notHome(t, p, "route-2", "TRK-2", days(1)))
	wantNoEvent(t, delivered(t, p, "route-2", "TRK-5", days(1))) // same visit: TRK-2 failed there
	if !p.Flagged() || p.OpenStrikes() != 2 || p.Delivered() != 1 {
		t.Fatalf("snapshot = %+v", p.Snapshot())
	}
}

func TestSafePlaceIsTalliedOnDeliveries(t *testing.T) {
	p := fresh(t)
	seen(t, p, "route-1", "TRK-1", route.OutcomeDelivered, route.DetailSafePlaceUsed, day0)
	delivered(t, p, "route-2", "TRK-2", days(1))
	if p.SafePlaceUsed() != 1 || p.Delivered() != 2 || p.Attempts() != 2 {
		t.Fatalf("snapshot = %+v", p.Snapshot())
	}
}

func TestCountersSaturate(t *testing.T) {
	s := Snapshot{
		StopKey: key, Attempts: shared.MaxInt32, Delivered: shared.MaxInt32, SafePlaceUsed: shared.MaxInt32, Version: 7, LastAttemptAt: day0,
	}
	p, err := Restore(s)
	wantNoErr(t, err)
	seen(t, p, "route-1", "TRK-1", route.OutcomeDelivered, route.DetailSafePlaceUsed, days(1))
	notHome(t, p, "route-2", "TRK-2", days(2))
	got := p.Snapshot()
	if got.Attempts != shared.MaxInt32 || got.Delivered != shared.MaxInt32 || got.SafePlaceUsed != shared.MaxInt32 || got.FailedNotHome != 1 {
		t.Fatalf("snapshot = %+v", got)
	}
	if _, err := Restore(got); err != nil {
		t.Fatalf("a saturated profile must restore: %v", err)
	}
}

func TestAnInvalidObservationChangesNothing(t *testing.T) {
	good := Observation{RouteID: "route-1", TrackingNumber: "TRK-1", Outcome: route.OutcomeFailed, Detail: route.DetailNotHome, At: day0}
	cases := map[string]func(*Observation){
		"blank route":         func(o *Observation) { o.RouteID = "" },
		"bad tracking number": func(o *Observation) { o.TrackingNumber = " x" },
		"zero instant":        func(o *Observation) { o.At = time.Time{} },
		"unknown outcome":     func(o *Observation) { o.Outcome = "LOST" },
		"safe place on fail":  func(o *Observation) { o.Detail = route.DetailSafePlaceUsed },
		"not home delivered":  func(o *Observation) { o.Outcome = route.OutcomeDelivered },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			o := good
			mutate(&o)
			p := fresh(t)
			before := p.Snapshot()
			ev, err := p.Observe(o, day0)
			if !errors.Is(err, shared.ErrValidation) || ev != nil || !reflect.DeepEqual(p.Snapshot(), before) {
				t.Fatalf("Observe = %v, %v; snapshot %+v", ev, err, p.Snapshot())
			}
		})
	}
}
