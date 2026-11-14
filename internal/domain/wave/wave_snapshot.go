package wave

import (
	"time"

	"github.com/claudioed/last-mile-delivery/internal/domain/shared"
)

// The persisted form of DeliveryWave and its validation: the snapshot, the
// restore and the invariant check the repository relies on.

// Snapshot is the persisted form of a wave. Restore validates it.
type Snapshot struct {
	ID               ID
	StationNodeID    string
	ServiceDate      string
	InductCutoffAt   time.Time
	WindowStart      time.Time
	WindowEnd        time.Time
	StopCapacity     int
	AssignedStops    int
	Inductions       []Induction
	State            State
	CancelReason     string
	CostPerStopCents int64
	EmissionPerStopG int64
	Version          int64
}

// Restore rebuilds a persisted wave, validating every invariant.
func Restore(s Snapshot) (*DeliveryWave, error) {
	if err := checkSnapshot(s); err != nil {
		return nil, shared.Corrupt("wave %q: %v", s.ID, err)
	}
	return &DeliveryWave{
		id: s.ID, station: s.StationNodeID, serviceDate: s.ServiceDate, cutoff: shared.UTC(s.InductCutoffAt),
		windowStart: shared.UTC(s.WindowStart), windowEnd: shared.UTC(s.WindowEnd), capacity: s.StopCapacity,
		assigned: s.AssignedStops, inductions: normalise(s.Inductions), state: s.State, cancelReason: s.CancelReason,
		costPerStopCents: s.CostPerStopCents, emissionPerStopG: s.EmissionPerStopG, version: s.Version,
	}, nil
}
