package route

import (
	"reflect"
	"testing"
	"time"

	"github.com/claudioed/last-mile-delivery/internal/domain/shared"
	"github.com/claudioed/last-mile-delivery/internal/domain/wave"
)

// The C8-LM pickup seam (ADR 0017): a PICKUP induction collects from the stop
// instead of delivering to it.

// pickupWave is closedWave with TRK-1 inducted as a PICKUP at k-a: TRK-1 is
// collected there, TRK-2 delivered there, TRK-3 delivered at k-b.
func pickupWave(t *testing.T) *wave.DeliveryWave {
	t.Helper()
	w, _, err := wave.Schedule(wave.NewWave{
		ID: "wave-1", StationNodeID: "node-ds-01", ServiceDate: "2026-10-24", InductCutoffAt: cutoff,
		WindowStart: t1, WindowEnd: t1.Add(8 * time.Hour), StopCapacity: 10,
	}, t0)
	wantNoErr(t, err)
	_, err = w.Induct("TRK-1", "k-a", wave.KindPickup, t0, t0)
	wantNoErr(t, err)
	for _, in := range [][2]string{{"TRK-2", "k-a"}, {"TRK-3", "k-b"}} {
		_, err = w.Induct(in[0], in[1], wave.KindDelivery, t0, t0)
		wantNoErr(t, err)
	}
	wantNoErr(t, w.CloseInduction())
	return w
}

// startedPickup plans the standard route and starts it on pickupWave.
func startedPickup(t *testing.T) *Route {
	t.Helper()
	r := planned(t)
	_, err := r.Start(t1, pickupWave(t))
	wantNoErr(t, err)
	return r
}

func TestCollectedNeedsAPickupInduction(t *testing.T) {
	r := startedPickup(t)
	events, err := r.RecordAttempt("TRK-1", wave.KindPickup, OutcomeCollected, "", "", t2, t2)
	wantNoErr(t, err)
	if got := events[0].(DeliveryAttempted); got.Outcome != OutcomeCollected || got.FailureReason != "" {
		t.Fatalf("collected event = %+v", got)
	}
	// A DELIVERY package can not be collected (ADR 0017).
	before := r.Snapshot()
	_, err = r.RecordAttempt("TRK-2", wave.KindDelivery, OutcomeCollected, "", "", t2, t2)
	wantIs(t, err, shared.ErrValidation)
	if !reflect.DeepEqual(r.Snapshot(), before) {
		t.Fatal("a refused attempt changed the route")
	}
}

func TestCollectedCarriesNoDetailAndNoReason(t *testing.T) {
	r := startedPickup(t)
	_, err := r.RecordAttempt("TRK-1", wave.KindPickup, OutcomeCollected, DetailOther, "", t2, t2)
	wantIs(t, err, shared.ErrValidation)
	_, err = r.RecordAttempt("TRK-1", wave.KindPickup, OutcomeCollected, "", "no shopper", t2, t2)
	wantIs(t, err, shared.ErrValidation)
}

func TestFailedAttemptIsValidForAPickupPackage(t *testing.T) {
	r := startedPickup(t)
	events, err := r.RecordAttempt("TRK-1", wave.KindPickup, OutcomeFailed, DetailNotHome, "nobody answered", t2, t2)
	wantNoErr(t, err)
	if got := events[0].(DeliveryAttempted); got.Outcome != OutcomeFailed {
		t.Fatalf("failed event = %+v", got)
	}
}

func TestStartRaisesPackagePickupOutForAPickupInduction(t *testing.T) {
	r := planned(t)
	events, err := r.Start(t1, pickupWave(t))
	wantNoErr(t, err)
	pick, ok := events[0].(PackagePickupOut)
	if !ok {
		t.Fatalf("event 0 = %T, want PackagePickupOut", events[0])
	}
	want := PackagePickupOut{
		Header: shared.Header{Version: 2, At: t1}, RouteID: "route-1", TrackingNumber: "TRK-1",
		StopKey: "k-a", StationNodeID: "node-ds-01", At: t1,
	}
	if pick != want {
		t.Fatalf("pickup event = %#v, want %#v", pick, want)
	}
	if pick.EventName() != "PackagePickupOut" || pick.Entity() != "route" || pick.SubjectID() != "route-1" {
		t.Fatal("pickup event shape wrong")
	}
	out, ok := events[1].(PackageOutForDelivery)
	if !ok || out.TrackingNumber != "TRK-2" {
		t.Fatalf("event 1 = %#v, want PackageOutForDelivery of TRK-2", events[1])
	}
	if events[1].SubjectID() != pick.SubjectID() {
		t.Fatal("both streams must share the subject and key so they stay ordered")
	}
}
