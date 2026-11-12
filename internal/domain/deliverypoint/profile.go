// Package deliverypoint is the DeliveryPointProfile aggregate: what the delivery
// attempts made at one drop-off point have shown, keyed by the opaque stopKey
// shipping-and-tracking derives at label time (ADR 0014, roadmap C5).
//
// The profile holds only counters and a short ring of strikes (a counted failed
// visit). It never holds an address, a name, a phone or a free text, and it
// influences nothing: no sequence, no ETA, no promise. The only command is
// Observe; the only event is DeliveryPointFlagged. Without an outcome detail from
// the driver client the profile tallies attempts and never counts a strike.
package deliverypoint

import (
	"slices"
	"strings"
	"time"

	"github.com/claudioed/last-mile-delivery/internal/domain/shared"
)

// The thresholds of ADR 0014, by name.
const (
	// FlagAfterStrikes is how many open strikes flag a point.
	FlagAfterStrikes = 2
	// StrikeWindow is how old a strike may be, measured from the newest attempt
	// time seen at the point, before it no longer counts.
	StrikeWindow = 60 * 24 * time.Hour
	// MaxStrikes is the size of the ring; the oldest strike is dropped first.
	MaxStrikes = 5
)

// stopKeyPackagePrefix marks a package that has no address key: it is its own
// stop and never gets a profile.
const stopKeyPackagePrefix = "pkg:"

// Category is the counted kind of a failed visit. It is also the reason a point
// is flagged for.
type Category string

// The only two failures that make a strike. OTHER and "not reported" are
// tallied but never counted.
const (
	CategoryNotHome       Category = "NOT_HOME"
	CategoryAccessProblem Category = "ACCESS_PROBLEM"
)

// Valid reports whether c is a counted category.
func (c Category) Valid() bool { return c == CategoryNotHome || c == CategoryAccessProblem }

// Strike is one counted failed visit: one route, at most one strike per route.
type Strike struct {
	RouteID        string
	TrackingNumber string
	Category       Category
	At             time.Time
	// Notified is true once a DeliveryPointFlagged carried the tracking number.
	Notified bool
}

// Profile is the aggregate root. Its id is the stopKey.
type Profile struct {
	stopKey       string
	attempts      int
	delivered     int
	safePlaceUsed int
	failedNotHome int
	failedAccess  int
	failedOther   int
	strikes       []Strike
	flaggedAt     time.Time
	lastAttemptAt time.Time
	version       int64
}

// Snapshot is the persisted form of a profile. Restore validates it.
type Snapshot struct {
	StopKey       string
	Attempts      int
	Delivered     int
	SafePlaceUsed int
	FailedNotHome int
	FailedAccess  int
	FailedOther   int
	Strikes       []Strike
	// FlaggedAt is zero while no flag is open.
	FlaggedAt     time.Time
	LastAttemptAt time.Time
	Version       int64
}

// validStopKey is the stop key a profile accepts: a valid stop key that is not a
// package-own stop.
func validStopKey(s string) bool {
	return shared.ValidStopKey(s) && !strings.HasPrefix(s, stopKeyPackagePrefix)
}

// New returns an empty profile at version 0. It is stored only after Observe has
// made it version 1.
func New(stopKey string) (*Profile, error) {
	if !validStopKey(stopKey) {
		return nil, shared.Invalid("stopKey %q is not a valid address stop key", stopKey)
	}
	return &Profile{stopKey: stopKey}, nil
}

// StopKey returns the aggregate id.
func (p *Profile) StopKey() string { return p.stopKey }

// Attempts returns how many attempts were observed.
func (p *Profile) Attempts() int { return p.attempts }

// Delivered returns how many observed attempts delivered.
func (p *Profile) Delivered() int { return p.delivered }

// SafePlaceUsed returns how many deliveries were left in a safe place.
func (p *Profile) SafePlaceUsed() int { return p.safePlaceUsed }

// FailedNotHome returns the failures reported as NOT_HOME.
func (p *Profile) FailedNotHome() int { return p.failedNotHome }

// FailedAccess returns the failures reported as ACCESS_PROBLEM.
func (p *Profile) FailedAccess() int { return p.failedAccess }

// FailedOther returns the failures reported as OTHER or not reported.
func (p *Profile) FailedOther() int { return p.failedOther }

// OpenStrikes returns the strikes in the ring. Strikes expire lazily, at the next
// observation, so a point nobody visits again keeps its last count.
func (p *Profile) OpenStrikes() int { return len(p.strikes) }

// Flagged reports whether a flag is open.
func (p *Profile) Flagged() bool { return !p.flaggedAt.IsZero() }

// FlaggedAt returns the instant the flag opened; zero while no flag is open.
func (p *Profile) FlaggedAt() time.Time { return p.flaggedAt }

// FlagReason is ACCESS_PROBLEM when any open strike has it, else NOT_HOME; empty
// while no flag is open.
func (p *Profile) FlagReason() Category {
	if !p.Flagged() {
		return ""
	}
	return p.reason()
}

func (p *Profile) reason() Category {
	for _, s := range p.strikes {
		if s.Category == CategoryAccessProblem {
			return CategoryAccessProblem
		}
	}
	return CategoryNotHome
}

// LastAttemptAt returns the newest attempt time seen at the point.
func (p *Profile) LastAttemptAt() time.Time { return p.lastAttemptAt }

// Version returns the aggregate version.
func (p *Profile) Version() int64 { return p.version }

// Snapshot returns the persisted form; the strikes are copied.
func (p *Profile) Snapshot() Snapshot {
	return Snapshot{
		StopKey: p.stopKey, Attempts: p.attempts, Delivered: p.delivered, SafePlaceUsed: p.safePlaceUsed,
		FailedNotHome: p.failedNotHome, FailedAccess: p.failedAccess, FailedOther: p.failedOther,
		Strikes: cloneStrikes(p.strikes), FlaggedAt: p.flaggedAt, LastAttemptAt: p.lastAttemptAt, Version: p.version,
	}
}

func cloneStrikes(in []Strike) []Strike {
	if len(in) == 0 {
		return nil
	}
	return slices.Clone(in)
}
