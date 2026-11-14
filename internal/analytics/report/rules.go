package report

import (
	"errors"
	"fmt"
	"slices"
)

// The KPI rules of ADR 0012. They are functions, not package variables, so the
// vocabulary cannot be mutated by a caller and mutation testing reaches them.

// AttemptOutcomes is the as-built outcome vocabulary of DeliveryAttempted
// (apis/asyncapi.yaml): an attempt either ended the package's journey
// successfully (DELIVERED, or COLLECTED for a pickup package, ADR 0017) or
// failed.
func AttemptOutcomes() []string { return []string{OutcomeDelivered, OutcomeFailed, OutcomeCollected} }

// IsAttemptOutcome reports whether outcome is in AttemptOutcomes.
func IsAttemptOutcome(outcome string) bool { return slices.Contains(AttemptOutcomes(), outcome) }

// UnknownStation is the station a fact reports under while the
// out-for-delivery fact that names its station has not been projected.
func UnknownStation() string { return "UNKNOWN" }

// IsCompletedRoute is the completion rule of ADR 0012: CompleteRoute raises no
// event, so a route counts as completed once it has out-for-delivery packages
// and every one of them has an attempt (the precondition CompleteRoute
// enforces). outForDelivery is the route's packages, attempted how many of
// them have an attempt on the route.
func IsCompletedRoute(outForDelivery, attempted int) bool {
	return outForDelivery > 0 && attempted == outForDelivery
}

// ErrInvalidEvent marks a fact that can never be projected: a required field is
// missing or out of range. It is deterministic, so the projector dead-letters it.
var ErrInvalidEvent = errors.New("report: invalid analytics event")

// Validate checks the fields every fact needs and the fields of its Kind.
func (e Event) Validate() error {
	missing := firstMissing(
		required{"id", e.EventID != ""},
		required{"time", !e.At.IsZero()},
		required{"the business instant", !e.OccurredAt.IsZero()},
	)
	if missing == "" {
		missing = e.kindMissing()
	}
	if missing != "" {
		return fmt.Errorf("%w: %s %s: %s is required", ErrInvalidEvent, e.Kind, e.EventID, missing)
	}
	return nil
}

// kindMissing names the first missing field of the Kind's own payload, or
// rejects a Kind the projection does not know.
func (e Event) kindMissing() string {
	checks, known := e.kindChecks()
	if !known {
		return "a known kind"
	}
	return firstMissing(checks...)
}

// kindChecks lists the payload checks of each projected Kind.
func (e Event) kindChecks() ([]required, bool) {
	wave := required{"waveId", e.WaveID != ""}
	route := required{"routeId", e.RouteID != ""}
	tracking := required{"trackingNumber", e.TrackingNumber != ""}
	switch e.Kind {
	case KindWaveScheduled:
		return []required{wave, {"stationNodeId", e.StationNodeID != ""}, {"serviceDate", !e.ServiceDate.IsZero()},
			{"a non-negative stopCapacity", e.StopCapacity >= 0}}, true
	case KindWaveCapacityChanged:
		return []required{wave, {"a positive assignedStops", e.AssignedStops > 0}}, true
	case KindWaveCancelled:
		return []required{wave}, true
	case KindWaveCostDeclared:
		return []required{wave, {"a non-negative costPerStopCents", e.CostPerStopCents >= 0}}, true
	case KindRoutePlanned:
		return []required{route, wave, {"a positive stopCount", e.StopCount > 0}}, true
	case KindOutForDelivery:
		return []required{route, tracking, {"stationNodeId", e.StationNodeID != ""}}, true
	case KindDeliveryAttempted:
		return []required{route, tracking, {"an outcome of the vocabulary", IsAttemptOutcome(e.Outcome)}}, true
	case KindReturnInitiated:
		return []required{tracking}, true
	case KindStopEtaWindowAssigned:
		return []required{route, {"a positive stopNo", e.StopNo >= 1}, {"at least one trackingNumber, none blank", allNonBlank(e.TrackingNumbers)},
			{"etaStart before etaEnd", !e.EtaStart.IsZero() && e.EtaStart.Before(e.EtaEnd)}}, true
	default:
		return nil, false
	}
}

// required is one field check: name and whether it is present.
type required struct {
	name    string
	present bool
}

// firstMissing names the first absent field, or "" when every one is present.
func firstMissing(fields ...required) string {
	for _, f := range fields {
		if !f.present {
			return f.name
		}
	}
	return ""
}

// allNonBlank reports whether values holds at least one entry and no blank one.
func allNonBlank(values []string) bool {
	return len(values) > 0 && !slices.Contains(values, "")
}
