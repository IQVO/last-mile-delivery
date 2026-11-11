package analyticsstore_test

import (
	"errors"
	"testing"
	"time"

	"github.com/claudioed/last-mile-delivery/internal/analytics/report"
)

// eta applies one StopEtaWindowAssigned fact: the stop's packages and window.
func (f *facts) eta(route string, stopNo int, start, end time.Time, tracking ...string) {
	f.apply(report.Event{Kind: report.KindStopEtaWindowAssigned, RouteID: route, StopNo: stopNo, TrackingNumbers: tracking,
		EtaStart: start, EtaEnd: end, OccurredAt: h(nov3, -1)})
}

// seedEta writes the ETA scenario of ADR 0013. Range [nov3, nov5).
func seedEta(f *facts) {
	// R1 (ST1): stop 1 = A B in [08:00, 10:00], stop 2 = C in [10:00, 12:00].
	f.out("R1", "ST1", "A", "B", "C")
	f.eta("R1", 1, h(nov3, 8), h(nov3, 10), "A", "B")
	f.eta("R1", 2, h(nov3, 10), h(nov3, 12), "C")
	f.eta("R1", 1, h(nov3, 20), h(nov3, 22), "A") // a second window for the same package never replaces the first
	f.attempt("R1", "A", del, h(nov3, 8))         // exactly etaStart: inside
	f.attempt("R1", "B", del, h(nov3, 10))        // exactly etaEnd: inside
	f.attempt("R1", "C", del, h(nov3, 12).Add(time.Second))
	// R4 (ST1): F had a window but FAILED; it is delivered on R5, which has no window.
	f.out("R4", "ST1", "F")
	f.eta("R4", 1, h(nov3, 8), h(nov3, 10), "F")
	f.attempt("R4", "F", fail, h(nov3, 9))
	f.out("R5", "ST1", "F")
	f.attempt("R5", "F", del, h(nov4, 9))
	// R2 (ST2): D delivered an hour before its window.
	f.out("R2", "ST2", "D")
	f.eta("R2", 1, h(nov4, 8), h(nov4, 10), "D")
	f.attempt("R2", "D", del, h(nov4, 7))
	// R3 (ST3): E delivered with no window at all.
	f.out("R3", "ST3", "E")
	f.attempt("R3", "E", del, h(nov4, 11))
	// R6: H's route never projected its out-for-delivery fact: station UNKNOWN.
	f.eta("R6", 1, h(nov4, 12), h(nov4, 14), "H")
	f.attempt("R6", "H", del, h(nov4, 13))
	// Delivered exactly at To: outside the range.
	f.out("R7", "ST1", "G")
	f.eta("R7", 1, h(nov3, 8), h(nov3, 10), "G")
	f.attempt("R7", "G", del, fullRange.To)
}

func contractEta(t *testing.T, s store) {
	seedEta(&facts{t: t, s: s})
	got, err := s.EtaWindow(ctx, fullRange)
	if err != nil {
		t.Fatal(err)
	}
	want := []report.EtaDay{
		{Day: nov3, Station: "ST1", DeliveriesWithEta: 3, DeliveriesInside: 2, DeliveriesAfter: 1},
		{Day: nov4, Station: "ST1"},
		{Day: nov4, Station: "ST2", DeliveriesWithEta: 1, DeliveriesBefore: 1},
		{Day: nov4, Station: "ST3"},
		{Day: nov4, Station: report.UnknownStation(), DeliveriesWithEta: 1, DeliveriesInside: 1},
	}
	sameDays(t, "eta windows", got, want, func(d report.EtaDay) time.Time { return d.Day })
	one, err := s.EtaWindow(ctx, report.Range{From: nov4, To: fullRange.To})
	if err != nil || len(one) != 4 || !one[0].Day.Equal(nov4) {
		t.Fatalf("a one-day range = %+v, %v", one, err)
	}
}

func contractEtaRejected(t *testing.T, s store) {
	ok := report.Event{Kind: report.KindStopEtaWindowAssigned, RouteID: "R1", StopNo: 1, TrackingNumbers: []string{"A"},
		EtaStart: h(nov3, 8), EtaEnd: h(nov3, 10), At: nov3, OccurredAt: nov3}
	bad := map[string]func(e report.Event) report.Event{
		"no stopNo":            func(e report.Event) report.Event { e.StopNo = 0; return e },
		"no tracking number":   func(e report.Event) report.Event { e.TrackingNumbers = nil; return e },
		"a blank tracking no.": func(e report.Event) report.Event { e.TrackingNumbers = []string{"A", ""}; return e },
		"an empty window":      func(e report.Event) report.Event { e.EtaEnd = e.EtaStart; return e },
		"an inverted window":   func(e report.Event) report.Event { e.EtaEnd = h(nov3, 7); return e },
		"no window":            func(e report.Event) report.Event { e.EtaStart, e.EtaEnd = time.Time{}, time.Time{}; return e },
		"no routeId":           func(e report.Event) report.Event { e.RouteID = ""; return e },
	}
	for name, mutate := range bad {
		e := mutate(ok)
		e.EventID = "evt-eta-" + name
		if applied, err := s.Apply(ctx, e); applied || !errors.Is(err, report.ErrRejected) {
			t.Errorf("%s: applied=%v err=%v; want ErrRejected", name, applied, err)
		}
	}
	if last, err := s.LastEventAt(ctx); err != nil || last != nil {
		t.Fatalf("a rejected fact must leave no mark, last = %v, %v", last, err)
	}
}
