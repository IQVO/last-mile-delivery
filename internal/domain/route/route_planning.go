package route

import (
	"errors"
	"fmt"

	"github.com/claudioed/last-mile-delivery/internal/domain/shared"
	"github.com/claudioed/last-mile-delivery/internal/domain/wave"
)

// checkStops validates the shape of the stops: at least one, numbered 1..n in
// order, each with at least one valid tracking number, no tracking number twice.
func checkStops(stops []Stop) error {
	if len(stops) == 0 {
		return errors.New("a route needs at least one stop")
	}
	seen := map[string]bool{}
	for i, s := range stops {
		position := i + 1
		if s.No != position {
			return fmt.Errorf("stop numbers must be 1..n in order: position %d has stopNo %d", position, s.No)
		}
		if len(s.TrackingNumbers) == 0 {
			return fmt.Errorf("stop %d has no tracking number", s.No)
		}
		for _, tn := range s.TrackingNumbers {
			if !shared.ValidID(tn) {
				return fmt.Errorf("trackingNumber %q is not a valid id", tn)
			}
			if seen[tn] {
				return fmt.Errorf("trackingNumber %q is on the route twice", tn)
			}
			seen[tn] = true
		}
	}
	return nil
}

func checkPlanning(stops []Stop, pl Planning) error {
	if pl.Wave.State() != wave.StateInductionClosed {
		return fmt.Errorf("%w: PlanRoute needs wave %s INDUCTION_CLOSED but it is %s", shared.ErrIllegalTransition, pl.Wave.ID(), pl.Wave.State())
	}
	if !pl.Partner.Active() {
		return fmt.Errorf("%w: %s", ErrPartnerInactive, pl.Partner.ID())
	}
	if pl.Partner.StationNodeID() != pl.Wave.StationNodeID() {
		return fmt.Errorf("%w: partner %s works for %s, the wave for %s", ErrPartnerStationMismatch, pl.Partner.ID(), pl.Partner.StationNodeID(), pl.Wave.StationNodeID())
	}
	if len(stops) > pl.Partner.VanCapacity() {
		return fmt.Errorf("%w: %d stops, van capacity %d", ErrRouteOverCapacity, len(stops), pl.Partner.VanCapacity())
	}
	for _, s := range stops {
		if err := checkStopPackages(s, pl); err != nil {
			return err
		}
	}
	return nil
}

func checkStopPackages(s Stop, pl Planning) error {
	key := ""
	for i, tn := range s.TrackingNumbers {
		in, ok := pl.Wave.Induction(tn)
		if !ok {
			return fmt.Errorf("%w: %s in wave %s", ErrPackageNotInducted, tn, pl.Wave.ID())
		}
		if pl.Routed[tn] {
			return fmt.Errorf("%w: %s", ErrPackageAlreadyRouted, tn)
		}
		if i > 0 && in.StopKey != key {
			return fmt.Errorf("%w: stop %d mixes stop keys", ErrStopKeyMismatch, s.No)
		}
		key = in.StopKey
	}
	return nil
}
