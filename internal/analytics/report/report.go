// Package report is the self-contained read-model region of the analytics read
// side (ADR 0012): the facts the projector folds in, the shapes of the
// route-efficiency, attempt-outcome and wave-utilisation reports, the KPI rules
// (the attempt-outcome vocabulary, the unknown station, the means and rates),
// the date-range rules, the freshness lag, and the writer/reader ports the
// analytical store implements. It imports no other internal package (arch
// test): the vocabulary below is hand-mirrored from apis/asyncapi.yaml, never
// imported from the domain, so the OLTP model can never leak into the
// projection.
package report

import (
	"context"
	"errors"
	"time"
)

// Kind is which of the nine projected events a fact came from. The names are
// the CloudEvents event names (the last segment of `type`).
type Kind string

// The nine projected events of this context's own two topics.
const (
	KindWaveScheduled         Kind = "DeliveryWaveScheduled"
	KindWaveCapacityChanged   Kind = "DeliveryWaveCapacityChanged"
	KindWaveCancelled         Kind = "DeliveryWaveCancelled"
	KindWaveCostDeclared      Kind = "WaveCostDeclared"
	KindRoutePlanned          Kind = "RoutePlanned"
	KindOutForDelivery        Kind = "PackageOutForDelivery"
	KindDeliveryAttempted     Kind = "DeliveryAttempted"
	KindStopEtaWindowAssigned Kind = "StopEtaWindowAssigned"
	KindReturnInitiated       Kind = "PackageReturnInitiated"
)

// Attempt outcomes, the as-built vocabulary of DeliveryAttempted.
const (
	OutcomeDelivered = "DELIVERED"
	OutcomeFailed    = "FAILED"
	// OutcomeCollected ended a pickup package's journey (ADR 0017); the KPIs
	// read it as a success like DELIVERED.
	OutcomeCollected = "COLLECTED"
)

// Event is one decoded analytics fact ready to project. It is the union of the
// nine payloads; only the fields of its Kind are set.
type Event struct {
	Kind    Kind
	EventID string    // CloudEvents id: the idempotency key
	At      time.Time // CloudEvents time: when the use case raised it (freshness)
	// OccurredAt is the business instant of the fact: the attempt's and the
	// out-for-delivery's `at`, the cost's `declaredAt`, else the CloudEvents
	// time (the wave and route events carry no other instant).
	OccurredAt time.Time

	// The wave events.
	WaveID           string
	StationNodeID    string    // DeliveryWaveScheduled, PackageOutForDelivery
	ServiceDate      time.Time // DeliveryWaveScheduled: 00:00 UTC of serviceDate
	StopCapacity     int       // DeliveryWaveScheduled
	AssignedStops    int       // DeliveryWaveCapacityChanged
	CostPerStopCents int64     // WaveCostDeclared

	// The route events (RoutePlanned also sets WaveID).
	RouteID   string
	DSPID     string // RoutePlanned
	StopCount int    // RoutePlanned
	Outcome   string // DeliveryAttempted

	// TrackingNumber is the package of PackageOutForDelivery,
	// DeliveryAttempted and PackageReturnInitiated.
	TrackingNumber string

	// StopEtaWindowAssigned (the route events' RouteID too): the stop, the
	// packages of the stop and the ETA window; the CloudEvents time is the
	// assignment instant (OccurredAt).
	StopNo          int
	TrackingNumbers []string
	EtaStart        time.Time
	EtaEnd          time.Time
}

// Projection is the WRITER port: Apply records the event id and projects the
// fact in ONE transaction. applied is false when the id was already recorded
// (a replay): nothing changed. An error wrapping ErrRejected is deterministic
// (the store can never accept this event: retry is pointless); any other error
// is transient and the same event may be retried.
type Projection interface {
	Apply(ctx context.Context, e Event) (applied bool, err error)
}

// ErrRejected marks an event the analytical store deterministically refuses
// (a data-exception or integrity-violation class error).
var ErrRejected = errors.New("report: event rejected by the analytical store")

// Reader is the READER port. Every method answers for the half-open instant
// range [r.From, r.To), bucketed by UTC day. Results are ordered by day, then
// station, and never nil.
type Reader interface {
	RouteEfficiency(ctx context.Context, r Range) ([]RouteDay, error)
	AttemptOutcomes(ctx context.Context, r Range) ([]AttemptDay, error)
	WaveUtilisation(ctx context.Context, r Range) ([]WaveDay, error)
	// EtaWindow is the day-of ETA hit report (ADR 0013).
	EtaWindow(ctx context.Context, r Range) ([]EtaDay, error)
	// LastEventAt is the CloudEvents time of the newest event the projection
	// applied (nil while it applied none): the basis of the freshness lag.
	LastEventAt(ctx context.Context) (*time.Time, error)
}

// RouteDay is what the store sums for one UTC day of completion and one
// station over the completed routes (CompletedRoute): their count, stops and
// packages, and the same sums over the routes whose wave declared a cost,
// with the declared cents (costPerStopCents x stopCount).
type RouteDay struct {
	Day            time.Time
	Station        string
	Routes         int
	Stops          int
	Packages       int
	CostedStops    int
	CostedPackages int
	CostedCents    int64
}

// AttemptDay is what the store counts for one UTC day and one station: the
// attempts by outcome, the packages whose first attempt fell on the day (and
// how many of those were delivered then, and all their attempts on any day),
// and the returns initiated.
type AttemptDay struct {
	Day                         time.Time
	Station                     string
	DeliveredAttempts           int
	FailedAttempts              int
	PackagesAttempted           int
	PackagesFirstAttemptSuccess int
	AttemptsOfPackagesAttempted int
	ReturnsInitiated            int
}

// WaveDay is what the store sums for one service date and one station: the
// waves scheduled and cancelled, their scheduled stop capacity and their
// inducted stops.
type WaveDay struct {
	Day            time.Time
	Station        string
	WavesScheduled int
	WavesCancelled int
	StopCapacity   int
	StopsInducted  int
}

// EtaDay is what the store counts for one UTC day of the DELIVERED attempt and
// one station (ADR 0013): the deliveries that had a StopEtaWindowAssigned row
// for their route and package, and how many fell inside, before and after the
// window. A delivery whose ETA row was never projected is in none of them.
type EtaDay struct {
	Day               time.Time
	Station           string
	DeliveriesWithEta int
	DeliveriesInside  int
	DeliveriesBefore  int
	DeliveriesAfter   int
}
