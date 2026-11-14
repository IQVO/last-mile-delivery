package wave

import (
	"slices"
	"time"
)

// The read-only accessors of DeliveryWave; the aggregate's behaviour lives in
// wave.go and the emission declaration in the events file.
// InductCutoffAt returns the induct cutoff.
func (w *DeliveryWave) InductCutoffAt() time.Time { return w.cutoff }

// WindowStart returns the start of the delivery window.
func (w *DeliveryWave) WindowStart() time.Time { return w.windowStart }

// WindowEnd returns the end of the delivery window.
func (w *DeliveryWave) WindowEnd() time.Time { return w.windowEnd }

// StopCapacity returns the stop capacity.
func (w *DeliveryWave) StopCapacity() int { return w.capacity }

// AssignedStops returns the stops taken so far.
func (w *DeliveryWave) AssignedStops() int { return w.assigned }

// RemainingStops returns the stops still free.
func (w *DeliveryWave) RemainingStops() int { return w.capacity - w.assigned }

// State returns the lifecycle state.
func (w *DeliveryWave) State() State { return w.state }

// CancelReason returns the cancel reason (empty unless CANCELLED).
func (w *DeliveryWave) CancelReason() string { return w.cancelReason }

// CostPerStopCents returns the declared marginal cost of one stop on the wave
// (integer cents; 0 until the planner declares one).
func (w *DeliveryWave) CostPerStopCents() int64 { return w.costPerStopCents }

// EmissionPerStopG returns the declared marginal greenhouse emission of one
// stop on the wave (grams of CO2 equivalent; 0 until the planner declares one).
func (w *DeliveryWave) EmissionPerStopG() int64 { return w.emissionPerStopG }

// Version returns the aggregate version.
func (w *DeliveryWave) Version() int64 { return w.version }

// Inductions returns a copy of the inductions in induction order.
func (w *DeliveryWave) Inductions() []Induction { return slices.Clone(w.inductions) }

// Induction returns the induction of a tracking number.
func (w *DeliveryWave) Induction(trackingNumber string) (Induction, bool) {
	for _, in := range w.inductions {
		if in.TrackingNumber == trackingNumber {
			return in, true
		}
	}
	return Induction{}, false
}
