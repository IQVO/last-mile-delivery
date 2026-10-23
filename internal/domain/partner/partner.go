// Package partner is the DeliveryServicePartner aggregate: an independent
// delivery business contracted to a station, with its vans.
//
// States: ACTIVE, INACTIVE; transition ACTIVE -> INACTIVE (DeactivatePartner,
// Gap Register G34). Invariant: van capacity > 0 (ErrInvalidVanCapacity). The
// aggregate raises no event (model: events []).
package partner

import (
	"errors"
	"fmt"

	"github.com/claudioed/last-mile-delivery/internal/domain/shared"
)

// ErrInvalidVanCapacity: van capacity must be greater than zero (422).
var ErrInvalidVanCapacity = errors.New("invalid van capacity")

// ID identifies a partner (dspId).
type ID string

// State is the lifecycle state of a partner.
type State string

// The two partner states of the model.
const (
	StateActive   State = "ACTIVE"
	StateInactive State = "INACTIVE"
)

// MaxVanCapacity is the Postgres integer ceiling for a van capacity.
const MaxVanCapacity = 2147483647

// DeliveryServicePartner is the aggregate root.
type DeliveryServicePartner struct {
	id          ID
	station     string
	vanCapacity int
	state       State
	version     int64
}

// Snapshot is the persisted form of a partner. Restore validates it.
type Snapshot struct {
	ID            ID
	StationNodeID string
	VanCapacity   int
	State         State
	Version       int64
}

// NewPartner is the input of Contract.
type NewPartner struct {
	ID            ID
	StationNodeID string
	VanCapacity   int
}

func check(id ID, station string, capacity int) error {
	if !shared.ValidID(string(id)) || !shared.ValidID(station) {
		return shared.Invalid("dspId %q and stationNodeId %q must be valid ids", id, station)
	}
	if capacity < 1 || capacity > MaxVanCapacity {
		return fmt.Errorf("%w: must be between 1 and %d, got %d", ErrInvalidVanCapacity, MaxVanCapacity, capacity)
	}
	return nil
}

// Contract creates an ACTIVE partner at version 1.
func Contract(in NewPartner) (*DeliveryServicePartner, error) {
	if err := check(in.ID, in.StationNodeID, in.VanCapacity); err != nil {
		return nil, err
	}
	return &DeliveryServicePartner{id: in.ID, station: in.StationNodeID, vanCapacity: in.VanCapacity, state: StateActive, version: 1}, nil
}

// Restore rebuilds a persisted partner, validating every invariant.
func Restore(s Snapshot) (*DeliveryServicePartner, error) {
	if err := check(s.ID, s.StationNodeID, s.VanCapacity); err != nil {
		return nil, shared.Corrupt("partner %q: %v", s.ID, err)
	}
	if s.State != StateActive && s.State != StateInactive {
		return nil, shared.Corrupt("partner %q: state %q is not ACTIVE or INACTIVE", s.ID, s.State)
	}
	if s.Version < 1 {
		return nil, shared.Corrupt("partner %q: a version of at least 1 is required", s.ID)
	}
	return &DeliveryServicePartner{id: s.ID, station: s.StationNodeID, vanCapacity: s.VanCapacity, state: s.State, version: s.Version}, nil
}

// Deactivate moves ACTIVE to INACTIVE.
func (p *DeliveryServicePartner) Deactivate() error {
	if p.state != StateActive {
		return fmt.Errorf("%w: partner %s is %s", shared.ErrIllegalTransition, p.id, p.state)
	}
	p.state = StateInactive
	p.version++
	return nil
}

// ID returns the partner id.
func (p *DeliveryServicePartner) ID() ID { return p.id }

// StationNodeID returns the station the partner is contracted to.
func (p *DeliveryServicePartner) StationNodeID() string { return p.station }

// VanCapacity returns the maximum stops per route.
func (p *DeliveryServicePartner) VanCapacity() int { return p.vanCapacity }

// State returns ACTIVE or INACTIVE.
func (p *DeliveryServicePartner) State() State { return p.state }

// Active reports whether the partner is ACTIVE.
func (p *DeliveryServicePartner) Active() bool { return p.state == StateActive }

// Version returns the aggregate version.
func (p *DeliveryServicePartner) Version() int64 { return p.version }

// Snapshot returns the persisted form.
func (p *DeliveryServicePartner) Snapshot() Snapshot {
	return Snapshot{ID: p.id, StationNodeID: p.station, VanCapacity: p.vanCapacity, State: p.state, Version: p.version}
}
