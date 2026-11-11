package route

import (
	"reflect"
	"testing"

	"github.com/claudioed/last-mile-delivery/internal/domain/shared"
)

func TestDetailValidMatrix(t *testing.T) {
	cases := []struct {
		d       Detail
		failed  bool
		deliver bool
	}{
		{"", true, true},
		{DetailNotHome, true, false},
		{DetailAccessProblem, true, false},
		{DetailOther, true, false},
		{DetailSafePlaceUsed, false, true},
		{"UNKNOWN", false, false},
	}
	for _, c := range cases {
		if got := c.d.Valid(OutcomeFailed); got != c.failed {
			t.Errorf("%q.Valid(FAILED) = %v, want %v", c.d, got, c.failed)
		}
		if got := c.d.Valid(OutcomeDelivered); got != c.deliver {
			t.Errorf("%q.Valid(DELIVERED) = %v, want %v", c.d, got, c.deliver)
		}
	}
	if DetailNotHome.Valid("LOST") || !Detail("").Valid("LOST") {
		t.Fatal("only the empty detail is valid for an unknown outcome")
	}
}

func TestRecordAttemptCarriesTheDetail(t *testing.T) {
	r := started(t)
	events, err := r.RecordAttempt("TRK-1", OutcomeFailed, DetailAccessProblem, "gate locked", t2, t2)
	wantNoErr(t, err)
	if got := events[0].(DeliveryAttempted); got.Detail != DetailAccessProblem || got.FailureReason != "gate locked" {
		t.Fatalf("event = %+v", got)
	}
	a, _ := r.AttemptOf("TRK-1")
	if a.Detail != DetailAccessProblem {
		t.Fatalf("attempt = %+v", a)
	}
	events, err = r.RecordAttempt("TRK-2", OutcomeDelivered, DetailSafePlaceUsed, "", t2, t2)
	wantNoErr(t, err)
	if got := events[0].(DeliveryAttempted); got.Detail != DetailSafePlaceUsed {
		t.Fatalf("event = %+v", got)
	}
}

func TestRecordAttemptRefusesAMismatchedDetail(t *testing.T) {
	cases := []struct {
		name    string
		outcome Outcome
		detail  Detail
		reason  string
	}{
		{"safe place on a failure", OutcomeFailed, DetailSafePlaceUsed, "absent"},
		{"not home on a delivery", OutcomeDelivered, DetailNotHome, ""},
		{"access problem on a delivery", OutcomeDelivered, DetailAccessProblem, ""},
		{"other on a delivery", OutcomeDelivered, DetailOther, ""},
		{"unknown on a failure", OutcomeFailed, "UNKNOWN", "absent"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := started(t)
			before := r.Snapshot()
			events, err := r.RecordAttempt("TRK-1", c.outcome, c.detail, c.reason, t2, t2)
			wantIs(t, err, shared.ErrValidation)
			if events != nil || !reflect.DeepEqual(r.Snapshot(), before) {
				t.Fatal("a refused attempt changed the route")
			}
		})
	}
}

func TestSnapshotRoundTripKeepsTheDetail(t *testing.T) {
	r := started(t)
	_, err := r.RecordAttempt("TRK-1", OutcomeFailed, DetailNotHome, "absent", t2, t2)
	wantNoErr(t, err)
	_, err = r.RecordAttempt("TRK-2", OutcomeDelivered, DetailSafePlaceUsed, "", t2, t2)
	wantNoErr(t, err)
	got, err := Restore(r.Snapshot())
	wantNoErr(t, err)
	if a := got.Snapshot().Attempts; a[0].Detail != DetailNotHome || a[1].Detail != DetailSafePlaceUsed {
		t.Fatalf("details lost: %+v", a)
	}
}

func TestRestoreRejectsAMismatchedDetail(t *testing.T) {
	for _, a := range []Attempt{
		{TrackingNumber: "TRK-1", Outcome: OutcomeFailed, Detail: DetailSafePlaceUsed, FailureReason: "x", At: t2},
		{TrackingNumber: "TRK-1", Outcome: OutcomeDelivered, Detail: DetailNotHome, At: t2},
	} {
		r, err := Restore(snapshotWith(StateOutForDelivery, []Attempt{a}))
		wantIs(t, err, shared.ErrInvalidSnapshot)
		if r != nil {
			t.Fatal("a corrupt row became a live route")
		}
	}
}
