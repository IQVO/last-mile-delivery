package wave

import (
	"fmt"
	"time"

	"github.com/claudioed/last-mile-delivery/internal/domain/shared"
)

// The planner's declarations on a wave: cost and emission per stop, the F7
// seams. Lifecycle rules live with the aggregate in wave.go.
// DeclareCost records the planner's declared marginal cost of ONE stop on the
// wave, in integer cents (ADR 0011, roadmap F7), and raises WaveCostDeclared.
// It is allowed while the wave is still running (OPEN, INDUCTION_CLOSED or
// DISPATCHED) and refused once the wave is CANCELLED or COMPLETED; declaring
// again overwrites the last value. Zero is a legal declaration: it means a
// declared zero marginal cost, which is not the same as an undeclared wave.
func (w *DeliveryWave) DeclareCost(costPerStopCents int64, now time.Time) ([]shared.Event, error) {
	if costPerStopCents < 0 || costPerStopCents > MaxCostPerStopCents {
		return nil, shared.Invalid("costPerStopCents must be between 0 and %d", MaxCostPerStopCents)
	}
	if w.state == StateCancelled || w.state == StateCompleted {
		return nil, fmt.Errorf("%w: DeclareCost needs a running wave but wave %s is %s", shared.ErrIllegalTransition, w.id, w.state)
	}
	w.costPerStopCents = costPerStopCents
	w.version++
	ev := WaveCostDeclared{
		Header: shared.Header{Version: w.version, At: shared.UTC(now)}, WaveID: w.id,
		CostPerStopCents: costPerStopCents, DeclaredAt: shared.UTC(now),
	}
	return []shared.Event{ev}, nil
}

// DeclareEmission records the planner's declared marginal greenhouse emission
// of ONE stop on the wave, in grams of CO2 equivalent (ADR 0016, roadmap C9),
// and raises WaveEmissionDeclared. It obeys the same lifecycle rule as
// DeclareCost (allowed while the wave still runs; refused once CANCELLED or
// COMPLETED) and a later declaration overwrites the last value. Zero is a
// legal declaration: it means a declared zero marginal emission, which is not
// the same as an undeclared wave.
func (w *DeliveryWave) DeclareEmission(gCo2ePerStop int64, now time.Time) ([]shared.Event, error) {
	if gCo2ePerStop < 0 || gCo2ePerStop > MaxEmissionPerStopG {
		return nil, shared.Invalid("gCo2ePerStop must be between 0 and %d", MaxEmissionPerStopG)
	}
	if w.state == StateCancelled || w.state == StateCompleted {
		return nil, fmt.Errorf("%w: DeclareEmission needs a running wave but wave %s is %s", shared.ErrIllegalTransition, w.id, w.state)
	}
	w.emissionPerStopG = gCo2ePerStop
	w.version++
	ev := WaveEmissionDeclared{
		Header: shared.Header{Version: w.version, At: shared.UTC(now)}, WaveID: w.id,
		GCo2ePerStop: gCo2ePerStop, DeclaredAt: shared.UTC(now),
	}
	return []shared.Event{ev}, nil
}
