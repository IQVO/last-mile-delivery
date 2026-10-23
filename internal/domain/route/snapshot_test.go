package route

import (
	"reflect"
	"testing"
	"time"

	"github.com/claudioed/last-mile-delivery/internal/domain/shared"
)

func snapshotWith(state State, attempts []Attempt) Snapshot {
	return Snapshot{
		ID: "route-1", WaveID: "wave-1", DSPID: "dsp-1", StationNodeID: "node-ds-01", State: state, Version: 5,
		Stops:    []Stop{{No: 1, TrackingNumbers: []string{"TRK-1", "TRK-2"}}, {No: 2, TrackingNumbers: []string{"TRK-3"}}},
		Attempts: attempts,
	}
}

func allAttempts() []Attempt {
	return []Attempt{
		{"TRK-1", OutcomeDelivered, "", t2}, {"TRK-2", OutcomeFailed, "absent", t2}, {"TRK-3", OutcomeDelivered, "", t2},
	}
}

func TestSnapshotRestoreRoundTrip(t *testing.T) {
	r := started(t)
	attemptAll(t, r)
	wantNoErr(t, r.Complete())
	got, err := Restore(r.Snapshot())
	wantNoErr(t, err)
	if !reflect.DeepEqual(got.Snapshot(), r.Snapshot()) {
		t.Fatalf("round trip changed the route:\n got %+v\nwant %+v", got.Snapshot(), r.Snapshot())
	}
	snap := got.Snapshot()
	snap.Stops[0].TrackingNumbers[0] = "TRK-X"
	snap.Attempts[0].TrackingNumber = "TRK-X"
	if got.Packages()[0] != "TRK-1" || got.Attempts()[0].TrackingNumber == "TRK-X" {
		t.Fatal("Snapshot aliases the route")
	}
}

func TestRestoreAccepts(t *testing.T) {
	cases := []struct {
		name string
		snap Snapshot
	}{
		{"planned", snapshotWith(StatePlanned, nil)},
		{"out for delivery with some attempts", snapshotWith(StateOutForDelivery, allAttempts()[:2])},
		{"out for delivery with every attempt (before the completion save)", snapshotWith(StateOutForDelivery, allAttempts())},
		{"completed", snapshotWith(StateCompleted, allAttempts())},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := Restore(c.snap); err != nil {
				t.Fatalf("Restore: %v", err)
			}
		})
	}
	s := snapshotWith(StatePlanned, nil)
	s.Version = 1
	if _, err := Restore(s); err != nil {
		t.Fatalf("version 1: %v", err)
	}
}

func TestRestoreRejectsCorruptRows(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*Snapshot)
	}{
		{"bad route id", func(s *Snapshot) { s.ID = "" }},
		{"bad wave id", func(s *Snapshot) { s.WaveID = "" }},
		{"bad dsp id", func(s *Snapshot) { s.DSPID = "" }},
		{"bad station", func(s *Snapshot) { s.StationNodeID = "" }},
		{"version zero", func(s *Snapshot) { s.Version = 0 }},
		{"unknown state", func(s *Snapshot) { s.State = "DONE" }},
		{"no stops", func(s *Snapshot) { s.Stops = nil }},
		{"stop numbers not 1..n", func(s *Snapshot) { s.Stops[1].No = 3 }},
		{"empty stop", func(s *Snapshot) { s.Stops[1].TrackingNumbers = nil }},
		{"duplicate package", func(s *Snapshot) { s.Stops[1].TrackingNumbers = []string{"TRK-1"} }},
		{"attempts on a planned route", func(s *Snapshot) { s.State, s.Attempts = StatePlanned, allAttempts()[:1] }},
		{"attempt of a foreign package", func(s *Snapshot) { s.Attempts = []Attempt{{"TRK-9", OutcomeDelivered, "", t2}} }},
		{"two attempts of one package", func(s *Snapshot) {
			s.Attempts = []Attempt{{"TRK-1", OutcomeFailed, "a", t2}, {"TRK-1", OutcomeDelivered, "", t2}}
		}},
		{"attempt with a bad outcome", func(s *Snapshot) { s.Attempts = []Attempt{{"TRK-1", "LOST", "", t2}} }},
		{"failed attempt without a reason", func(s *Snapshot) { s.Attempts = []Attempt{{"TRK-1", OutcomeFailed, "", t2}} }},
		{"delivered attempt with a reason", func(s *Snapshot) { s.Attempts = []Attempt{{"TRK-1", OutcomeDelivered, "x", t2}} }},
		{"attempt without an instant", func(s *Snapshot) { s.Attempts = []Attempt{{"TRK-1", OutcomeDelivered, "", time.Time{}}} }},
		{"completed with a missing attempt", func(s *Snapshot) { s.State, s.Attempts = StateCompleted, allAttempts()[:2] }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := snapshotWith(StateOutForDelivery, allAttempts()[:2])
			c.mutate(&s)
			r, err := Restore(s)
			wantIs(t, err, shared.ErrInvalidSnapshot)
			if r != nil {
				t.Fatal("a corrupt row became a live route")
			}
		})
	}
}
