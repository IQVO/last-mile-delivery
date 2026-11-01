package route

import (
	"errors"
	"fmt"

	"github.com/claudioed/last-mile-delivery/internal/domain/shared"
)

// Restore rebuilds a persisted route, validating every invariant.
func Restore(s Snapshot) (*Route, error) {
	if err := checkSnapshot(s); err != nil {
		return nil, shared.Corrupt("route %q: %v", s.ID, err)
	}
	return &Route{
		id: s.ID, waveID: s.WaveID, dspID: s.DSPID, station: s.StationNodeID, state: s.State, stops: cloneStops(s.Stops),
		attempts: normaliseAttempts(s.Attempts), version: s.Version,
	}, nil
}

func normaliseAttempts(in []Attempt) []Attempt {
	if len(in) == 0 {
		return nil
	}
	out := make([]Attempt, len(in))
	for i, a := range in {
		out[i] = Attempt{TrackingNumber: a.TrackingNumber, Outcome: a.Outcome, FailureReason: a.FailureReason, At: shared.UTC(a.At)}
	}
	return out
}

func checkSnapshot(s Snapshot) error {
	if !shared.ValidID(string(s.ID)) || !shared.ValidID(string(s.WaveID)) || !shared.ValidID(string(s.DSPID)) || !shared.ValidID(s.StationNodeID) {
		return errors.New("routeId, waveId, dspId and stationNodeId must be valid ids")
	}
	if s.Version < 1 || !s.State.Valid() {
		return errors.New("a version of at least 1 and a route state are required")
	}
	if err := checkStops(s.Stops); err != nil {
		return err
	}
	if err := checkSnapshotAttempts(s); err != nil {
		return err
	}
	return nil
}

func checkSnapshotAttempts(s Snapshot) error {
	if s.State == StatePlanned && len(s.Attempts) > 0 {
		return errors.New("a PLANNED route has no attempts")
	}
	on := map[string]bool{}
	for _, st := range s.Stops {
		for _, tn := range st.TrackingNumbers {
			on[tn] = true
		}
	}
	seen := map[string]bool{}
	for _, a := range s.Attempts {
		if !on[a.TrackingNumber] || seen[a.TrackingNumber] {
			return fmt.Errorf("attempt of %q is not a first attempt of a package on the route", a.TrackingNumber)
		}
		seen[a.TrackingNumber] = true
		if err := checkAttempt(a.TrackingNumber, a.Outcome, a.FailureReason, a.At); err != nil {
			return err
		}
	}
	if s.State == StateCompleted && len(s.Attempts) != len(on) {
		return errors.New("a COMPLETED route has an attempt for every package")
	}
	return nil
}
