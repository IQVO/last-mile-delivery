package wave

import (
	"reflect"
	"testing"
	"time"

	"github.com/claudioed/last-mile-delivery/internal/domain/shared"
)

func validSnapshot() Snapshot {
	return Snapshot{
		ID: "wave-1", StationNodeID: "node-ds-01", ServiceDate: "2026-10-24", InductCutoffAt: cutoff, WindowStart: wStart,
		WindowEnd: wEnd, StopCapacity: 2, AssignedStops: 2, State: StateOpen, Version: 4,
		Inductions: []Induction{
			{TrackingNumber: "TRK-1", StopKey: "k-a", At: t0},
			{TrackingNumber: "TRK-2", StopKey: "k-a", At: t0},
			{TrackingNumber: "TRK-3", StopKey: "k-b", At: t0},
		},
	}
}

func TestSnapshotRestoreRoundTrip(t *testing.T) {
	w := mustSchedule(t, nil)
	for i, tn := range []string{"TRK-1", "TRK-2", "TRK-3"} {
		key := []string{"k-a", "k-a", "k-b"}[i]
		_, err := w.Induct(tn, key, t0, t0)
		wantNoErr(t, err)
	}
	_, err := w.Cancel("closed", t0)
	wantNoErr(t, err)
	got, err := Restore(w.Snapshot())
	wantNoErr(t, err)
	if !reflect.DeepEqual(got.Snapshot(), w.Snapshot()) {
		t.Fatalf("round trip changed the wave:\n got %+v\nwant %+v", got.Snapshot(), w.Snapshot())
	}
	// The snapshot and the accessors never alias the wave.
	snap := got.Snapshot()
	snap.Inductions[0].TrackingNumber = "TRK-X"
	if got.Inductions()[0].TrackingNumber != "TRK-1" {
		t.Fatal("Snapshot aliases the stored inductions")
	}
	got.Inductions()[0].TrackingNumber = "TRK-Y"
	if got.Inductions()[0].TrackingNumber != "TRK-1" {
		t.Fatal("Inductions aliases the stored inductions")
	}
}

func TestRestoreAcceptsEveryState(t *testing.T) {
	for _, s := range []State{StateOpen, StateInductionClosed, StateDispatched, StateCompleted} {
		snap := validSnapshot()
		snap.State = s
		if _, err := Restore(snap); err != nil {
			t.Errorf("Restore(%s): %v", s, err)
		}
	}
	snap := validSnapshot()
	snap.State, snap.CancelReason = StateCancelled, "closed"
	if _, err := Restore(snap); err != nil {
		t.Errorf("Restore(CANCELLED): %v", err)
	}
}

func TestRestoreBoundaries(t *testing.T) {
	accept := []struct {
		name   string
		mutate func(*Snapshot)
	}{
		{"version 1", func(s *Snapshot) { s.Version = 1 }},
		{"no inductions at zero stops", func(s *Snapshot) { s.Inductions, s.AssignedStops, s.StopCapacity = nil, 0, 0 }},
		{"assigned equal to capacity", func(s *Snapshot) {}},
		{"capacity at the postgres ceiling", func(s *Snapshot) { s.StopCapacity = MaxStopCapacity }},
		{"cutoff one nanosecond before the window", func(s *Snapshot) { s.InductCutoffAt = wStart.Add(-time.Nanosecond) }},
	}
	for _, c := range accept {
		t.Run("accepts "+c.name, func(t *testing.T) {
			s := validSnapshot()
			c.mutate(&s)
			if _, err := Restore(s); err != nil {
				t.Fatalf("Restore: %v", err)
			}
		})
	}
}

func TestRestoreRejectsCorruptRows(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*Snapshot)
	}{
		{"bad id", func(s *Snapshot) { s.ID = "" }},
		{"bad station", func(s *Snapshot) { s.StationNodeID = "a b" }},
		{"bad date", func(s *Snapshot) { s.ServiceDate = "x" }},
		{"negative capacity", func(s *Snapshot) { s.StopCapacity = -1 }},
		{"capacity above the ceiling", func(s *Snapshot) { s.StopCapacity = MaxStopCapacity + 1 }},
		{"zero cutoff", func(s *Snapshot) { s.InductCutoffAt = time.Time{} }},
		{"cutoff after window start", func(s *Snapshot) { s.InductCutoffAt = wStart }},
		{"window end before start", func(s *Snapshot) { s.WindowEnd = wStart }},
		{"version zero", func(s *Snapshot) { s.Version = 0 }},
		{"unknown state", func(s *Snapshot) { s.State = "DONE" }},
		{"reason on an open wave", func(s *Snapshot) { s.CancelReason = "x" }},
		{"cancelled without a reason", func(s *Snapshot) { s.State = StateCancelled }},
		{"assigned below the stops", func(s *Snapshot) { s.AssignedStops = 1 }},
		{"assigned above the stops", func(s *Snapshot) { s.AssignedStops, s.StopCapacity = 3, 3 }},
		{"assigned above the capacity", func(s *Snapshot) { s.StopCapacity = 1 }},
		{"duplicate package", func(s *Snapshot) { s.Inductions[1].TrackingNumber = "TRK-1" }},
		{"bad tracking number", func(s *Snapshot) { s.Inductions[0].TrackingNumber = "" }},
		{"bad stop key", func(s *Snapshot) { s.Inductions[0].StopKey = "" }},
		{"zero induction instant", func(s *Snapshot) { s.Inductions[0].At = time.Time{} }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := validSnapshot()
			c.mutate(&s)
			w, err := Restore(s)
			wantIs(t, err, shared.ErrInvalidSnapshot)
			if w != nil {
				t.Fatal("a corrupt row became a live wave")
			}
		})
	}
}
