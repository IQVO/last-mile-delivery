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
	if c, ok := o.counted(); ok {
		p.strike(o, c, at)
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
	cutoff := ref.AddDate(0, 0, -StrikeWindowDays)
	p.strikes = slices.DeleteFunc(p.strikes, func(s Strike) bool { return s.At.Before(cutoff) })
	if len(p.strikes) < FlagAfterStrikes {
		p.flaggedAt = time.Time{}
	}
}

// tally counts the attempt (step 2). Counters saturate at shared.MaxInt32.
func (p *Profile) tally(o Observation) {
	p.attempts = bump(p.attempts)
	if o.Outcome == route.OutcomeDelivered {
		p.delivered = bump(p.delivered)
		if o.Detail == route.DetailSafePlaceUsed {
			p.safePlaceUsed = bump(p.safePlaceUsed)
		}
		return
	}
	switch o.Detail {
	case route.DetailNotHome:
		p.failedNotHome = bump(p.failedNotHome)
	case route.DetailAccessProblem:
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

// strike records a counted failure (step 3, ADR 0018): the first one of a route
// opens a strike and the ring keeps the newest MaxStrikes; a later one of the
// same route joins that strike, so a visit names every package that failed in it.
func (p *Profile) strike(o Observation, c Category, at time.Time) {
	i := slices.IndexFunc(p.strikes, func(s Strike) bool { return s.RouteID == o.RouteID })
	if i < 0 {
		p.strikes = append(p.strikes, Strike{RouteID: o.RouteID, TrackingNumbers: []string{o.TrackingNumber}, Category: c, At: at})
		if len(p.strikes) > MaxStrikes {
			p.strikes = slices.Clone(p.strikes[1:])
		}
		return
	}
	if k := &p.strikes[i]; len(k.TrackingNumbers) < MaxStrikePackages && !slices.Contains(k.TrackingNumbers, o.TrackingNumber) {
		k.TrackingNumbers = append(k.TrackingNumbers, o.TrackingNumber)
	}
}

// flag opens the flag while FlagAfterStrikes strikes are open and raises the
// events for the packages that were not told yet (step 6): one event, or several
// when more than MaxEventPackages packages are new.
func (p *Profile) flag(at, now time.Time) []shared.Event {
	if len(p.strikes) < FlagAfterStrikes {
		return nil
	}
	if p.flaggedAt.IsZero() {
		p.flaggedAt = at
	}
	fresh := p.untold()
	p.markTold(fresh)
	var events []shared.Event
	for tns := range slices.Chunk(fresh, MaxEventPackages) {
		events = append(events, DeliveryPointFlagged{
			Header: shared.Header{Version: p.version, At: now}, StopKey: p.stopKey, Reason: p.reason(),
			Failures: len(p.strikes), TrackingNumbers: slices.Clone(tns), FlaggedAt: at,
		})
	}
	return events
}

// untold returns the distinct packages of the open strikes that no event carried
// yet, in the order the strikes and the visits reported them.
func (p *Profile) untold() []string {
	var fresh []string
	for _, s := range p.strikes {
		for _, tn := range s.TrackingNumbers {
			if !p.told(tn) && !slices.Contains(fresh, tn) {
				fresh = append(fresh, tn)
			}
		}
	}
	return fresh
}

// told reports whether an open strike records that an event carried tn.
func (p *Profile) told(tn string) bool {
	return slices.ContainsFunc(p.strikes, func(s Strike) bool { return slices.Contains(s.Told, tn) })
}

// markTold records fresh as told on every strike that names it, and keeps a
// package told on every strike that names it, so that it is never told again
// while any strike of it stays open.
func (p *Profile) markTold(fresh []string) {
	for i := range p.strikes {
		s := &p.strikes[i]
		for _, tn := range s.TrackingNumbers {
			if !slices.Contains(s.Told, tn) && (slices.Contains(fresh, tn) || p.told(tn)) {
				s.Told = append(s.Told, tn)
			}
		}
	}
}
