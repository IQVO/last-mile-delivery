package deliverypoint

import (
	"reflect"
	"strconv"
	"testing"
	"time"

	"github.com/claudioed/last-mile-delivery/internal/domain/route"
)

// ADR 0018: a strike names every package of the visit that failed at the stop,
// and a DeliveryPointFlagged carries every package it newly affects.

func wantPackages(t *testing.T, ev DeliveryPointFlagged, failures int, tns ...string) {
	t.Helper()
	if !reflect.DeepEqual(ev.TrackingNumbers, tns) || ev.Failures != failures {
		t.Fatalf("event = %+v, want %d failures for %v", ev, failures, tns)
	}
}

func TestTwoPackagesOfOneVisitAreBothFlagged(t *testing.T) {
	p := fresh(t)
	wantNoEvent(t, notHome(t, p, "route-1", "TRK-1", day0))
	wantPackages(t, flagged(t, notHome(t, p, "route-2", "TRK-2", days(1))), 2, "TRK-1", "TRK-2")
	// The second package of the visit fails a minute later: it joins the strike
	// of its route and is told on its own, the first one is not told again.
	ev := flagged(t, notHome(t, p, "route-2", "TRK-3", days(1).Add(time.Minute)))
	wantPackages(t, ev, 2, "TRK-3")
	if !ev.FlaggedAt.Equal(days(1).Add(time.Minute)) || !p.FlaggedAt().Equal(days(1)) {
		t.Fatalf("flaggedAt = %v, the profile flag opened at %v", ev.FlaggedAt, p.FlaggedAt())
	}
	snap := p.Snapshot()
	want := []Strike{
		{RouteID: "route-1", TrackingNumbers: []string{"TRK-1"}, Category: CategoryNotHome, At: day0, Told: []string{"TRK-1"}},
		{RouteID: "route-2", TrackingNumbers: []string{"TRK-2", "TRK-3"}, Category: CategoryNotHome, At: days(1), Told: []string{"TRK-2", "TRK-3"}},
	}
	if !reflect.DeepEqual(snap.Strikes, want) || p.OpenStrikes() != 2 || p.FailedNotHome() != 3 {
		t.Fatalf("snapshot = %+v", snap)
	}
}

func TestAVisitBeforeTheFlagNamesEveryPackageOfItInTheFirstEvent(t *testing.T) {
	p := fresh(t)
	wantNoEvent(t, notHome(t, p, "route-1", "TRK-1", day0))
	wantNoEvent(t, notHome(t, p, "route-1", "TRK-2", day0.Add(time.Minute)))
	wantNoEvent(t, notHome(t, p, "route-1", "TRK-3", day0.Add(2*time.Minute)))
	if p.OpenStrikes() != 1 || p.Flagged() {
		t.Fatalf("one visit is one strike: %+v", p.Snapshot())
	}
	wantPackages(t, flagged(t, notHome(t, p, "route-2", "TRK-4", days(1))), 2, "TRK-1", "TRK-2", "TRK-3", "TRK-4")
}

func TestALaterVisitFlagsOnlyTheNewlyAffectedPackages(t *testing.T) {
	p := fresh(t)
	notHome(t, p, "route-1", "TRK-1", day0)
	notHome(t, p, "route-1", "TRK-2", day0)
	// The two packages come back on route-2: the first report raises the flag for
	// both, the second finds nothing new.
	wantPackages(t, flagged(t, notHome(t, p, "route-2", "TRK-1", days(1))), 2, "TRK-1", "TRK-2")
	wantNoEvent(t, notHome(t, p, "route-2", "TRK-2", days(1)))
	// A third visit brings the two again and one more: only the new one is told.
	wantNoEvent(t, notHome(t, p, "route-3", "TRK-1", days(2)))
	wantNoEvent(t, notHome(t, p, "route-3", "TRK-2", days(2)))
	wantPackages(t, flagged(t, notHome(t, p, "route-3", "TRK-4", days(2))), 3, "TRK-4")
	if p.OpenStrikes() != 3 {
		t.Fatalf("snapshot = %+v", p.Snapshot())
	}
}

func TestAPackageReportedTwiceInOneVisitIsNamedOnce(t *testing.T) {
	p := fresh(t)
	notHome(t, p, "route-1", "TRK-1", day0)
	notHome(t, p, "route-1", "TRK-1", day0.Add(time.Minute))
	if got := p.Snapshot().Strikes[0].TrackingNumbers; !reflect.DeepEqual(got, []string{"TRK-1"}) {
		t.Fatalf("tracking numbers = %v", got)
	}
}

func TestOnlyACountedFailureJoinsTheStrike(t *testing.T) {
	p := fresh(t)
	notHome(t, p, "route-1", "TRK-1", day0)
	seen(t, p, "route-1", "TRK-2", route.OutcomeFailed, route.DetailOther, day0)
	seen(t, p, "route-1", "TRK-3", route.OutcomeFailed, "", day0)
	seen(t, p, "route-1", "TRK-4", route.OutcomeDelivered, "", day0)
	seen(t, p, "route-1", "TRK-5", route.OutcomeFailed, route.DetailAccessProblem, day0)
	wantPackages(t, flagged(t, notHome(t, p, "route-2", "TRK-6", days(1))), 2, "TRK-1", "TRK-5", "TRK-6")
}

func TestAPackageOfAnotherRouteDoesNotJoinAVisit(t *testing.T) {
	p := fresh(t)
	notHome(t, p, "route-1", "TRK-1", day0)
	notHome(t, p, "route-2", "TRK-2", day0)
	snap := p.Snapshot()
	if len(snap.Strikes) != 2 || !reflect.DeepEqual(snap.Strikes[0].TrackingNumbers, []string{"TRK-1"}) ||
		!reflect.DeepEqual(snap.Strikes[1].TrackingNumbers, []string{"TRK-2"}) {
		t.Fatalf("strikes = %+v", snap.Strikes)
	}
}

func TestAToldPackageStaysToldWhenItsFirstStrikeExpires(t *testing.T) {
	p := fresh(t)
	notHome(t, p, "route-1", "TRK-1", day0)
	flagged(t, notHome(t, p, "route-2", "TRK-2", days(1)))
	wantNoEvent(t, notHome(t, p, "route-3", "TRK-1", days(2))) // told with the first pair
	// Day 61 drops the strike of day 0 only: TRK-1 stays open through route-3 and
	// must not be told again beside the new package.
	wantPackages(t, flagged(t, notHome(t, p, "route-4", "TRK-5", days(61))), 3, "TRK-5")
	snap := p.Snapshot()
	if got := snap.Strikes[1].Told; !reflect.DeepEqual(got, []string{"TRK-1"}) {
		t.Fatalf("the later strike of a told package keeps it told: %+v", snap.Strikes)
	}
	if _, err := Restore(snap); err != nil {
		t.Fatalf("Restore: %v", err)
	}
}

func TestMoreThanFivePackagesAreSplitOverSeveralEvents(t *testing.T) {
	cases := []struct {
		name   string
		count  int
		events []int
	}{
		{"five fit one event", 5, []int{5}},
		{"six need two", 6, []int{5, 1}},
		{"eleven need three", 11, []int{5, 5, 1}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := fresh(t)
			for i := 1; i < c.count; i++ {
				wantNoEvent(t, notHome(t, p, "route-1", "TRK-"+strconv.Itoa(i), day0))
			}
			ev := notHome(t, p, "route-2", "TRK-"+strconv.Itoa(c.count), days(1))
			var sizes []int
			var all []string
			for _, e := range ev {
				f, ok := e.(DeliveryPointFlagged)
				if !ok || f.Failures != 2 || f.Version != p.Version() || f.StopKey != key || f.Reason != CategoryNotHome || !f.FlaggedAt.Equal(days(1)) {
					t.Fatalf("event = %#v", e)
				}
				sizes, all = append(sizes, len(f.TrackingNumbers)), append(all, f.TrackingNumbers...)
			}
			if !reflect.DeepEqual(sizes, c.events) || !reflect.DeepEqual(all, packages(c.count)) {
				t.Fatalf("events carry %v packages, want %v: %v", sizes, c.events, all)
			}
			wantNoEvent(t, notHome(t, p, "route-3", "TRK-1", days(2))) // nobody is told twice
		})
	}
}

func TestAVisitNamesAtMostMaxStrikePackages(t *testing.T) {
	p := fresh(t)
	for _, tn := range packages(MaxStrikePackages + 1) {
		notHome(t, p, "route-1", tn, day0)
	}
	snap := p.Snapshot()
	if got := snap.Strikes[0].TrackingNumbers; !reflect.DeepEqual(got, packages(MaxStrikePackages)) {
		t.Fatalf("a strike names %d packages, want the first %d", len(got), MaxStrikePackages)
	}
	if p.FailedNotHome() != MaxStrikePackages+1 || p.Attempts() != MaxStrikePackages+1 {
		t.Fatalf("the counters keep every attempt: %+v", snap)
	}
	if _, err := Restore(snap); err != nil {
		t.Fatalf("Restore: %v", err)
	}
}

func TestTheVisitStrikeKeepsItsPlaceInTheRing(t *testing.T) {
	p := fresh(t)
	for i := 1; i <= MaxStrikes; i++ {
		notHome(t, p, "route-"+strconv.Itoa(i), "TRK-"+strconv.Itoa(i), days(i))
	}
	// The oldest visit is the first one in the ring: a package of it still joins.
	notHome(t, p, "route-1", "TRK-9", days(MaxStrikes))
	got := p.Snapshot().Strikes
	if len(got) != MaxStrikes || !reflect.DeepEqual(got[0].TrackingNumbers, []string{"TRK-1", "TRK-9"}) {
		t.Fatalf("strikes = %+v", got)
	}
	// A package of a visit the ring already dropped opens a new strike.
	notHome(t, p, "route-6", "TRK-6", days(MaxStrikes+1))
	notHome(t, p, "route-1", "TRK-7", days(MaxStrikes+2))
	got = p.Snapshot().Strikes
	if len(got) != MaxStrikes || got[MaxStrikes-1].RouteID != "route-1" || !reflect.DeepEqual(got[MaxStrikes-1].TrackingNumbers, []string{"TRK-7"}) {
		t.Fatalf("strikes = %+v", got)
	}
}
