package wave

import (
	"fmt"
	"slices"
	"time"

	"github.com/claudioed/last-mile-delivery/internal/domain/shared"
)

// The induction seam: a package takes one stop of the wave's capacity, the
// wave's uniqueness rules, and the capacity fact the analytics side reads.

// Induct places a package of the given stop into the wave. kind is why the
// package takes the stop, DELIVERY or PICKUP (ADR 0017; a PICKUP induction
// occupies stop capacity like any other). at is the instant
// the package became eligible (it must be before the cutoff), now the instant of
// the change. A package joining a stop already in the wave takes no capacity and
// raises no event; a new stop raises DeliveryWaveCapacityChanged.
func (w *DeliveryWave) Induct(trackingNumber, stopKey string, kind Kind, at, now time.Time) ([]shared.Event, error) {
	if !shared.ValidID(trackingNumber) || !shared.ValidStopKey(stopKey) || at.IsZero() {
		return nil, shared.Invalid("trackingNumber, stopKey and the induction instant must be valid")
	}
	if !kind.Valid() {
		return nil, shared.Invalid("induction kind %q is not DELIVERED or PICKUP", kind)
	}
	if w.state != StateOpen || !at.Before(w.cutoff) {
		return nil, fmt.Errorf("%w: wave %s is %s, cutoff %s", ErrInductionClosed, w.id, w.state, w.cutoff.Format(time.RFC3339))
	}
	if w.holds(trackingNumber) {
		return nil, fmt.Errorf("%w: %s in wave %s", ErrAlreadyInducted, trackingNumber, w.id)
	}
	newStop := !w.hasStop(stopKey)
	if newStop && w.assigned >= w.capacity {
		return nil, fmt.Errorf("%w: wave %s has %d of %d stops", ErrWaveFull, w.id, w.assigned, w.capacity)
	}
	w.inductions = append(w.inductions, Induction{TrackingNumber: trackingNumber, StopKey: stopKey, Kind: kind, At: shared.UTC(now)})
	w.version++
	if !newStop {
		return nil, nil
	}
	w.assigned++
	return []shared.Event{w.capacityChanged(now)}, nil
}

func (w *DeliveryWave) capacityChanged(now time.Time) DeliveryWaveCapacityChanged {
	return DeliveryWaveCapacityChanged{
		Header: shared.Header{Version: w.version, At: shared.UTC(now)}, WaveID: w.id, StopCapacity: w.capacity,
		AssignedStops: w.assigned, RemainingStops: w.capacity - w.assigned,
	}
}

func (w *DeliveryWave) holds(trackingNumber string) bool {
	return slices.ContainsFunc(w.inductions, func(in Induction) bool { return in.TrackingNumber == trackingNumber })
}

func (w *DeliveryWave) hasStop(stopKey string) bool {
	return slices.ContainsFunc(w.inductions, func(in Induction) bool { return in.StopKey == stopKey })
}
