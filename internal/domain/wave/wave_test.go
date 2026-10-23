package wave

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/claudioed/last-mile-delivery/internal/domain/shared"
)

var (
	t0     = time.Date(2026, 10, 24, 6, 0, 0, 0, time.UTC)
	cutoff = time.Date(2026, 10, 24, 8, 0, 0, 0, time.UTC)
	wStart = time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	wEnd   = time.Date(2026, 10, 24, 20, 0, 0, 0, time.UTC)
)

func validNew() NewWave {
	return NewWave{
		ID: "wave-1", StationNodeID: "node-ds-01", ServiceDate: "2026-10-24",
		InductCutoffAt: cutoff, WindowStart: wStart, WindowEnd: wEnd, StopCapacity: 2,
	}
}

func mustSchedule(t *testing.T, mutate func(*NewWave)) *DeliveryWave {
	t.Helper()
	in := validNew()
	if mutate != nil {
		mutate(&in)
	}
	w, _, err := Schedule(in, t0)
	if err != nil {
		t.Fatalf("Schedule: %v", err)
	}
	return w
}

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

func assertState(t *testing.T, w *DeliveryWave, state State, version int64) {
	t.Helper()
	if w.State() != state || w.Version() != version {
		t.Fatalf("state/version = %s/%d, want %s/%d", w.State(), w.Version(), state, version)
	}
}

func TestScheduleRaisesDeliveryWaveScheduled(t *testing.T) {
	w, events, err := Schedule(validNew(), t0)
	wantNoErr(t, err)
	assertState(t, w, StateOpen, 1)
	if w.ID() != "wave-1" || w.StationNodeID() != "node-ds-01" || w.ServiceDate() != "2026-10-24" ||
		!w.InductCutoffAt().Equal(cutoff) || !w.WindowStart().Equal(wStart) || !w.WindowEnd().Equal(wEnd) ||
		w.StopCapacity() != 2 || w.AssignedStops() != 0 || w.RemainingStops() != 2 || w.CancelReason() != "" {
		t.Fatalf("accessors wrong: %+v", w.Snapshot())
	}
	want := DeliveryWaveScheduled{
		Header: shared.Header{Version: 1, At: t0}, WaveID: "wave-1", StationNodeID: "node-ds-01", ServiceDate: "2026-10-24",
		InductCutoffAt: cutoff, WindowStart: wStart, WindowEnd: wEnd, StopCapacity: 2,
	}
	if len(events) != 1 || !reflect.DeepEqual(events[0], want) {
		t.Fatalf("events = %#v, want [%#v]", events, want)
	}
	assertEventShape(t, events[0], "DeliveryWaveScheduled", "wave-1", 1)
}

func assertEventShape(t *testing.T, ev shared.Event, name, subject string, version int64) {
	t.Helper()
	if ev.EventName() != name || ev.Entity() != "wave" || ev.SubjectID() != subject || ev.AggregateVersion() != version {
		t.Fatalf("event shape = %s/%s/%s/%d", ev.EventName(), ev.Entity(), ev.SubjectID(), ev.AggregateVersion())
	}
}

func TestScheduleValidation(t *testing.T) {
	zone := time.FixedZone("BRT", -3*3600)
	cases := []struct {
		name   string
		mutate func(*NewWave)
		want   error
	}{
		{"bad wave id", func(n *NewWave) { n.ID = "-x" }, shared.ErrValidation},
		{"bad station id", func(n *NewWave) { n.StationNodeID = "" }, shared.ErrValidation},
		{"bad service date", func(n *NewWave) { n.ServiceDate = "2026-02-30" }, shared.ErrValidation},
		{"negative capacity", func(n *NewWave) { n.StopCapacity = -1 }, shared.ErrValidation},
		{"capacity above the postgres ceiling", func(n *NewWave) { n.StopCapacity = MaxStopCapacity + 1 }, shared.ErrValidation},
		{"zero cutoff", func(n *NewWave) { n.InductCutoffAt = time.Time{} }, shared.ErrValidation},
		{"zero window start", func(n *NewWave) { n.WindowStart = time.Time{} }, shared.ErrValidation},
		{"zero window end", func(n *NewWave) { n.WindowEnd = time.Time{} }, shared.ErrValidation},
		{"cutoff equal to window start", func(n *NewWave) { n.InductCutoffAt = wStart }, ErrInvalidWaveTimes},
		{"cutoff after window start", func(n *NewWave) { n.InductCutoffAt = wStart.Add(time.Minute) }, ErrInvalidWaveTimes},
		{"window start equal to window end", func(n *NewWave) { n.WindowEnd = wStart }, ErrInvalidWaveTimes},
		{"window end before window start", func(n *NewWave) { n.WindowEnd = wStart.Add(-time.Minute) }, ErrInvalidWaveTimes},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			in := validNew()
			c.mutate(&in)
			w, ev, err := Schedule(in, t0)
			wantIs(t, err, c.want)
			if w != nil || ev != nil {
				t.Fatalf("a refused schedule returned %v %v", w, ev)
			}
		})
	}
	t.Run("boundaries are accepted and instants become UTC", func(t *testing.T) {
		in := validNew()
		in.StopCapacity = 0
		in.InductCutoffAt = cutoff.In(zone)
		w, _, err := Schedule(in, t0.In(zone))
		wantNoErr(t, err)
		if w.InductCutoffAt().Location() != time.UTC {
			t.Fatal("instants must be stored in UTC")
		}
		in.StopCapacity = MaxStopCapacity
		_, _, err = Schedule(in, t0)
		wantNoErr(t, err)
		in.InductCutoffAt = wStart.Add(-time.Nanosecond)
		_, _, err = Schedule(in, t0)
		wantNoErr(t, err)
	})
}

func TestInduct(t *testing.T) {
	w := mustSchedule(t, nil)
	events, err := w.Induct("TRK-1", "k-a", t0, t0.Add(time.Minute))
	wantNoErr(t, err)
	assertState(t, w, StateOpen, 2)
	want := DeliveryWaveCapacityChanged{
		Header: shared.Header{Version: 2, At: t0.Add(time.Minute)}, WaveID: "wave-1", StopCapacity: 2, AssignedStops: 1, RemainingStops: 1,
	}
	if len(events) != 1 || !reflect.DeepEqual(events[0], want) {
		t.Fatalf("events = %#v, want [%#v]", events, want)
	}
	assertEventShape(t, events[0], "DeliveryWaveCapacityChanged", "wave-1", 2)

	// A package of a stop already in the wave takes no capacity and raises nothing.
	events, err = w.Induct("TRK-2", "k-a", t0, t0)
	wantNoErr(t, err)
	if events != nil || w.AssignedStops() != 1 || w.Version() != 3 {
		t.Fatalf("same stop: events=%v assigned=%d version=%d", events, w.AssignedStops(), w.Version())
	}
	// The wave fills at exactly its capacity.
	events, err = w.Induct("TRK-3", "k-b", t0, t0)
	wantNoErr(t, err)
	if len(events) != 1 || w.AssignedStops() != 2 || w.RemainingStops() != 0 {
		t.Fatalf("filling stop: events=%v assigned=%d", events, w.AssignedStops())
	}
	// A package of an existing stop still fits in a full wave.
	_, err = w.Induct("TRK-4", "k-b", t0, t0)
	wantNoErr(t, err)
	if len(w.Inductions()) != 4 {
		t.Fatalf("inductions = %d, want 4", len(w.Inductions()))
	}
	in, ok := w.Induction("TRK-3")
	if !ok || in.StopKey != "k-b" || !in.At.Equal(t0) {
		t.Fatalf("Induction(TRK-3) = %+v %v", in, ok)
	}
	if _, ok := w.Induction("TRK-9"); ok {
		t.Fatal("an unknown package must not be found")
	}
}

func TestInductRefusals(t *testing.T) {
	full := func(t *testing.T) *DeliveryWave {
		w := mustSchedule(t, func(n *NewWave) { n.StopCapacity = 1 })
		_, err := w.Induct("TRK-1", "k-a", t0, t0)
		wantNoErr(t, err)
		return w
	}
	cases := []struct {
		name  string
		build func(*testing.T) *DeliveryWave
		tn    string
		key   string
		at    time.Time
		want  error
	}{
		{"new stop on a full wave", full, "TRK-2", "k-b", t0, ErrWaveFull},
		{"duplicate tracking number", full, "TRK-1", "k-a", t0, ErrAlreadyInducted},
		{"at the cutoff", full, "TRK-2", "k-a", cutoff, ErrInductionClosed},
		{"after the cutoff", full, "TRK-2", "k-a", cutoff.Add(time.Second), ErrInductionClosed},
		{"bad tracking number", full, "", "k-a", t0, shared.ErrValidation},
		{"bad stop key", full, "TRK-2", "", t0, shared.ErrValidation},
		{"zero instant", full, "TRK-2", "k-a", time.Time{}, shared.ErrValidation},
		{"induction closed", func(t *testing.T) *DeliveryWave { w := full(t); wantNoErr(t, w.CloseInduction()); return w }, "TRK-2", "k-a", t0, ErrInductionClosed},
		{"dispatched", dispatched, "TRK-2", "k-a", t0, ErrInductionClosed},
		{"completed", completed, "TRK-2", "k-a", t0, ErrInductionClosed},
		{"cancelled", cancelled, "TRK-2", "k-a", t0, ErrInductionClosed},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w := c.build(t)
			before := w.Snapshot()
			events, err := w.Induct(c.tn, c.key, c.at, t0)
			wantIs(t, err, c.want)
			if events != nil || !reflect.DeepEqual(w.Snapshot(), before) {
				t.Fatal("a refused induction changed the wave")
			}
		})
	}
	t.Run("one instant before the cutoff is accepted", func(t *testing.T) {
		w := mustSchedule(t, nil)
		_, err := w.Induct("TRK-1", "k-a", cutoff.Add(-time.Nanosecond), t0)
		wantNoErr(t, err)
	})
}

func dispatched(t *testing.T) *DeliveryWave {
	t.Helper()
	w := mustSchedule(t, nil)
	wantNoErr(t, w.CloseInduction())
	wantNoErr(t, w.Dispatch())
	return w
}

func completed(t *testing.T) *DeliveryWave {
	t.Helper()
	w := dispatched(t)
	wantNoErr(t, w.Complete())
	return w
}

func cancelled(t *testing.T) *DeliveryWave {
	t.Helper()
	w := mustSchedule(t, nil)
	_, err := w.Cancel("why", t0)
	wantNoErr(t, err)
	return w
}

func inState(t *testing.T, s State) *DeliveryWave {
	t.Helper()
	switch s {
	case StateOpen:
		return mustSchedule(t, nil)
	case StateInductionClosed:
		w := mustSchedule(t, nil)
		wantNoErr(t, w.CloseInduction())
		return w
	case StateDispatched:
		return dispatched(t)
	case StateCompleted:
		return completed(t)
	default:
		return cancelled(t)
	}
}

func TestTransitionsLegalAndIllegal(t *testing.T) {
	all := []State{StateOpen, StateInductionClosed, StateDispatched, StateCompleted, StateCancelled}
	commands := []struct {
		name  string
		from  State
		to    State
		apply func(*DeliveryWave) error
	}{
		{"CloseInduction", StateOpen, StateInductionClosed, (*DeliveryWave).CloseInduction},
		{"DispatchWave", StateInductionClosed, StateDispatched, (*DeliveryWave).Dispatch},
		{"CompleteWave", StateDispatched, StateCompleted, (*DeliveryWave).Complete},
		{"CancelWave", StateOpen, StateCancelled, func(w *DeliveryWave) error { _, err := w.Cancel("closed", t0); return err }},
	}
	for _, c := range commands {
		for _, s := range all {
			t.Run(c.name+" from "+string(s), func(t *testing.T) {
				w := inState(t, s)
				before := w.Version()
				err := c.apply(w)
				if s == c.from {
					wantNoErr(t, err)
					assertState(t, w, c.to, before+1)
					return
				}
				wantIs(t, err, shared.ErrIllegalTransition)
				assertState(t, w, s, before)
			})
		}
	}
}

func TestCancel(t *testing.T) {
	w := mustSchedule(t, nil)
	_, err := w.Induct("TRK-1", "k-a", t0, t0)
	wantNoErr(t, err)
	events, err := w.Cancel("station closed", t0.Add(time.Hour))
	wantNoErr(t, err)
	want := DeliveryWaveCancelled{Header: shared.Header{Version: 3, At: t0.Add(time.Hour)}, WaveID: "wave-1", Reason: "station closed"}
	if len(events) != 1 || !reflect.DeepEqual(events[0], want) || w.CancelReason() != "station closed" {
		t.Fatalf("events=%#v reason=%q", events, w.CancelReason())
	}
	assertEventShape(t, events[0], "DeliveryWaveCancelled", "wave-1", 3)
	if len(w.Inductions()) != 1 {
		t.Fatal("a cancelled wave keeps its inductions as a record")
	}
	for _, reason := range []string{"", " x", "x ", "a\x00b", strings.Repeat("x", MaxReason+1)} {
		w2 := mustSchedule(t, nil)
		_, err := w2.Cancel(reason, t0)
		wantIs(t, err, shared.ErrValidation)
		assertState(t, w2, StateOpen, 1)
	}
	w3 := mustSchedule(t, nil)
	_, err = w3.Cancel(strings.Repeat("x", MaxReason), t0)
	wantNoErr(t, err)
}

func TestStateValid(t *testing.T) {
	for _, s := range []State{StateOpen, StateInductionClosed, StateDispatched, StateCompleted, StateCancelled} {
		if !s.Valid() {
			t.Errorf("%s must be valid", s)
		}
	}
	for _, s := range []State{"", "open", "DONE"} {
		if s.Valid() {
			t.Errorf("%q must be invalid", s)
		}
	}
}
