package route

import "slices"

// HasPackage reports whether the tracking number is on the route.
func (r *Route) HasPackage(trackingNumber string) bool {
	return slices.Contains(r.Packages(), trackingNumber)
}

// Packages returns every tracking number of the route in stop order.
func (r *Route) Packages() []string {
	var out []string
	for _, s := range r.stops {
		out = append(out, s.TrackingNumbers...)
	}
	return out
}

// AttemptOf returns the attempt recorded for a package.
func (r *Route) AttemptOf(trackingNumber string) (Attempt, bool) {
	for _, a := range r.attempts {
		if a.TrackingNumber == trackingNumber {
			return a, true
		}
	}
	return Attempt{}, false
}

// AllAttempted reports whether every package of the route has an attempt.
func (r *Route) AllAttempted() bool { return len(r.attempts) == len(r.Packages()) }

// StopOutcome is the outcome of stop no, derived from its packages' attempts: no
// outcome until every package has one, then DELIVERED when all were delivered,
// else FAILED (ADR 0002, A17).
func (r *Route) StopOutcome(no int) (Outcome, bool) {
	if no < 1 || no > len(r.stops) {
		return "", false
	}
	out := OutcomeDelivered
	for _, tn := range r.stops[no-1].TrackingNumbers {
		a, ok := r.AttemptOf(tn)
		if !ok {
			return "", false
		}
		if a.Outcome == OutcomeFailed {
			out = OutcomeFailed
		}
	}
	return out, true
}
