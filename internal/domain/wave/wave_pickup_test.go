package wave

import (
	"testing"

	"github.com/claudioed/last-mile-delivery/internal/domain/shared"
)

// The C8-LM pickup seam (ADR 0017): a PICKUP induction is an induction like
// any other — it takes stop capacity and follows the same rules — and the
// kind itself is checked.

func TestPickupInductionTakesStopCapacityLikeAnyOther(t *testing.T) {
	w, _, err := Schedule(NewWave{
		ID: "wave-1", StationNodeID: "node-ds-01", ServiceDate: "2026-10-24", InductCutoffAt: cutoff,
		WindowStart: wStart, WindowEnd: wEnd, StopCapacity: 1,
	}, t0)
	wantNoErr(t, err)
	events, err := w.Induct("TRK-1", "k-a", KindPickup, t0, t0)
	wantNoErr(t, err)
	if len(events) != 1 || w.AssignedStops() != 1 {
		t.Fatalf("a PICKUP induction must take the stop and raise capacity: %+v assigned=%d", events, w.AssignedStops())
	}
	// A second PICKUP on a new stop is refused once the wave is full; on the
	// same stop it takes no further capacity, exactly like a delivery.
	_, err = w.Induct("TRK-2", "k-b", KindPickup, t0, t0)
	wantIs(t, err, ErrWaveFull)
	events, err = w.Induct("TRK-2", "k-a", KindPickup, t0, t0)
	wantNoErr(t, err)
	if events != nil || w.AssignedStops() != 1 {
		t.Fatalf("a pickup joining an existing stop takes no capacity: %+v assigned=%d", events, w.AssignedStops())
	}
	if in, ok := w.Induction("TRK-1"); !ok || in.Kind != KindPickup {
		t.Fatalf("induction = %+v, want Kind PICKUP", in)
	}
}

func TestInductRefusesAnUnknownKind(t *testing.T) {
	w := mustSchedule(t, nil)
	_, err := w.Induct("TRK-1", "k-a", Kind("RETURN"), t0, t0)
	wantIs(t, err, shared.ErrValidation)
	if len(w.Inductions()) != 0 {
		t.Fatal("a refused induction changed the wave")
	}
}
