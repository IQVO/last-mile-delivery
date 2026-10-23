// Package route is the Route aggregate: one delivery service partner's van route
// within one wave, as ordered stops, and the delivery attempts recorded on it.
//
// States: PLANNED, OUT_FOR_DELIVERY, COMPLETED; transitions PLANNED ->
// OUT_FOR_DELIVERY (StartRoute) and OUT_FOR_DELIVERY -> COMPLETED
// (CompleteRoute). RecordAttempt keeps OUT_FOR_DELIVERY. Invariants: stops <= the
// partner's van capacity (ErrRouteOverCapacity); an attempt is recorded only on an
// OUT_FOR_DELIVERY route (ErrRouteNotStarted); a package is delivered at most once
// (ErrAlreadyDelivered). Gap Register G34 adds: a route holds only packages
// inducted in its wave, only from INDUCTION_CLOSED. Outcomes are per package; a
// stop's outcome is derived (G17).
package route

import (
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/claudioed/last-mile-delivery/internal/domain/partner"
	"github.com/claudioed/last-mile-delivery/internal/domain/shared"
	"github.com/claudioed/last-mile-delivery/internal/domain/wave"
)

// The Route invariants of the model and of ADR 0002, by name.
var (
	// ErrRouteOverCapacity: stops must not exceed the partner's van capacity (409).
	ErrRouteOverCapacity = errors.New("route over capacity")
	// ErrRouteNotStarted: an attempt is recorded only on an OUT_FOR_DELIVERY route (409).
	ErrRouteNotStarted = errors.New("route not started")
	// ErrAlreadyDelivered: a package is delivered at most once (409).
	ErrAlreadyDelivered = shared.ErrAlreadyDelivered
	// ErrPackageNotOnRoute: the attempted package is not on the route (404).
	ErrPackageNotOnRoute = errors.New("package not on route")
	// ErrAttemptAlreadyRecorded: one attempt per package per route (409).
	ErrAttemptAlreadyRecorded = errors.New("attempt already recorded")
	// ErrPackageNotInducted: a route holds only packages inducted in its wave (409).
	ErrPackageNotInducted = errors.New("package not inducted in the wave")
	// ErrPackageAlreadyRouted: a package is on at most one route of a wave (409).
	ErrPackageAlreadyRouted = errors.New("package already on a route")
	// ErrStopKeyMismatch: the packages of one stop share one stopKey (409).
	ErrStopKeyMismatch = errors.New("stop key mismatch")
	// ErrPartnerInactive: a route is given only to an ACTIVE partner (409).
	ErrPartnerInactive = errors.New("partner inactive")
	// ErrPartnerStationMismatch: the partner works for the wave's station (409).
	ErrPartnerStationMismatch = errors.New("partner station mismatch")
	// ErrRouteIncomplete: a route completes only when every package has an attempt (409).
	ErrRouteIncomplete = errors.New("route incomplete")
)

// ID identifies a route (routeId).
type ID string

// State is the lifecycle state of a route.
type State string

// The three route states of the model.
const (
	StatePlanned        State = "PLANNED"
	StateOutForDelivery State = "OUT_FOR_DELIVERY"
	StateCompleted      State = "COMPLETED"
)

// Valid reports whether s is one of the three states.
func (s State) Valid() bool {
	return s == StatePlanned || s == StateOutForDelivery || s == StateCompleted
}

// Outcome is the result of a delivery attempt.
type Outcome string

// The two attempt outcomes.
const (
	OutcomeDelivered Outcome = "DELIVERED"
	OutcomeFailed    Outcome = "FAILED"
)

// Valid reports whether o is DELIVERED or FAILED.
func (o Outcome) Valid() bool { return o == OutcomeDelivered || o == OutcomeFailed }

// MaxFailureReason is the longest accepted failure reason in characters.
const MaxFailureReason = 200

// Stop is one stop of a route: a 1-based number and the packages delivered there.
type Stop struct {
	No              int
	TrackingNumbers []string
}

// Attempt is one recorded delivery attempt of one package.
type Attempt struct {
	TrackingNumber string
	Outcome        Outcome
	FailureReason  string
	At             time.Time
}

// Route is the aggregate root.
type Route struct {
	id       ID
	waveID   wave.ID
	dspID    partner.ID
	station  string
	state    State
	stops    []Stop
	attempts []Attempt
	version  int64
}

// Snapshot is the persisted form of a route. Restore validates it.
type Snapshot struct {
	ID            ID
	WaveID        wave.ID
	DSPID         partner.ID
	StationNodeID string
	State         State
	Stops         []Stop
	Attempts      []Attempt
	Version       int64
}

// Planning is what Plan reads of the other aggregates and of the station: the
// wave and the partner (read only) and the packages other routes of the wave
// already hold.
type Planning struct {
	Wave    *wave.DeliveryWave
	Partner *partner.DeliveryServicePartner
	Routed  map[string]bool
}

// Plan creates a PLANNED route at version 1 and raises RoutePlanned.
func Plan(id ID, stops []Stop, pl Planning, now time.Time) (*Route, []shared.Event, error) {
	if !shared.ValidID(string(id)) || pl.Wave == nil || pl.Partner == nil {
		return nil, nil, shared.Invalid("routeId, the wave and the partner are required")
	}
	if err := checkStops(stops); err != nil {
		return nil, nil, shared.Invalid("%v", err)
	}
	if err := checkPlanning(stops, pl); err != nil {
		return nil, nil, err
	}
	r := &Route{
		id: id, waveID: pl.Wave.ID(), dspID: pl.Partner.ID(), station: pl.Wave.StationNodeID(),
		state: StatePlanned, stops: cloneStops(stops), version: 1,
	}
	ev := RoutePlanned{
		Header: shared.Header{Version: r.version, At: shared.UTC(now)}, RouteID: r.id, WaveID: r.waveID, DSPID: r.dspID, StopCount: len(r.stops),
	}
	return r, []shared.Event{ev}, nil
}

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

func cloneStops(in []Stop) []Stop {
	out := make([]Stop, len(in))
	for i, s := range in {
		out[i] = Stop{No: s.No, TrackingNumbers: slices.Clone(s.TrackingNumbers)}
	}
	return out
}

// Start moves PLANNED to OUT_FOR_DELIVERY and raises one PackageOutForDelivery
// per tracking number, in stop order.
func (r *Route) Start(now time.Time) ([]shared.Event, error) {
	if r.state != StatePlanned {
		return nil, fmt.Errorf("%w: StartRoute needs route %s PLANNED but it is %s", shared.ErrIllegalTransition, r.id, r.state)
	}
	r.state = StateOutForDelivery
	r.version++
	var events []shared.Event
	for _, s := range r.stops {
		for _, tn := range s.TrackingNumbers {
			events = append(events, PackageOutForDelivery{
				Header: shared.Header{Version: r.version, At: shared.UTC(now)}, RouteID: r.id, TrackingNumber: tn,
				StationNodeID: r.station, At: shared.UTC(now),
			})
		}
	}
	return events, nil
}

// RecordAttempt records one delivery attempt of one package and raises
// DeliveryAttempted. at is the instant the driver reports, now the instant of
// the change.
func (r *Route) RecordAttempt(trackingNumber string, outcome Outcome, failureReason string, at, now time.Time) ([]shared.Event, error) {
	if err := checkAttempt(trackingNumber, outcome, failureReason, at); err != nil {
		return nil, shared.Invalid("%v", err)
	}
	if r.state != StateOutForDelivery {
		return nil, fmt.Errorf("%w: route %s is %s", ErrRouteNotStarted, r.id, r.state)
	}
	if !r.HasPackage(trackingNumber) {
		return nil, fmt.Errorf("%w: %s on route %s", ErrPackageNotOnRoute, trackingNumber, r.id)
	}
	if prev, ok := r.AttemptOf(trackingNumber); ok {
		if prev.Outcome == OutcomeDelivered {
			return nil, fmt.Errorf("%w: %s", ErrAlreadyDelivered, trackingNumber)
		}
		return nil, fmt.Errorf("%w: %s on route %s", ErrAttemptAlreadyRecorded, trackingNumber, r.id)
	}
	a := Attempt{TrackingNumber: trackingNumber, Outcome: outcome, FailureReason: failureReason, At: shared.UTC(at)}
	r.attempts = append(r.attempts, a)
	r.version++
	ev := DeliveryAttempted{
		Header: shared.Header{Version: r.version, At: shared.UTC(now)}, RouteID: r.id, TrackingNumber: trackingNumber,
		Outcome: outcome, FailureReason: failureReason, At: a.At,
	}
	return []shared.Event{ev}, nil
}

func checkAttempt(trackingNumber string, outcome Outcome, reason string, at time.Time) error {
	if !shared.ValidID(trackingNumber) {
		return fmt.Errorf("trackingNumber %q is not a valid id", trackingNumber)
	}
	if !outcome.Valid() {
		return fmt.Errorf("outcome %q is not DELIVERED or FAILED", outcome)
	}
	if at.IsZero() {
		return errors.New("the attempt instant is required")
	}
	if outcome == OutcomeDelivered && reason != "" {
		return errors.New("failureReason is only for a FAILED attempt")
	}
	if outcome == OutcomeFailed && !shared.ValidTextN(reason, MaxFailureReason) {
		return fmt.Errorf("a FAILED attempt needs a failureReason of 1 to %d characters, trimmed", MaxFailureReason)
	}
	return nil
}

// Complete moves OUT_FOR_DELIVERY to COMPLETED once every package has an
// attempt (ErrRouteIncomplete otherwise). It raises no event.
func (r *Route) Complete() error {
	if r.state != StateOutForDelivery {
		return fmt.Errorf("%w: CompleteRoute needs route %s OUT_FOR_DELIVERY but it is %s", shared.ErrIllegalTransition, r.id, r.state)
	}
	if !r.AllAttempted() {
		return fmt.Errorf("%w: route %s has %d of %d packages attempted", ErrRouteIncomplete, r.id, len(r.attempts), len(r.Packages()))
	}
	r.state = StateCompleted
	r.version++
	return nil
}

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

// ID returns the route id.
func (r *Route) ID() ID { return r.id }

// WaveID returns the wave the route runs in.
func (r *Route) WaveID() wave.ID { return r.waveID }

// DSPID returns the partner running the route.
func (r *Route) DSPID() partner.ID { return r.dspID }

// StationNodeID returns the station, copied from the wave.
func (r *Route) StationNodeID() string { return r.station }

// State returns the lifecycle state.
func (r *Route) State() State { return r.state }

// Version returns the aggregate version.
func (r *Route) Version() int64 { return r.version }

// Stops returns a copy of the stops.
func (r *Route) Stops() []Stop { return cloneStops(r.stops) }

// Attempts returns a copy of the attempts in recording order.
func (r *Route) Attempts() []Attempt { return slices.Clone(r.attempts) }

// Snapshot returns the persisted form.
func (r *Route) Snapshot() Snapshot {
	return Snapshot{
		ID: r.id, WaveID: r.waveID, DSPID: r.dspID, StationNodeID: r.station, State: r.state, Stops: r.Stops(),
		Attempts: r.Attempts(), Version: r.version,
	}
}
