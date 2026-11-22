package deliverypoint

import (
	"errors"
	"fmt"
	"slices"

	"github.com/claudioed/last-mile-delivery/internal/domain/shared"
)

// Restore rebuilds a persisted profile, validating every invariant of ADR 0014:
// a valid address stop key; counters in range, attempts equal to the sum of the
// outcome counters (both saturating at shared.MaxInt32) and safePlaceUsed at most
// delivered; at most MaxStrikes strikes, each a counted category with its own
// route and 1 to MaxStrikePackages distinct packages, the told ones among them; and a flag open exactly when FlagAfterStrikes strikes are.
func Restore(s Snapshot) (*Profile, error) {
	if err := checkSnapshot(s); err != nil {
		return nil, shared.Corrupt("delivery point profile %q: %v", s.StopKey, err)
	}
	strikes := cloneStrikes(s.Strikes)
	for i := range strikes {
		strikes[i].At = shared.UTC(strikes[i].At)
	}
	return &Profile{
		stopKey: s.StopKey, attempts: s.Attempts, delivered: s.Delivered, safePlaceUsed: s.SafePlaceUsed,
		failedNotHome: s.FailedNotHome, failedAccess: s.FailedAccess, failedOther: s.FailedOther, strikes: strikes,
		flaggedAt: shared.UTC(s.FlaggedAt), lastAttemptAt: shared.UTC(s.LastAttemptAt), version: s.Version,
	}, nil
}

func checkSnapshot(s Snapshot) error {
	if !validStopKey(s.StopKey) {
		return errors.New("the stop key must be a valid address stop key")
	}
	if s.Version < 1 || s.LastAttemptAt.IsZero() {
		return errors.New("a version of at least 1 and the last attempt instant are required")
	}
	if err := checkCounters(s); err != nil {
		return err
	}
	if err := checkStrikes(s.Strikes); err != nil {
		return err
	}
	if (len(s.Strikes) >= FlagAfterStrikes) == s.FlaggedAt.IsZero() {
		return fmt.Errorf("flaggedAt must be set exactly when %d or more strikes are open", FlagAfterStrikes)
	}
	return nil
}

func checkCounters(s Snapshot) error {
	all := []int{s.Attempts, s.Delivered, s.SafePlaceUsed, s.FailedNotHome, s.FailedAccess, s.FailedOther}
	var sum int64
	for _, n := range all {
		if n < 0 || n > shared.MaxInt32 {
			return fmt.Errorf("a counter of %d is outside 0 to %d", n, shared.MaxInt32)
		}
	}
	for _, n := range []int{s.Delivered, s.FailedNotHome, s.FailedAccess, s.FailedOther} {
		sum += int64(n)
	}
	if int64(s.Attempts) != min(sum, shared.MaxInt32) {
		return fmt.Errorf("attempts %d is not the sum %d of the outcome counters", s.Attempts, sum)
	}
	if s.SafePlaceUsed > s.Delivered {
		return errors.New("safePlaceUsed exceeds delivered")
	}
	return nil
}

func checkStrikes(in []Strike) error {
	if len(in) > MaxStrikes {
		return fmt.Errorf("%d strikes exceed the ring of %d", len(in), MaxStrikes)
	}
	routes := map[string]bool{}
	for _, k := range in {
		if !shared.ValidID(k.RouteID) || k.At.IsZero() {
			return errors.New("a strike needs a route and an instant")
		}
		if !k.Category.Valid() {
			return fmt.Errorf("strike category %q is not a counted category", k.Category)
		}
		if routes[k.RouteID] {
			return fmt.Errorf("two strikes share route %q", k.RouteID)
		}
		routes[k.RouteID] = true
		if err := checkPackages(k); err != nil {
			return err
		}
	}
	return nil
}

// checkPackages validates the packages a strike names: 1 to MaxStrikePackages
// distinct valid tracking numbers, and a told set that is a distinct subset.
func checkPackages(k Strike) error {
	if len(k.TrackingNumbers) < 1 || len(k.TrackingNumbers) > MaxStrikePackages {
		return fmt.Errorf("a strike names 1 to %d packages, not %d", MaxStrikePackages, len(k.TrackingNumbers))
	}
	for i, tn := range k.TrackingNumbers {
		if !shared.ValidID(tn) || slices.Contains(k.TrackingNumbers[:i], tn) {
			return fmt.Errorf("strike package %q is not a distinct valid tracking number", tn)
		}
	}
	for i, tn := range k.Told {
		if !slices.Contains(k.TrackingNumbers, tn) || slices.Contains(k.Told[:i], tn) {
			return fmt.Errorf("told package %q is not a distinct package of the strike", tn)
		}
	}
	return nil
}
