package history

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/claudioed/last-mile-delivery/internal/domain/shared"
)

var (
	t0 = time.Date(2026, 10, 24, 6, 0, 0, 0, time.UTC)
	t1 = t0.Add(time.Hour)
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

func arrived(t *testing.T, placed bool) *History {
	t.Helper()
	h, _, err := Arrive("TRK-1", "node-ds-01", "asg-1", placed, t0, t0)
	wantNoErr(t, err)
	return h
}

func assertAt(t *testing.T, h *History, status Status, failed int, version int64) {
	t.Helper()
	if h.Status() != status || h.FailedAttempts() != failed || h.Version() != version {
		t.Fatalf("status/failed/version = %s/%d/%d, want %s/%d/%d", h.Status(), h.FailedAttempts(), h.Version(), status, failed, version)
	}
}

func TestArrive(t *testing.T) {
	h, events, err := Arrive("TRK-1", "node-ds-01", "asg-1", true, t0, t1)
	wantNoErr(t, err)
	assertAt(t, h, StatusInducted, 0, 1)
	if events != nil || h.TrackingNumber() != "TRK-1" || h.StationNodeID() != "node-ds-01" || h.AssignmentID() != "asg-1" || !h.ArrivedAt().Equal(t0) {
		t.Fatalf("placed arrival: events=%v %+v", events, h.Snapshot())
	}
	h, events, err = Arrive("TRK-1", "node-ds-01", "asg-1", false, t0, t1)
	wantNoErr(t, err)
	assertAt(t, h, StatusAtStation, 0, 1)
	want := PackageInductionDeferred{Header: shared.Header{Version: 1, At: t1}, TrackingNumber: "TRK-1", AssignmentID: "asg-1"}
	if len(events) != 1 || !reflect.DeepEqual(events[0], want) {
		t.Fatalf("events = %#v, want [%#v]", events, want)
	}
	if events[0].EventName() != "PackageInductionDeferred" || events[0].Entity() != "packageattempthistory" ||
		events[0].SubjectID() != "TRK-1" || events[0].AggregateVersion() != 1 || !events[0].OccurredAt().Equal(t1) {
		t.Fatal("deferred event shape wrong")
	}
	if _, _, err := Arrive("TRK-1", "node-ds-01", "", true, t0, t0); err != nil {
		t.Fatalf("an empty assignmentId is allowed: %v", err)
	}
	if _, _, err := Arrive("TRK-1", "node-ds-01", strings.Repeat("a", MaxAssignmentID), true, t0, t0); err != nil {
		t.Fatalf("the assignmentId limit is allowed: %v", err)
	}
}

func TestArriveValidation(t *testing.T) {
	cases := []struct {
		name                  string
		tn, station, assignID string
		at                    time.Time
	}{
		{"bad tracking number", "", "n", "", t0},
		{"bad station", "TRK-1", "", "", t0},
		{"blank assignment", "TRK-1", "n", " ", t0},
		{"long assignment", "TRK-1", "n", strings.Repeat("a", MaxAssignmentID+1), t0},
		{"zero instant", "TRK-1", "n", "", time.Time{}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h, ev, err := Arrive(c.tn, c.station, c.assignID, true, c.at, t0)
			wantIs(t, err, shared.ErrValidation)
			if h != nil || ev != nil {
				t.Fatal("a refused arrival returned a history")
			}
		})
	}
}

func TestPlaceAndUnplace(t *testing.T) {
	h := arrived(t, false)
	wantNoErr(t, h.Place())
	assertAt(t, h, StatusInducted, 0, 2)
	wantIs(t, h.Place(), shared.ErrIllegalTransition)
	events, err := h.Unplace(t1)
	wantNoErr(t, err)
	assertAt(t, h, StatusAtStation, 0, 3)
	want := PackageInductionDeferred{Header: shared.Header{Version: 3, At: t1}, TrackingNumber: "TRK-1", AssignmentID: "asg-1"}
	if len(events) != 1 || !reflect.DeepEqual(events[0], want) {
		t.Fatalf("events = %#v", events)
	}
	if ev, err := h.Unplace(t1); !errors.Is(err, shared.ErrIllegalTransition) || ev != nil {
		t.Fatalf("Unplace of a waiting package = %v %v", ev, err)
	}
	assertAt(t, h, StatusAtStation, 0, 3)
}

func TestRecordFailureBeforeTheThird(t *testing.T) {
	h := arrived(t, true)
	if h.WouldReturn() {
		t.Fatal("the first failure must not return the package")
	}
	events, err := h.RecordFailure(true, t1)
	wantNoErr(t, err)
	assertAt(t, h, StatusInducted, 1, 2)
	if events != nil || h.WouldReturn() {
		t.Fatalf("first failure placed: events=%v", events)
	}
	// Second failure, no wave: deferred.
	events, err = h.RecordFailure(false, t1)
	wantNoErr(t, err)
	assertAt(t, h, StatusAtStation, 2, 3)
	if len(events) != 1 || events[0].EventName() != "PackageInductionDeferred" {
		t.Fatalf("second failure, no wave: events=%v", events)
	}
	wantNoErr(t, h.Place())
	if !h.WouldReturn() {
		t.Fatal("after two failures the next one is the last")
	}
}

func TestThirdFailureInitiatesTheReturn(t *testing.T) {
	h := twiceFailed(t)
	// Never re-induction, even when a wave took the package.
	events, err := h.RecordFailure(true, t1)
	wantNoErr(t, err)
	assertAt(t, h, StatusReturnInitiated, 3, 4)
	want := PackageReturnInitiated{Header: shared.Header{Version: 4, At: t1}, TrackingNumber: "TRK-1"}
	if len(events) != 1 || !reflect.DeepEqual(events[0], want) {
		t.Fatalf("events = %#v, want [%#v]", events, want)
	}
	assertReturnEvent(t, events[0])
	if ev, err := h.RecordFailure(true, t1); !errors.Is(err, shared.ErrIllegalTransition) || ev != nil {
		t.Fatalf("a fourth failure = %v %v", ev, err)
	}
	assertAt(t, h, StatusReturnInitiated, 3, 4)
}

func twiceFailed(t *testing.T) *History {
	t.Helper()
	h := arrived(t, true)
	for i := 0; i < MaxFailedAttempts-1; i++ {
		_, err := h.RecordFailure(true, t1)
		wantNoErr(t, err)
	}
	return h
}

func assertReturnEvent(t *testing.T, ev shared.Event) {
	t.Helper()
	if ev.EventName() != "PackageReturnInitiated" || ev.Entity() != "packageattempthistory" ||
		ev.SubjectID() != "TRK-1" || ev.AggregateVersion() != 4 {
		t.Fatal("return event shape wrong")
	}
}

func TestRecordFailureRequiresInducted(t *testing.T) {
	for _, build := range []func(*testing.T) *History{
		func(t *testing.T) *History { return arrived(t, false) },
		deliveredHistory,
	} {
		h := build(t)
		before := h.Snapshot()
		ev, err := h.RecordFailure(true, t1)
		wantIs(t, err, shared.ErrIllegalTransition)
		if ev != nil || !reflect.DeepEqual(h.Snapshot(), before) {
			t.Fatal("a refused failure changed the history")
		}
	}
}

func deliveredHistory(t *testing.T) *History {
	t.Helper()
	h := arrived(t, true)
	wantNoErr(t, h.RecordDelivered())
	return h
}

func TestRecordDelivered(t *testing.T) {
	h := deliveredHistory(t)
	assertAt(t, h, StatusDelivered, 0, 2)
	wantIs(t, h.RecordDelivered(), shared.ErrAlreadyDelivered)
	assertAt(t, h, StatusDelivered, 0, 2)
	waiting := arrived(t, false)
	wantIs(t, waiting.RecordDelivered(), shared.ErrIllegalTransition)
	assertAt(t, waiting, StatusAtStation, 0, 1)
	returned := arrived(t, true)
	for i := 0; i < MaxFailedAttempts; i++ {
		_, err := returned.RecordFailure(true, t1)
		wantNoErr(t, err)
	}
	wantIs(t, returned.RecordDelivered(), shared.ErrIllegalTransition)
	if errors.Is(returned.RecordDelivered(), shared.ErrAlreadyDelivered) {
		t.Fatal("a returned package is not an already delivered one")
	}
}

func TestStatusValid(t *testing.T) {
	for _, s := range []Status{StatusAtStation, StatusInducted, StatusDelivered, StatusReturnInitiated} {
		if !s.Valid() {
			t.Errorf("%s must be valid", s)
		}
	}
	if Status("").Valid() || Status("LOST").Valid() {
		t.Fatal("unknown statuses must be invalid")
	}
}

func validSnapshot() Snapshot {
	return Snapshot{
		TrackingNumber: "TRK-1", StationNodeID: "node-ds-01", AssignmentID: "asg-1", FailedAttempts: 1,
		Status: StatusInducted, ArrivedAt: t0, Version: 3,
	}
}

func TestSnapshotRestore(t *testing.T) {
	h, err := Restore(validSnapshot())
	wantNoErr(t, err)
	if !reflect.DeepEqual(h.Snapshot(), validSnapshot()) {
		t.Fatalf("round trip = %+v", h.Snapshot())
	}
	accept := []func(*Snapshot){
		func(s *Snapshot) { s.Version = 1 },
		func(s *Snapshot) { s.FailedAttempts = 0 },
		func(s *Snapshot) { s.AssignmentID = "" },
		func(s *Snapshot) { s.Status, s.FailedAttempts = StatusReturnInitiated, MaxFailedAttempts },
		func(s *Snapshot) { s.Status, s.FailedAttempts = StatusAtStation, MaxFailedAttempts-1 },
		func(s *Snapshot) { s.Status = StatusDelivered },
	}
	for i, m := range accept {
		s := validSnapshot()
		m(&s)
		if _, err := Restore(s); err != nil {
			t.Errorf("accept case %d: %v", i, err)
		}
	}
	reject := []func(*Snapshot){
		func(s *Snapshot) { s.TrackingNumber = "" },
		func(s *Snapshot) { s.StationNodeID = "" },
		func(s *Snapshot) { s.AssignmentID = " x" },
		func(s *Snapshot) { s.ArrivedAt = time.Time{} },
		func(s *Snapshot) { s.Status = "LOST" },
		func(s *Snapshot) { s.Version = 0 },
		func(s *Snapshot) { s.FailedAttempts = -1 },
		func(s *Snapshot) { s.FailedAttempts = MaxFailedAttempts + 1 },
		func(s *Snapshot) { s.Status = StatusReturnInitiated },
		func(s *Snapshot) { s.FailedAttempts = MaxFailedAttempts },
	}
	for i, m := range reject {
		s := validSnapshot()
		m(&s)
		if h, err := Restore(s); !errors.Is(err, shared.ErrInvalidSnapshot) || h != nil {
			t.Errorf("reject case %d: Restore = %v, %v", i, h, err)
		}
	}
}
