package route

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/claudioed/last-mile-delivery/internal/domain/partner"
	"github.com/claudioed/last-mile-delivery/internal/domain/shared"
	"github.com/claudioed/last-mile-delivery/internal/domain/wave"
)

var (
	t0     = time.Date(2026, 10, 24, 6, 0, 0, 0, time.UTC)
	cutoff = time.Date(2026, 10, 24, 8, 0, 0, 0, time.UTC)
	t1     = time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	t2     = t1.Add(2 * time.Hour)
)

func wantIs(t *testing.T, err, target error) {
	t.Helper()
	if !errors.Is(err, target) {
		t.Fatalf("error = %v, want %v", err, target)
	}
}

func wantNoErr(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// closedWave returns a wave of node-ds-01 in INDUCTION_CLOSED holding TRK-1 and
// TRK-2 (stop k-a), TRK-3 (stop k-b) and TRK-4 (stop k-c).
func closedWave(t *testing.T) *wave.DeliveryWave {
	t.Helper()
	w, _, err := wave.Schedule(wave.NewWave{
		ID: "wave-1", StationNodeID: "node-ds-01", ServiceDate: "2026-10-24", InductCutoffAt: cutoff,
		WindowStart: t1, WindowEnd: t1.Add(8 * time.Hour), StopCapacity: 10,
	}, t0)
	wantNoErr(t, err)
	for _, in := range [][2]string{{"TRK-1", "k-a"}, {"TRK-2", "k-a"}, {"TRK-3", "k-b"}, {"TRK-4", "k-c"}} {
		_, err = w.Induct(in[0], in[1], t0, t0)
		wantNoErr(t, err)
	}
	wantNoErr(t, w.CloseInduction())
	return w
}

func activePartner(t *testing.T, station string, capacity int) *partner.DeliveryServicePartner {
	t.Helper()
	p, err := partner.Contract(partner.NewPartner{ID: "dsp-1", StationNodeID: station, VanCapacity: capacity})
	wantNoErr(t, err)
	return p
}

func goodStops() []Stop {
	return []Stop{{No: 1, TrackingNumbers: []string{"TRK-1", "TRK-2"}}, {No: 2, TrackingNumbers: []string{"TRK-3"}}}
}

func goodPlanning(t *testing.T) Planning {
	t.Helper()
	return Planning{Wave: closedWave(t), Partner: activePartner(t, "node-ds-01", 2)}
}

func planned(t *testing.T) *Route {
	t.Helper()
	r, _, err := Plan("route-1", goodStops(), goodPlanning(t), t0)
	wantNoErr(t, err)
	return r
}

func started(t *testing.T) *Route {
	t.Helper()
	r := planned(t)
	_, err := r.Start(t1)
	wantNoErr(t, err)
	return r
}

func assertState(t *testing.T, r *Route, state State, version int64) {
	t.Helper()
	if r.State() != state || r.Version() != version {
		t.Fatalf("state/version = %s/%d, want %s/%d", r.State(), r.Version(), state, version)
	}
}

func TestPlanRaisesRoutePlanned(t *testing.T) {
	pl := goodPlanning(t)
	r, events, err := Plan("route-1", goodStops(), pl, t0)
	wantNoErr(t, err)
	assertState(t, r, StatePlanned, 1)
	if r.ID() != "route-1" || r.WaveID() != "wave-1" || r.DSPID() != "dsp-1" || r.StationNodeID() != "node-ds-01" ||
		!reflect.DeepEqual(r.Stops(), goodStops()) || len(r.Attempts()) != 0 {
		t.Fatalf("route = %+v", r.Snapshot())
	}
	want := RoutePlanned{Header: shared.Header{Version: 1, At: t0}, RouteID: "route-1", WaveID: "wave-1", DSPID: "dsp-1", StopCount: 2}
	if len(events) != 1 || !reflect.DeepEqual(events[0], want) {
		t.Fatalf("events = %#v, want [%#v]", events, want)
	}
	ev := events[0]
	if ev.EventName() != "RoutePlanned" || ev.Entity() != "route" || ev.SubjectID() != "route-1" || ev.AggregateVersion() != 1 || !ev.OccurredAt().Equal(t0) {
		t.Fatal("event shape wrong")
	}
	if got := r.Packages(); !reflect.DeepEqual(got, []string{"TRK-1", "TRK-2", "TRK-3"}) {
		t.Fatalf("packages = %v", got)
	}
}

func TestPlanDoesNotAliasTheInput(t *testing.T) {
	stops := goodStops()
	r, _, err := Plan("route-1", stops, goodPlanning(t), t0)
	wantNoErr(t, err)
	stops[0].TrackingNumbers[0] = "TRK-X"
	r.Stops()[0].TrackingNumbers[0] = "TRK-Y"
	if r.Packages()[0] != "TRK-1" {
		t.Fatal("the route aliases a caller slice")
	}
}

func TestPlanRefusals(t *testing.T) {
	cases := []struct {
		name  string
		stops []Stop
		mod   func(*testing.T, *Planning)
		id    ID
		want  error
	}{
		{"bad route id", goodStops(), nil, "", shared.ErrValidation},
		{"no wave", goodStops(), func(_ *testing.T, p *Planning) { p.Wave = nil }, "route-1", shared.ErrValidation},
		{"no partner", goodStops(), func(_ *testing.T, p *Planning) { p.Partner = nil }, "route-1", shared.ErrValidation},
		{"no stops", nil, nil, "route-1", shared.ErrValidation},
		{"stop numbers not 1..n", []Stop{{No: 2, TrackingNumbers: []string{"TRK-1"}}}, nil, "route-1", shared.ErrValidation},
		{"stop numbers with a gap", []Stop{{No: 1, TrackingNumbers: []string{"TRK-1"}}, {No: 3, TrackingNumbers: []string{"TRK-3"}}}, nil, "route-1", shared.ErrValidation},
		{"empty stop", []Stop{{No: 1}}, nil, "route-1", shared.ErrValidation},
		{"bad tracking number", []Stop{{No: 1, TrackingNumbers: []string{""}}}, nil, "route-1", shared.ErrValidation},
		{"tracking number twice in a stop", []Stop{{No: 1, TrackingNumbers: []string{"TRK-1", "TRK-1"}}}, nil, "route-1", shared.ErrValidation},
		{"tracking number on two stops", []Stop{{No: 1, TrackingNumbers: []string{"TRK-1"}}, {No: 2, TrackingNumbers: []string{"TRK-1"}}}, nil, "route-1", shared.ErrValidation},
		{"wave still open", goodStops(), func(t *testing.T, p *Planning) { p.Wave = openWave(t) }, "route-1", shared.ErrIllegalTransition},
		{"wave dispatched", goodStops(), func(t *testing.T, p *Planning) {
			wantNoErr(t, p.Wave.Dispatch())
		}, "route-1", shared.ErrIllegalTransition},
		{"inactive partner", goodStops(), func(t *testing.T, p *Planning) { wantNoErr(t, p.Partner.Deactivate()) }, "route-1", ErrPartnerInactive},
		{"partner of another station", goodStops(), func(t *testing.T, p *Planning) { p.Partner = activePartner(t, "node-ds-02", 5) }, "route-1", ErrPartnerStationMismatch},
		{"more stops than the van holds", goodStops(), func(t *testing.T, p *Planning) { p.Partner = activePartner(t, "node-ds-01", 1) }, "route-1", ErrRouteOverCapacity},
		{"package not inducted", []Stop{{No: 1, TrackingNumbers: []string{"TRK-9"}}}, nil, "route-1", ErrPackageNotInducted},
		{"package on another route", goodStops(), func(_ *testing.T, p *Planning) { p.Routed = map[string]bool{"TRK-3": true} }, "route-1", ErrPackageAlreadyRouted},
		{"two stop keys in one stop", []Stop{{No: 1, TrackingNumbers: []string{"TRK-1", "TRK-3"}}}, nil, "route-1", ErrStopKeyMismatch},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			pl := goodPlanning(t)
			if c.mod != nil {
				c.mod(t, &pl)
			}
			r, ev, err := Plan(c.id, c.stops, pl, t0)
			wantIs(t, err, c.want)
			if r != nil || ev != nil {
				t.Fatal("a refused plan returned a route")
			}
		})
	}
}

func openWave(t *testing.T) *wave.DeliveryWave {
	t.Helper()
	w, _, err := wave.Schedule(wave.NewWave{
		ID: "wave-1", StationNodeID: "node-ds-01", ServiceDate: "2026-10-24", InductCutoffAt: cutoff,
		WindowStart: t1, WindowEnd: t2, StopCapacity: 1,
	}, t0)
	wantNoErr(t, err)
	return w
}

func TestPlanCapacityBoundary(t *testing.T) {
	pl := goodPlanning(t)
	pl.Partner = activePartner(t, "node-ds-01", 2)
	if _, _, err := Plan("route-1", goodStops(), pl, t0); err != nil {
		t.Fatalf("2 stops on a van of 2 must be accepted: %v", err)
	}
	// Several packages sharing a stopKey take one stop of the van.
	one := []Stop{{No: 1, TrackingNumbers: []string{"TRK-1", "TRK-2"}}}
	pl.Partner = activePartner(t, "node-ds-01", 1)
	if _, _, err := Plan("route-1", one, pl, t0); err != nil {
		t.Fatalf("1 stop of 2 packages on a van of 1 must be accepted: %v", err)
	}
}

func TestStart(t *testing.T) {
	r := planned(t)
	events, err := r.Start(t1)
	wantNoErr(t, err)
	assertState(t, r, StateOutForDelivery, 2)
	if len(events) != 3 {
		t.Fatalf("events = %d, want 3", len(events))
	}
	for i, tn := range []string{"TRK-1", "TRK-2", "TRK-3"} {
		want := PackageOutForDelivery{
			Header: shared.Header{Version: 2, At: t1}, RouteID: "route-1", TrackingNumber: tn, StationNodeID: "node-ds-01", At: t1,
		}
		if !reflect.DeepEqual(events[i], want) {
			t.Fatalf("event %d = %#v, want %#v", i, events[i], want)
		}
		if events[i].EventName() != "PackageOutForDelivery" || events[i].Entity() != "route" || events[i].SubjectID() != "route-1" {
			t.Fatal("event shape wrong")
		}
	}
}

func TestRouteTransitionsLegalAndIllegal(t *testing.T) {
	build := map[State]func(*testing.T) *Route{
		StatePlanned: planned, StateOutForDelivery: started, StateCompleted: completedRoute,
	}
	commands := []struct {
		name  string
		from  State
		to    State
		apply func(*Route) error
	}{
		{"StartRoute", StatePlanned, StateOutForDelivery, func(r *Route) error { _, err := r.Start(t1); return err }},
		{"CompleteRoute", StateOutForDelivery, StateCompleted, func(r *Route) error { attemptAll(t, r); return r.Complete() }},
	}
	for _, c := range commands {
		for state, mk := range build {
			t.Run(c.name+" from "+string(state), func(t *testing.T) {
				r := mk(t)
				before := r.Version()
				var err error
				if c.name == "CompleteRoute" && state == StateOutForDelivery {
					attemptAll(t, r)
					before = r.Version()
					err = r.Complete()
				} else {
					err = c.apply(r)
				}
				if state == c.from {
					wantNoErr(t, err)
					assertState(t, r, c.to, before+1)
					return
				}
				wantIs(t, err, shared.ErrIllegalTransition)
				assertState(t, r, state, before)
			})
		}
	}
}

func attemptAll(t *testing.T, r *Route) {
	t.Helper()
	if r.State() != StateOutForDelivery {
		return
	}
	for _, tn := range r.Packages() {
		if _, ok := r.AttemptOf(tn); !ok {
			_, err := r.RecordAttempt(tn, OutcomeDelivered, "", t2, t2)
			wantNoErr(t, err)
		}
	}
}

func completedRoute(t *testing.T) *Route {
	t.Helper()
	r := started(t)
	attemptAll(t, r)
	wantNoErr(t, r.Complete())
	return r
}

func TestCompleteNeedsEveryPackageAttempted(t *testing.T) {
	r := started(t)
	_, err := r.RecordAttempt("TRK-1", OutcomeDelivered, "", t2, t2)
	wantNoErr(t, err)
	if r.AllAttempted() {
		t.Fatal("one of three attempted is not all")
	}
	before := r.Version()
	wantIs(t, r.Complete(), ErrRouteIncomplete)
	assertState(t, r, StateOutForDelivery, before)
}

func TestRecordAttempt(t *testing.T) {
	r := started(t)
	events, err := r.RecordAttempt("TRK-1", OutcomeFailed, "recipient absent", t2, t2.Add(time.Minute))
	wantNoErr(t, err)
	assertState(t, r, StateOutForDelivery, 3)
	want := DeliveryAttempted{
		Header: shared.Header{Version: 3, At: t2.Add(time.Minute)}, RouteID: "route-1", TrackingNumber: "TRK-1",
		Outcome: OutcomeFailed, FailureReason: "recipient absent", At: t2,
	}
	if len(events) != 1 || !reflect.DeepEqual(events[0], want) {
		t.Fatalf("events = %#v, want [%#v]", events, want)
	}
	if events[0].EventName() != "DeliveryAttempted" || events[0].Entity() != "route" || events[0].SubjectID() != "route-1" {
		t.Fatal("event shape wrong")
	}
	assertAttempt(t, r, "TRK-1", OutcomeFailed, "recipient absent")
	if _, ok := r.AttemptOf("TRK-2"); ok {
		t.Fatal("TRK-2 has no attempt yet")
	}
}

func assertAttempt(t *testing.T, r *Route, tn string, outcome Outcome, reason string) {
	t.Helper()
	a, ok := r.AttemptOf(tn)
	if !ok || a.Outcome != outcome || a.FailureReason != reason || !a.At.Equal(t2) {
		t.Fatalf("attempt of %s = %+v %v", tn, a, ok)
	}
}

func TestRecordDeliveredAttemptAndLookups(t *testing.T) {
	r := started(t)
	events, err := r.RecordAttempt("TRK-2", OutcomeDelivered, "", t2, t2)
	wantNoErr(t, err)
	if got := events[0].(DeliveryAttempted); got.FailureReason != "" || got.Outcome != OutcomeDelivered {
		t.Fatalf("delivered event = %+v", got)
	}
	if !r.HasPackage("TRK-3") || r.HasPackage("TRK-9") {
		t.Fatal("HasPackage wrong")
	}
	if len(r.Attempts()) != 1 {
		t.Fatalf("attempts = %d", len(r.Attempts()))
	}
}

func TestRecordAttemptRefusals(t *testing.T) {
	long := strings.Repeat("x", MaxFailureReason+1)
	cases := []struct {
		name    string
		build   func(*testing.T) *Route
		tn      string
		outcome Outcome
		reason  string
		at      time.Time
		want    error
	}{
		{"planned route", planned, "TRK-1", OutcomeDelivered, "", t2, ErrRouteNotStarted},
		{"completed route", completedRoute, "TRK-1", OutcomeDelivered, "", t2, ErrRouteNotStarted},
		{"package not on the route", started, "TRK-4", OutcomeDelivered, "", t2, ErrPackageNotOnRoute},
		{"bad tracking number", started, "", OutcomeDelivered, "", t2, shared.ErrValidation},
		{"unknown outcome", started, "TRK-1", "LOST", "", t2, shared.ErrValidation},
		{"zero instant", started, "TRK-1", OutcomeDelivered, "", time.Time{}, shared.ErrValidation},
		{"delivered with a reason", started, "TRK-1", OutcomeDelivered, "why", t2, shared.ErrValidation},
		{"failed without a reason", started, "TRK-1", OutcomeFailed, "", t2, shared.ErrValidation},
		{"failed with a blank reason", started, "TRK-1", OutcomeFailed, " ", t2, shared.ErrValidation},
		{"failed with a long reason", started, "TRK-1", OutcomeFailed, long, t2, shared.ErrValidation},
		{"second attempt after delivered", func(t *testing.T) *Route { return withAttempt(t, OutcomeDelivered, "") }, "TRK-1", OutcomeDelivered, "", t2, ErrAlreadyDelivered},
		{"failed after delivered", func(t *testing.T) *Route { return withAttempt(t, OutcomeDelivered, "") }, "TRK-1", OutcomeFailed, "x", t2, ErrAlreadyDelivered},
		{"second attempt after failed", func(t *testing.T) *Route { return withAttempt(t, OutcomeFailed, "absent") }, "TRK-1", OutcomeDelivered, "", t2, ErrAttemptAlreadyRecorded},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := c.build(t)
			before := r.Snapshot()
			events, err := r.RecordAttempt(c.tn, c.outcome, c.reason, c.at, t2)
			wantIs(t, err, c.want)
			if events != nil || !reflect.DeepEqual(r.Snapshot(), before) {
				t.Fatal("a refused attempt changed the route")
			}
		})
	}
	t.Run("a reason at the limit is accepted", func(t *testing.T) {
		r := started(t)
		_, err := r.RecordAttempt("TRK-1", OutcomeFailed, strings.Repeat("x", MaxFailureReason), t2, t2)
		wantNoErr(t, err)
	})
	t.Run("a refusal of the first attempt is not already delivered", func(t *testing.T) {
		r := withAttempt(t, OutcomeFailed, "absent")
		_, err := r.RecordAttempt("TRK-1", OutcomeDelivered, "", t2, t2)
		if errors.Is(err, ErrAlreadyDelivered) {
			t.Fatal("a failed package was never delivered")
		}
	})
}

func withAttempt(t *testing.T, o Outcome, reason string) *Route {
	t.Helper()
	r := started(t)
	_, err := r.RecordAttempt("TRK-1", o, reason, t2, t2)
	wantNoErr(t, err)
	return r
}

func TestStopOutcomeIsDerivedPerPackage(t *testing.T) {
	cases := []struct {
		name     string
		attempts []Attempt
		stop1    Outcome
		set1     bool
		stop2    Outcome
		set2     bool
	}{
		{"no attempts", nil, "", false, "", false},
		{"half of stop 1", []Attempt{{"TRK-1", OutcomeDelivered, "", t2}}, "", false, "", false},
		{"stop 1 delivered", []Attempt{{"TRK-1", OutcomeDelivered, "", t2}, {"TRK-2", OutcomeDelivered, "", t2}}, OutcomeDelivered, true, "", false},
		{"stop 1 mixed is failed", []Attempt{{"TRK-1", OutcomeDelivered, "", t2}, {"TRK-2", OutcomeFailed, "absent", t2}}, OutcomeFailed, true, "", false},
		{"stop 1 failed first", []Attempt{{"TRK-1", OutcomeFailed, "absent", t2}, {"TRK-2", OutcomeDelivered, "", t2}}, OutcomeFailed, true, "", false},
		{"stop 2 failed", []Attempt{{"TRK-3", OutcomeFailed, "absent", t2}}, "", false, OutcomeFailed, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := started(t)
			for _, a := range c.attempts {
				_, err := r.RecordAttempt(a.TrackingNumber, a.Outcome, a.FailureReason, a.At, a.At)
				wantNoErr(t, err)
			}
			assertOutcome(t, r, 1, c.stop1, c.set1)
			assertOutcome(t, r, 2, c.stop2, c.set2)
		})
	}
	r := started(t)
	for _, no := range []int{0, 3, -1} {
		if _, ok := r.StopOutcome(no); ok {
			t.Errorf("StopOutcome(%d) must not exist", no)
		}
	}
}

func assertOutcome(t *testing.T, r *Route, no int, want Outcome, wantSet bool) {
	t.Helper()
	got, ok := r.StopOutcome(no)
	if got != want || ok != wantSet {
		t.Fatalf("StopOutcome(%d) = %q %v, want %q %v", no, got, ok, want, wantSet)
	}
}

func TestEnumsValid(t *testing.T) {
	for _, s := range []State{StatePlanned, StateOutForDelivery, StateCompleted} {
		if !s.Valid() {
			t.Errorf("%s must be valid", s)
		}
	}
	if State("").Valid() || State("DONE").Valid() || Outcome("").Valid() || Outcome("LOST").Valid() {
		t.Fatal("unknown enum values must be invalid")
	}
	if !OutcomeDelivered.Valid() || !OutcomeFailed.Valid() {
		t.Fatal("DELIVERED and FAILED are valid")
	}
}
