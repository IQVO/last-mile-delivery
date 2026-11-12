package deliverypoint

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/claudioed/last-mile-delivery/internal/domain/route"
	"github.com/claudioed/last-mile-delivery/internal/domain/shared"
)

const key = "9f2c4a1e7b3d"

var day0 = time.Date(2026, 11, 2, 9, 0, 0, 0, time.UTC)

func days(n int) time.Time { return day0.Add(time.Duration(n) * 24 * time.Hour) }

func wantNoErr(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func fresh(t *testing.T) *Profile {
	t.Helper()
	p, err := New(key)
	wantNoErr(t, err)
	return p
}

// seen observes one attempt and returns the raised events.
func seen(t *testing.T, p *Profile, routeID, tn string, o route.Outcome, d route.Detail, at time.Time) []shared.Event {
	t.Helper()
	ev, err := p.Observe(Observation{RouteID: routeID, TrackingNumber: tn, Outcome: o, Detail: d, At: at}, at.Add(time.Second))
	wantNoErr(t, err)
	return ev
}

func notHome(t *testing.T, p *Profile, routeID, tn string, at time.Time) []shared.Event {
	t.Helper()
	return seen(t, p, routeID, tn, route.OutcomeFailed, route.DetailNotHome, at)
}

func delivered(t *testing.T, p *Profile, routeID, tn string, at time.Time) []shared.Event {
	t.Helper()
	return seen(t, p, routeID, tn, route.OutcomeDelivered, "", at)
}

func flagged(t *testing.T, ev []shared.Event) DeliveryPointFlagged {
	t.Helper()
	if len(ev) != 1 {
		t.Fatalf("events = %#v, want exactly one DeliveryPointFlagged", ev)
	}
	got, ok := ev[0].(DeliveryPointFlagged)
	if !ok {
		t.Fatalf("event = %#v", ev[0])
	}
	return got
}

func wantNoEvent(t *testing.T, ev []shared.Event) {
	t.Helper()
	if len(ev) != 0 {
		t.Fatalf("events = %#v, want none", ev)
	}
}

func TestNewRefusesAnAddressLessKey(t *testing.T) {
	for _, k := range []string{"", " ", "pkg:TRK-1", " padded"} {
		if _, err := New(k); !errors.Is(err, shared.ErrValidation) {
			t.Errorf("New(%q) error = %v, want a validation error", k, err)
		}
	}
	p := fresh(t)
	if p.StopKey() != key || p.Version() != 0 || p.Flagged() || p.FlagReason() != "" {
		t.Fatalf("new profile = %+v", p.Snapshot())
	}
}

func TestFirstStrikeDoesNotFlagAndTheSecondDoes(t *testing.T) {
	p := fresh(t)
	wantNoEvent(t, notHome(t, p, "route-1", "TRK-1", day0))
	if p.OpenStrikes() != 1 || p.Flagged() || p.Version() != 1 {
		t.Fatalf("after one strike: %+v", p.Snapshot())
	}
	ev := flagged(t, notHome(t, p, "route-2", "TRK-2", days(1)))
	want := DeliveryPointFlagged{
		Header: shared.Header{Version: 2, At: days(1).Add(time.Second)}, StopKey: key, Reason: CategoryNotHome, Failures: 2,
		TrackingNumbers: []string{"TRK-1", "TRK-2"}, FlaggedAt: days(1),
	}
	if !reflect.DeepEqual(ev, want) {
		t.Fatalf("event = %#v\nwant    %#v", ev, want)
	}
	if ev.EventName() != "DeliveryPointFlagged" || ev.Entity() != "deliverypointprofile" || ev.SubjectID() != key {
		t.Fatal("event shape wrong")
	}
	if !p.Flagged() || !p.FlaggedAt().Equal(days(1)) || p.FlagReason() != CategoryNotHome {
		t.Fatalf("flag = %v %v %q", p.Flagged(), p.FlaggedAt(), p.FlagReason())
	}
}

func TestAccessProblemWinsTheReason(t *testing.T) {
	p := fresh(t)
	notHome(t, p, "route-1", "TRK-1", day0)
	ev := flagged(t, seen(t, p, "route-2", "TRK-2", route.OutcomeFailed, route.DetailAccessProblem, days(1)))
	if ev.Reason != CategoryAccessProblem || p.FlagReason() != CategoryAccessProblem {
		t.Fatalf("reason = %q", ev.Reason)
	}
	if p.FailedNotHome() != 1 || p.FailedAccess() != 1 {
		t.Fatalf("counters = %+v", p.Snapshot())
	}
}

func TestTwoParcelsOnOneRouteAreOneStrike(t *testing.T) {
	p := fresh(t)
	wantNoEvent(t, notHome(t, p, "route-1", "TRK-1", day0))
	wantNoEvent(t, notHome(t, p, "route-1", "TRK-2", day0.Add(time.Minute)))
	if p.OpenStrikes() != 1 || p.Flagged() || p.FailedNotHome() != 2 || p.Attempts() != 2 {
		t.Fatalf("snapshot = %+v", p.Snapshot())
	}
}

func TestOtherAndUnreportedAreTalliedNeverCounted(t *testing.T) {
	p := fresh(t)
	for i, d := range []route.Detail{route.DetailOther, "", route.DetailOther, ""} {
		wantNoEvent(t, seen(t, p, "route-"+string(rune('1'+i)), "TRK-"+string(rune('1'+i)), route.OutcomeFailed, d, days(i)))
	}
	if p.OpenStrikes() != 0 || p.Flagged() || p.FailedOther() != 4 || p.Attempts() != 4 {
		t.Fatalf("snapshot = %+v", p.Snapshot())
	}
}

func TestStrikeWindowExpiry(t *testing.T) {
	cases := []struct {
		name     string
		gapDays  int
		wantFlag bool
	}{{"59 days apart still counts", 59, true}, {"60 days apart still counts", 60, true}, {"61 days apart no longer counts", 61, false}}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := fresh(t)
			notHome(t, p, "route-1", "TRK-1", day0)
			ev := notHome(t, p, "route-2", "TRK-2", days(c.gapDays))
			if got := len(ev) == 1; got != c.wantFlag || p.Flagged() != c.wantFlag {
				t.Fatalf("flag = %v (events %d), want %v", p.Flagged(), len(ev), c.wantFlag)
			}
			if !c.wantFlag && p.OpenStrikes() != 1 {
				t.Fatalf("the old strike must be dropped: %+v", p.Snapshot())
			}
		})
	}
}

func TestAnExpiredFlagClosesAndTheNextPairReopensIt(t *testing.T) {
	p := fresh(t)
	notHome(t, p, "route-1", "TRK-1", day0)
	flagged(t, notHome(t, p, "route-2", "TRK-2", days(1)))
	wantNoEvent(t, notHome(t, p, "route-3", "TRK-3", days(200))) // both old strikes expired, one new strike
	if p.Flagged() || p.OpenStrikes() != 1 {
		t.Fatalf("flag should be closed: %+v", p.Snapshot())
	}
	ev := flagged(t, notHome(t, p, "route-4", "TRK-4", days(201)))
	if !reflect.DeepEqual(ev.TrackingNumbers, []string{"TRK-3", "TRK-4"}) || ev.Failures != 2 {
		t.Fatalf("event = %+v", ev)
	}
}

func TestAnOlderReportKeepsTheWindowAnchoredAtTheNewestAttempt(t *testing.T) {
	p := fresh(t)
	notHome(t, p, "route-1", "TRK-1", days(100))
	notHome(t, p, "route-2", "TRK-2", days(101)) // flagged
	// A late report of an attempt made 90 days before the newest one: the window
	// is measured from the newest attempt time, so the strikes of day 100 and 101
	// stay and the late package is told on its own.
	ev := flagged(t, notHome(t, p, "route-3", "TRK-3", days(11)))
	if !reflect.DeepEqual(ev.TrackingNumbers, []string{"TRK-3"}) || ev.Failures != 3 || !p.LastAttemptAt().Equal(days(101)) {
		t.Fatalf("event = %+v, snapshot = %+v", ev, p.Snapshot())
	}
	// The stale strike is dropped by the next observation.
	ev = flagged(t, notHome(t, p, "route-4", "TRK-4", days(102)))
	if !reflect.DeepEqual(ev.TrackingNumbers, []string{"TRK-4"}) || ev.Failures != 3 {
		t.Fatalf("event = %+v", ev)
	}
}
