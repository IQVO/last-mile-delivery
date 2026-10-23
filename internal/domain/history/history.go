// Package history is the PackageAttemptHistory aggregate (Gap Register G16): one
// record per tracking number that reaches the station. It counts failed delivery
// attempts, remembers whether the package waits for a wave, and makes "a package
// is delivered at most once" hold across routes.
//
// States: INDUCTED (placed in a wave), AT_STATION (waiting for a wave),
// DELIVERED and RETURN_INITIATED (terminal). The third failed attempt
// (MaxFailedAttempts) raises PackageReturnInitiated; a package that finds no
// wave raises PackageInductionDeferred (ADR 0002, A18 and A19).
package history

import (
	"fmt"
	"time"

	"github.com/claudioed/last-mile-delivery/internal/domain/shared"
)

// MaxFailedAttempts is the number of failed attempts after which a package is
// returned to its sender.
const MaxFailedAttempts = 3

// Status is where a package is in its last-mile life.
type Status string

// The four package statuses.
const (
	StatusAtStation       Status = "AT_STATION"
	StatusInducted        Status = "INDUCTED"
	StatusDelivered       Status = "DELIVERED"
	StatusReturnInitiated Status = "RETURN_INITIATED"
)

// Valid reports whether s is one of the four statuses.
func (s Status) Valid() bool {
	switch s {
	case StatusAtStation, StatusInducted, StatusDelivered, StatusReturnInitiated:
		return true
	}
	return false
}

// MaxAssignmentID is the longest accepted assignment id in characters.
const MaxAssignmentID = 100

// History is the aggregate root, keyed by the tracking number.
type History struct {
	trackingNumber string
	station        string
	assignmentID   string
	failed         int
	status         Status
	arrivedAt      time.Time
	version        int64
}

// Snapshot is the persisted form of a history. Restore validates it.
type Snapshot struct {
	TrackingNumber string
	StationNodeID  string
	AssignmentID   string
	FailedAttempts int
	Status         Status
	ArrivedAt      time.Time
	Version        int64
}

// Arrive creates the history of a package that reached the station. placed is
// true when a wave accepted it (status INDUCTED); otherwise it waits (AT_STATION)
// and PackageInductionDeferred is raised. assignmentID may be empty.
func Arrive(trackingNumber, station, assignmentID string, placed bool, at, now time.Time) (*History, []shared.Event, error) {
	if err := check(trackingNumber, station, assignmentID, at); err != nil {
		return nil, nil, shared.Invalid("%v", err)
	}
	h := &History{
		trackingNumber: trackingNumber, station: station, assignmentID: assignmentID,
		status: StatusInducted, arrivedAt: shared.UTC(at), version: 1,
	}
	if placed {
		return h, nil, nil
	}
	h.status = StatusAtStation
	return h, []shared.Event{h.deferred(now)}, nil
}

func check(trackingNumber, station, assignmentID string, at time.Time) error {
	if !shared.ValidID(trackingNumber) {
		return fmt.Errorf("trackingNumber %q is not a valid id", trackingNumber)
	}
	if !shared.ValidID(station) {
		return fmt.Errorf("stationNodeId %q is not a valid id", station)
	}
	if assignmentID != "" && !shared.ValidTextN(assignmentID, MaxAssignmentID) {
		return fmt.Errorf("assignmentId must be at most %d characters, trimmed", MaxAssignmentID)
	}
	if at.IsZero() {
		return fmt.Errorf("the arrival instant is required")
	}
	return nil
}

func (h *History) deferred(now time.Time) PackageInductionDeferred {
	return PackageInductionDeferred{
		Header: shared.Header{Version: h.version, At: shared.UTC(now)}, TrackingNumber: h.trackingNumber, AssignmentID: h.assignmentID,
	}
}

// Restore rebuilds a persisted history, validating every invariant.
func Restore(s Snapshot) (*History, error) {
	if err := check(s.TrackingNumber, s.StationNodeID, s.AssignmentID, s.ArrivedAt); err != nil {
		return nil, shared.Corrupt("history %q: %v", s.TrackingNumber, err)
	}
	if !s.Status.Valid() {
		return nil, shared.Corrupt("history %q: status %q is unknown", s.TrackingNumber, s.Status)
	}
	if s.Version < 1 {
		return nil, shared.Corrupt("history %q: a version of at least 1 is required", s.TrackingNumber)
	}
	if s.FailedAttempts < 0 || s.FailedAttempts > MaxFailedAttempts {
		return nil, shared.Corrupt("history %q: failedAttempts must be between 0 and %d", s.TrackingNumber, MaxFailedAttempts)
	}
	if (s.Status == StatusReturnInitiated) != (s.FailedAttempts == MaxFailedAttempts) {
		return nil, shared.Corrupt("history %q: RETURN_INITIATED exactly when %d attempts failed", s.TrackingNumber, MaxFailedAttempts)
	}
	return &History{
		trackingNumber: s.TrackingNumber, station: s.StationNodeID, assignmentID: s.AssignmentID, failed: s.FailedAttempts,
		status: s.Status, arrivedAt: shared.UTC(s.ArrivedAt), version: s.Version,
	}, nil
}

func (h *History) illegal(command string, from ...Status) error {
	return fmt.Errorf("%w: %s needs %v but package %s is %s", shared.ErrIllegalTransition, command, from, h.trackingNumber, h.status)
}

// Place moves AT_STATION to INDUCTED: a wave accepted the waiting package.
func (h *History) Place() error {
	if h.status != StatusAtStation {
		return h.illegal("Place", StatusAtStation)
	}
	h.status = StatusInducted
	h.version++
	return nil
}

// Unplace moves INDUCTED to AT_STATION because the wave it was in could not take
// it onto another wave (cancel, dispatch leftover), and raises
// PackageInductionDeferred.
func (h *History) Unplace(now time.Time) ([]shared.Event, error) {
	if h.status != StatusInducted {
		return nil, h.illegal("Unplace", StatusInducted)
	}
	h.status = StatusAtStation
	h.version++
	return []shared.Event{h.deferred(now)}, nil
}

// WouldReturn reports whether the next failed attempt is the last one.
func (h *History) WouldReturn() bool { return h.failed+1 >= MaxFailedAttempts }

// RecordFailure counts a failed attempt of an INDUCTED package. The third
// failure ends in RETURN_INITIATED and raises PackageReturnInitiated; any other
// keeps the package INDUCTED when placed is true (another wave took it) or moves
// it to AT_STATION and raises PackageInductionDeferred.
func (h *History) RecordFailure(placed bool, now time.Time) ([]shared.Event, error) {
	if h.status != StatusInducted {
		return nil, h.illegal("RecordFailure", StatusInducted)
	}
	h.failed++
	h.version++
	if h.failed >= MaxFailedAttempts {
		h.status = StatusReturnInitiated
		return []shared.Event{PackageReturnInitiated{
			Header: shared.Header{Version: h.version, At: shared.UTC(now)}, TrackingNumber: h.trackingNumber,
		}}, nil
	}
	if placed {
		return nil, nil
	}
	h.status = StatusAtStation
	return []shared.Event{h.deferred(now)}, nil
}

// RecordDelivered moves INDUCTED to DELIVERED. A package already DELIVERED is
// ErrAlreadyDelivered.
func (h *History) RecordDelivered() error {
	switch h.status {
	case StatusInducted:
		h.status = StatusDelivered
		h.version++
		return nil
	case StatusDelivered:
		return fmt.Errorf("%w: package %s", shared.ErrAlreadyDelivered, h.trackingNumber)
	}
	return h.illegal("RecordDelivered", StatusInducted)
}

// TrackingNumber returns the tracking number.
func (h *History) TrackingNumber() string { return h.trackingNumber }

// StationNodeID returns the station the package is at.
func (h *History) StationNodeID() string { return h.station }

// AssignmentID returns the assignment id from the label (empty when unknown).
func (h *History) AssignmentID() string { return h.assignmentID }

// FailedAttempts returns the number of failed attempts so far.
func (h *History) FailedAttempts() int { return h.failed }

// Status returns the package status.
func (h *History) Status() Status { return h.status }

// ArrivedAt returns when the package first reached the station.
func (h *History) ArrivedAt() time.Time { return h.arrivedAt }

// Version returns the aggregate version.
func (h *History) Version() int64 { return h.version }

// Snapshot returns the persisted form.
func (h *History) Snapshot() Snapshot {
	return Snapshot{
		TrackingNumber: h.trackingNumber, StationNodeID: h.station, AssignmentID: h.assignmentID, FailedAttempts: h.failed,
		Status: h.status, ArrivedAt: h.arrivedAt, Version: h.version,
	}
}
