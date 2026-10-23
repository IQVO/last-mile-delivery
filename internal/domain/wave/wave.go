// Package wave is the DeliveryWave aggregate: one dispatch wave of one delivery
// station on one service date, with its induct cutoff, delivery window and stop
// capacity.
//
// States: OPEN, INDUCTION_CLOSED, DISPATCHED, COMPLETED, CANCELLED. Transitions:
// OPEN -> INDUCTION_CLOSED (CloseInduction), INDUCTION_CLOSED -> DISPATCHED
// (DispatchWave), DISPATCHED -> COMPLETED (CompleteWave), OPEN -> CANCELLED
// (CancelWave). InductPackage keeps the wave OPEN. Invariants: inductCutoffAt <
// windowStart < windowEnd (ErrInvalidWaveTimes); assigned stops <= stop capacity
// (ErrWaveFull); no package joins after induction closes (ErrInductionClosed).
// Capacity is counted in stops: packages sharing a stopKey take one stop
// (ADR 0002, A4).
package wave

import (
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/claudioed/last-mile-delivery/internal/domain/shared"
)

// The DeliveryWave invariants of the model, by the model's names.
var (
	// ErrInvalidWaveTimes: inductCutoffAt < windowStart < windowEnd (422).
	ErrInvalidWaveTimes = errors.New("invalid wave times")
	// ErrWaveFull: assigned stops must not exceed the stop capacity (409).
	ErrWaveFull = errors.New("wave full")
	// ErrInductionClosed: no package joins after induction closes or after the
	// cutoff (409).
	ErrInductionClosed = errors.New("induction closed")
	// ErrAlreadyInducted: the tracking number is already inducted in this wave.
	// The use case treats it as a no-op (ADR 0002, A5).
	ErrAlreadyInducted = errors.New("already inducted")
)

// ID identifies a wave (waveId).
type ID string

// State is the lifecycle state of a wave.
type State string

// The five wave states of the model.
const (
	StateOpen            State = "OPEN"
	StateInductionClosed State = "INDUCTION_CLOSED"
	StateDispatched      State = "DISPATCHED"
	StateCompleted       State = "COMPLETED"
	StateCancelled       State = "CANCELLED"
)

// Valid reports whether s is one of the five states.
func (s State) Valid() bool {
	switch s {
	case StateOpen, StateInductionClosed, StateDispatched, StateCompleted, StateCancelled:
		return true
	}
	return false
}

// MaxStopCapacity is the Postgres integer ceiling for a stop capacity.
const MaxStopCapacity = 2147483647

// MaxReason is the longest accepted cancel reason in characters.
const MaxReason = 200

// Induction records one package inducted into a wave.
type Induction struct {
	TrackingNumber string
	StopKey        string
	At             time.Time
}

// DeliveryWave is the aggregate root.
type DeliveryWave struct {
	id           ID
	station      string
	serviceDate  string
	cutoff       time.Time
	windowStart  time.Time
	windowEnd    time.Time
	capacity     int
	assigned     int
	inductions   []Induction
	state        State
	cancelReason string
	version      int64
}

// Snapshot is the persisted form of a wave. Restore validates it.
type Snapshot struct {
	ID             ID
	StationNodeID  string
	ServiceDate    string
	InductCutoffAt time.Time
	WindowStart    time.Time
	WindowEnd      time.Time
	StopCapacity   int
	AssignedStops  int
	Inductions     []Induction
	State          State
	CancelReason   string
	Version        int64
}

// NewWave is the input of Schedule.
type NewWave struct {
	ID             ID
	StationNodeID  string
	ServiceDate    string
	InductCutoffAt time.Time
	WindowStart    time.Time
	WindowEnd      time.Time
	StopCapacity   int
}

// Schedule creates an OPEN wave at version 1 and raises DeliveryWaveScheduled.
func Schedule(in NewWave, now time.Time) (*DeliveryWave, []shared.Event, error) {
	if err := checkDefinition(in.ID, in.StationNodeID, in.ServiceDate, in.StopCapacity); err != nil {
		return nil, nil, shared.Invalid("%v", err)
	}
	if err := checkTimes(in.InductCutoffAt, in.WindowStart, in.WindowEnd); err != nil {
		return nil, nil, err
	}
	w := &DeliveryWave{
		id: in.ID, station: in.StationNodeID, serviceDate: in.ServiceDate,
		cutoff: shared.UTC(in.InductCutoffAt), windowStart: shared.UTC(in.WindowStart), windowEnd: shared.UTC(in.WindowEnd),
		capacity: in.StopCapacity, state: StateOpen, version: 1,
	}
	ev := DeliveryWaveScheduled{
		Header: shared.Header{Version: w.version, At: shared.UTC(now)}, WaveID: w.id, StationNodeID: w.station,
		ServiceDate: w.serviceDate, InductCutoffAt: w.cutoff, WindowStart: w.windowStart, WindowEnd: w.windowEnd,
		StopCapacity: w.capacity,
	}
	return w, []shared.Event{ev}, nil
}

func checkDefinition(id ID, station, date string, capacity int) error {
	if !shared.ValidID(string(id)) {
		return fmt.Errorf("waveId %q is not a valid id", id)
	}
	if !shared.ValidID(station) {
		return fmt.Errorf("stationNodeId %q is not a valid id", station)
	}
	if !shared.ValidDate(date) {
		return fmt.Errorf("serviceDate %q is not a YYYY-MM-DD date", date)
	}
	if capacity < 0 || capacity > MaxStopCapacity {
		return fmt.Errorf("stopCapacity must be between 0 and %d", MaxStopCapacity)
	}
	return nil
}

// checkTimes enforces inductCutoffAt < windowStart < windowEnd; a zero instant
// is a value-object error, a wrong order is ErrInvalidWaveTimes.
func checkTimes(cutoff, start, end time.Time) error {
	if cutoff.IsZero() || start.IsZero() || end.IsZero() {
		return shared.Invalid("inductCutoffAt, windowStart and windowEnd are required")
	}
	if !cutoff.Before(start) || !start.Before(end) {
		return fmt.Errorf("%w: need inductCutoffAt < windowStart < windowEnd", ErrInvalidWaveTimes)
	}
	return nil
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
		version: s.Version,
	}, nil
}

func normalise(in []Induction) []Induction {
	if len(in) == 0 {
		return nil
	}
	out := make([]Induction, len(in))
	for i, x := range in {
		out[i] = Induction{TrackingNumber: x.TrackingNumber, StopKey: x.StopKey, At: shared.UTC(x.At)}
	}
	return out
}

func checkSnapshot(s Snapshot) error {
	if err := checkDefinition(s.ID, s.StationNodeID, s.ServiceDate, s.StopCapacity); err != nil {
		return err
	}
	if err := checkTimes(s.InductCutoffAt, s.WindowStart, s.WindowEnd); err != nil {
		return err
	}
	if s.Version < 1 {
		return errors.New("a version of at least 1 is required")
	}
	if !s.State.Valid() {
		return fmt.Errorf("state %q is not a wave state", s.State)
	}
	if (s.State == StateCancelled) != (s.CancelReason != "") {
		return errors.New("a cancelReason is set exactly when the wave is CANCELLED")
	}
	return checkInductions(s)
}

func checkInductions(s Snapshot) error {
	seen := map[string]bool{}
	keys := map[string]bool{}
	for _, in := range s.Inductions {
		if !shared.ValidID(in.TrackingNumber) || !shared.ValidStopKey(in.StopKey) || in.At.IsZero() {
			return errors.New("an induction needs a valid trackingNumber, stopKey and instant")
		}
		if seen[in.TrackingNumber] {
			return fmt.Errorf("trackingNumber %q is inducted twice", in.TrackingNumber)
		}
		seen[in.TrackingNumber] = true
		keys[in.StopKey] = true
	}
	if s.AssignedStops != len(keys) || s.AssignedStops > s.StopCapacity {
		return errors.New("assignedStops must equal the distinct stop keys and not exceed the stopCapacity")
	}
	return nil
}

// Induct places a package of the given stop into the wave. at is the instant
// the package became eligible (it must be before the cutoff), now the instant of
// the change. A package joining a stop already in the wave takes no capacity and
// raises no event; a new stop raises DeliveryWaveCapacityChanged.
func (w *DeliveryWave) Induct(trackingNumber, stopKey string, at, now time.Time) ([]shared.Event, error) {
	if !shared.ValidID(trackingNumber) || !shared.ValidStopKey(stopKey) || at.IsZero() {
		return nil, shared.Invalid("trackingNumber, stopKey and the induction instant must be valid")
	}
	if w.state != StateOpen || !at.Before(w.cutoff) {
		return nil, fmt.Errorf("%w: wave %s is %s, cutoff %s", ErrInductionClosed, w.id, w.state, w.cutoff.Format(time.RFC3339))
	}
	if w.holds(trackingNumber) {
		return nil, fmt.Errorf("%w: %s in wave %s", ErrAlreadyInducted, trackingNumber, w.id)
	}
	newStop := !w.hasStop(stopKey)
	if newStop && w.assigned >= w.capacity {
		return nil, fmt.Errorf("%w: wave %s has %d of %d stops", ErrWaveFull, w.id, w.assigned, w.capacity)
	}
	w.inductions = append(w.inductions, Induction{TrackingNumber: trackingNumber, StopKey: stopKey, At: shared.UTC(now)})
	w.version++
	if !newStop {
		return nil, nil
	}
	w.assigned++
	return []shared.Event{w.capacityChanged(now)}, nil
}

func (w *DeliveryWave) capacityChanged(now time.Time) DeliveryWaveCapacityChanged {
	return DeliveryWaveCapacityChanged{
		Header: shared.Header{Version: w.version, At: shared.UTC(now)}, WaveID: w.id, StopCapacity: w.capacity,
		AssignedStops: w.assigned, RemainingStops: w.capacity - w.assigned,
	}
}

func (w *DeliveryWave) holds(trackingNumber string) bool {
	return slices.ContainsFunc(w.inductions, func(in Induction) bool { return in.TrackingNumber == trackingNumber })
}

func (w *DeliveryWave) hasStop(stopKey string) bool {
	return slices.ContainsFunc(w.inductions, func(in Induction) bool { return in.StopKey == stopKey })
}

// move applies one state transition from one state; every other state is an
// illegal transition. It raises no event.
func (w *DeliveryWave) move(from, to State, command string) error {
	if w.state != from {
		return fmt.Errorf("%w: %s needs %s but wave %s is %s", shared.ErrIllegalTransition, command, from, w.id, w.state)
	}
	w.state = to
	w.version++
	return nil
}

// CloseInduction moves OPEN to INDUCTION_CLOSED. It raises no event.
func (w *DeliveryWave) CloseInduction() error {
	return w.move(StateOpen, StateInductionClosed, "CloseInduction")
}

// Dispatch moves INDUCTION_CLOSED to DISPATCHED. It raises no event.
func (w *DeliveryWave) Dispatch() error {
	return w.move(StateInductionClosed, StateDispatched, "DispatchWave")
}

// Complete moves DISPATCHED to COMPLETED. It raises no event.
func (w *DeliveryWave) Complete() error {
	return w.move(StateDispatched, StateCompleted, "CompleteWave")
}

// Cancel moves OPEN to CANCELLED and raises DeliveryWaveCancelled. The packages
// inducted so far stay recorded; re-inducting them is the use case's policy.
func (w *DeliveryWave) Cancel(reason string, now time.Time) ([]shared.Event, error) {
	if !shared.ValidTextN(reason, MaxReason) {
		return nil, shared.Invalid("reason must be 1 to %d characters, trimmed", MaxReason)
	}
	if err := w.move(StateOpen, StateCancelled, "CancelWave"); err != nil {
		return nil, err
	}
	w.cancelReason = reason
	ev := DeliveryWaveCancelled{Header: shared.Header{Version: w.version, At: shared.UTC(now)}, WaveID: w.id, Reason: reason}
	return []shared.Event{ev}, nil
}

// ID returns the wave id.
func (w *DeliveryWave) ID() ID { return w.id }

// StationNodeID returns the delivery station.
func (w *DeliveryWave) StationNodeID() string { return w.station }

// ServiceDate returns the station's local service date.
func (w *DeliveryWave) ServiceDate() string { return w.serviceDate }

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

// Snapshot returns the persisted form.
func (w *DeliveryWave) Snapshot() Snapshot {
	return Snapshot{
		ID: w.id, StationNodeID: w.station, ServiceDate: w.serviceDate, InductCutoffAt: w.cutoff,
		WindowStart: w.windowStart, WindowEnd: w.windowEnd, StopCapacity: w.capacity, AssignedStops: w.assigned,
		Inductions: w.Inductions(), State: w.state, CancelReason: w.cancelReason, Version: w.version,
	}
}
