package deliverypoint

import (
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/claudioed/last-mile-delivery/internal/domain/route"
	"github.com/claudioed/last-mile-delivery/internal/domain/shared"
)

// Observation is one recorded delivery attempt seen from the point: the route
// that made the visit, the package, the outcome with its optional detail, and
// the instant the driver reports.
type Observation struct {
	RouteID        string
	TrackingNumber string
	Outcome        route.Outcome
	Detail         route.Detail
	At             time.Time
}

func (o Observation) check() error {
	if !shared.ValidID(o.RouteID) || !shared.ValidID(o.TrackingNumber) {
		return errors.New("routeId and trackingNumber must be valid ids")
	}
	if !o.Outcome.Valid() || !o.Detail.Valid(o.Outcome) {
		return fmt.Errorf("outcome %q with detail %q is not a valid attempt", o.Outcome, o.Detail)
	}
	if o.At.IsZero() {
		return errors.New("the attempt instant is required")
	}
	return nil
}

// counted returns the strike category of a failed attempt; only NOT_HOME and
// ACCESS_PROBLEM count (OTHER and "not reported" never do).
func (o Observation) counted() (Category, bool) {
	if o.Outcome != route.OutcomeFailed {
		return "", false
	}
	switch o.Detail {
	case route.DetailNotHome:
		return CategoryNotHome, true
	case route.DetailAccessProblem:
		return CategoryAccessProblem, true
	}
	return "", false
}

// Observe folds one attempt into the profile and raises DeliveryPointFlagged
// when an open flag reaches packages that were not told yet (ADR 0014, steps 1
// to 6). now is the instant of the change; the attempt instant is o.At. An
// invalid observation is shared.ErrValidation and changes nothing.
func (p *Profile) Observe(o Observation, now time.Time) ([]shared.Event, error) {
	if err := o.check(); err != nil {
		return nil, shared.Invalid("%v", err)
	}
	at := shared.UTC(o.At)
	ref := later(at, p.lastAttemptAt)
	p.expire(ref)
	p.tally(o)
	if c, ok := o.counted(); ok && !p.hasStrike(o.RouteID) {
		p.addStrike(Strike{RouteID: o.RouteID, TrackingNumber: o.TrackingNumber, Category: c, At: at})
	}
	if o.Outcome == route.OutcomeDelivered && !p.hasStrike(o.RouteID) {
		p.strikes, p.flaggedAt = nil, time.Time{}
	}
	p.lastAttemptAt = ref
	p.version++
	return p.flag(at, shared.UTC(now)), nil
}

func later(a, b time.Time) time.Time {
	if b.After(a) {
		return b
	}
	return a
}

// expire drops the strikes older than the window and closes the flag when fewer
// than FlagAfterStrikes remain (step 1).
func (p *Profile) expire(ref time.Time) {
	cutoff := ref.Add(-StrikeWindow)
	p.strikes = slices.DeleteFunc(p.strikes, func(s Strike) bool { return s.At.Before(cutoff) })
	if len(p.strikes) < FlagAfterStrikes {
		p.flaggedAt = time.Time{}
	}
}

// tally counts the attempt (step 2). Counters saturate at shared.MaxInt32.
func (p *Profile) tally(o Observation) {
	p.attempts = bump(p.attempts)
	switch {
	case o.Outcome == route.OutcomeDelivered:
		p.delivered = bump(p.delivered)
		if o.Detail == route.DetailSafePlaceUsed {
			p.safePlaceUsed = bump(p.safePlaceUsed)
		}
	case o.Detail == route.DetailNotHome:
		p.failedNotHome = bump(p.failedNotHome)
	case o.Detail == route.DetailAccessProblem:
		p.failedAccess = bump(p.failedAccess)
	default:
		p.failedOther = bump(p.failedOther)
	}
}

func bump(n int) int {
	if n >= shared.MaxInt32 {
		return shared.MaxInt32
	}
	return n + 1
}

func (p *Profile) hasStrike(routeID string) bool {
	return slices.ContainsFunc(p.strikes, func(s Strike) bool { return s.RouteID == routeID })
}

// addStrike appends a strike and keeps the newest MaxStrikes (step 3).
func (p *Profile) addStrike(s Strike) {
	p.strikes = append(p.strikes, s)
	if over := len(p.strikes) - MaxStrikes; over > 0 {
		p.strikes = slices.Clone(p.strikes[over:])
	}
}

// flag opens the flag while FlagAfterStrikes strikes are open and raises one
// event for the packages that were not told yet (step 6).
func (p *Profile) flag(at, now time.Time) []shared.Event {
	if len(p.strikes) < FlagAfterStrikes {
		return nil
	}
	if p.flaggedAt.IsZero() {
		p.flaggedAt = at
	}
	fresh := p.untold()
	if len(fresh) == 0 {
		return nil
	}
	for i := range p.strikes {
		if slices.Contains(fresh, p.strikes[i].TrackingNumber) {
			p.strikes[i].Notified = true
		}
	}
	return []shared.Event{DeliveryPointFlagged{
		Header: shared.Header{Version: p.version, At: now}, StopKey: p.stopKey, Reason: p.reason(),
		Failures: len(p.strikes), TrackingNumbers: fresh, FlaggedAt: at,
	}}
}

// untold returns the distinct tracking numbers of the strikes not notified yet,
// leaving out a package that already has a notified strike.
func (p *Profile) untold() []string {
	told := map[string]bool{}
	for _, s := range p.strikes {
		if s.Notified {
			told[s.TrackingNumber] = true
		}
	}
	var fresh []string
	for _, s := range p.strikes {
		if !s.Notified && !told[s.TrackingNumber] && !slices.Contains(fresh, s.TrackingNumber) {
			fresh = append(fresh, s.TrackingNumber)
		}
	}
	return fresh
}
