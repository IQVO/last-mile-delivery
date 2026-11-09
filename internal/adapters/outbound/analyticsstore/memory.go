package analyticsstore

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/claudioed/last-mile-delivery/internal/analytics/report"
)

// MemoryStore is the in-memory twin of Projection and Reader, for unit tests
// of the consumer and the reports. contract_test.go runs the SAME suite
// against it and against Postgres, so its behaviour cannot drift from the real
// store. The shipped binaries never construct it.
type MemoryStore struct {
	mu         sync.Mutex
	processed  map[string]time.Time
	waves      map[string]memWave
	inductions map[string]int
	cancelled  map[string]bool
	costs      map[string]memCost
	plans      map[string]memPlan
	packages   map[string]map[string]string // routeId -> trackingNumber -> station
	attempts   []memAttempt
	returns    []memReturn
	// Fail, when set, is returned by the next Apply (and cleared), to drive
	// the consumer's transient and rejection paths.
	Fail error
}

type memWave struct {
	station  string
	date     time.Time
	capacity int
}

type memCost struct {
	cents   int64
	at      time.Time
	eventID string
}

type memPlan struct {
	waveID    string
	stopCount int
}

type memAttempt struct {
	eventID, routeID, tracking, outcome string
	at                                  time.Time
}

type memReturn struct {
	tracking string
	at       time.Time
}

// NewMemoryStore returns an empty twin.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		processed: map[string]time.Time{}, waves: map[string]memWave{}, inductions: map[string]int{},
		cancelled: map[string]bool{}, costs: map[string]memCost{}, plans: map[string]memPlan{},
		packages: map[string]map[string]string{},
	}
}

var (
	_ report.Projection = (*MemoryStore)(nil)
	_ report.Reader     = (*MemoryStore)(nil)
)

// Apply implements report.Projection with the Postgres store's semantics: an
// event the schema's CHECKs would refuse is rejected before anything is
// recorded, a replay is a no-op.
func (m *MemoryStore) Apply(_ context.Context, e report.Event) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.Fail != nil {
		err := m.Fail
		m.Fail = nil
		return false, err
	}
	if err := e.Validate(); err != nil {
		return false, fmt.Errorf("%w: %w", report.ErrRejected, err)
	}
	if _, seen := m.processed[e.EventID]; seen {
		return false, nil
	}
	m.processed[e.EventID] = e.At.UTC()
	m.project(e)
	return true, nil
}

// project folds one validated fact in, with the Postgres statements' rules.
func (m *MemoryStore) project(e report.Event) {
	switch e.Kind {
	case report.KindWaveScheduled:
		m.waves[e.WaveID] = memWave{station: e.StationNodeID, date: utcDay(e.ServiceDate), capacity: e.StopCapacity}
	case report.KindWaveCapacityChanged:
		m.inductions[e.WaveID] = max(m.inductions[e.WaveID], e.AssignedStops)
	case report.KindWaveCancelled:
		m.cancelled[e.WaveID] = true
	case report.KindWaveCostDeclared:
		m.declare(e)
	case report.KindRoutePlanned:
		if _, ok := m.plans[e.RouteID]; !ok {
			m.plans[e.RouteID] = memPlan{waveID: e.WaveID, stopCount: e.StopCount}
		}
	case report.KindOutForDelivery:
		m.outForDelivery(e)
	case report.KindDeliveryAttempted:
		m.attempts = append(m.attempts, memAttempt{eventID: e.EventID, routeID: e.RouteID, tracking: e.TrackingNumber,
			outcome: e.Outcome, at: e.OccurredAt.UTC()})
	default: // report.KindReturnInitiated: Validate admitted only the known kinds
		m.returns = append(m.returns, memReturn{tracking: e.TrackingNumber, at: e.OccurredAt.UTC()})
	}
}

// declare keeps the latest declaration: by declaredAt, then event id.
func (m *MemoryStore) declare(e report.Event) {
	c := memCost{cents: e.CostPerStopCents, at: e.OccurredAt.UTC(), eventID: e.EventID}
	if old, ok := m.costs[e.WaveID]; ok && !later(c.at, c.eventID, old.at, old.eventID) {
		return
	}
	m.costs[e.WaveID] = c
}

// outForDelivery records a route's package once.
func (m *MemoryStore) outForDelivery(e report.Event) {
	pkgs := m.packages[e.RouteID]
	if pkgs == nil {
		pkgs = map[string]string{}
		m.packages[e.RouteID] = pkgs
	}
	if _, ok := pkgs[e.TrackingNumber]; !ok {
		pkgs[e.TrackingNumber] = e.StationNodeID
	}
}

// later reports whether (at, id) sorts after (otherAt, otherID).
func later(at time.Time, id string, otherAt time.Time, otherID string) bool {
	if c := at.Compare(otherAt); c != 0 {
		return c > 0
	}
	return id > otherID
}

func utcDay(t time.Time) time.Time { return t.UTC().Truncate(24 * time.Hour) }

func inRange(t time.Time, r report.Range) bool { return t.Compare(r.From) >= 0 && t.Before(r.To) }

// dayStation is the grouping key of every report.
type dayStation struct {
	day     time.Time
	station string
}

// sortedKeys orders the group keys by day, then station.
func sortedKeys[V any](groups map[dayStation]V) []dayStation {
	keys := make([]dayStation, 0, len(groups))
	for k := range groups {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if !keys[i].day.Equal(keys[j].day) {
			return keys[i].day.Before(keys[j].day)
		}
		return keys[i].station < keys[j].station
	})
	return keys
}

// RouteEfficiency implements report.Reader.
func (m *MemoryStore) RouteEfficiency(_ context.Context, r report.Range) ([]report.RouteDay, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	groups := map[dayStation]*report.RouteDay{}
	for routeID, pkgs := range m.packages {
		m.addRoute(groups, r, routeID, pkgs)
	}
	out := []report.RouteDay{}
	for _, k := range sortedKeys(groups) {
		out = append(out, *groups[k])
	}
	return out, nil
}

// addRoute adds one route to its day and station when it is a completed,
// planned route of the range.
func (m *MemoryStore) addRoute(groups map[dayStation]*report.RouteDay, r report.Range, routeID string, pkgs map[string]string) {
	attempted, last := map[string]bool{}, time.Time{}
	for _, a := range m.attempts {
		if _, onRoute := pkgs[a.tracking]; a.routeID == routeID && onRoute {
			attempted[a.tracking] = true
			last = maxTime(last, a.at)
		}
	}
	plan, planned := m.plans[routeID]
	if !planned || !report.IsCompletedRoute(len(pkgs), len(attempted)) || !inRange(last, r) {
		return
	}
	k := dayStation{utcDay(last), minStation(pkgs)}
	d := groups[k]
	if d == nil {
		d = &report.RouteDay{Day: k.day, Station: k.station}
		groups[k] = d
	}
	d.Routes++
	d.Stops += plan.stopCount
	d.Packages += len(pkgs)
	if c, ok := m.costs[plan.waveID]; ok {
		d.CostedStops += plan.stopCount
		d.CostedPackages += len(pkgs)
		d.CostedCents += c.cents * int64(plan.stopCount)
	}
}

func maxTime(a, b time.Time) time.Time {
	if b.After(a) {
		return b
	}
	return a
}

// minStation is the smallest station of a route's packages (they all share one).
func minStation(pkgs map[string]string) string {
	first := true
	var out string
	for _, s := range pkgs {
		if first || s < out {
			out, first = s, false
		}
	}
	return out
}

// stationOf is the station of an attempt's route package, UNKNOWN until its
// out-for-delivery fact is projected.
func (m *MemoryStore) stationOf(a memAttempt) string {
	if s, ok := m.packages[a.routeID][a.tracking]; ok {
		return s
	}
	return report.UnknownStation()
}

// AttemptOutcomes implements report.Reader.
func (m *MemoryStore) AttemptOutcomes(_ context.Context, r report.Range) ([]report.AttemptDay, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	groups := map[dayStation]*report.AttemptDay{}
	at := func(t time.Time, station string) *report.AttemptDay {
		k := dayStation{utcDay(t), station}
		if groups[k] == nil {
			groups[k] = &report.AttemptDay{Day: k.day, Station: k.station}
		}
		return groups[k]
	}
	first, latest, totals := m.perPackage()
	for _, a := range m.attempts {
		if !inRange(a.at, r) {
			continue
		}
		d := at(a.at, m.stationOf(a))
		if a.outcome == report.OutcomeDelivered {
			d.DeliveredAttempts++
		} else {
			d.FailedAttempts++
		}
	}
	for tn, f := range first {
		if inRange(f.at, r) {
			d := at(f.at, m.stationOf(f))
			d.PackagesAttempted++
			d.AttemptsOfPackagesAttempted += totals[tn]
			if f.outcome == report.OutcomeDelivered {
				d.PackagesFirstAttemptSuccess++
			}
		}
	}
	m.addReturns(r, latest, at)
	out := []report.AttemptDay{}
	for _, k := range sortedKeys(groups) {
		out = append(out, *groups[k])
	}
	return out, nil
}

// perPackage is every package's first and latest attempt (by instant, then
// event id) and its attempt count.
func (m *MemoryStore) perPackage() (first, latest map[string]memAttempt, totals map[string]int) {
	first, latest, totals = map[string]memAttempt{}, map[string]memAttempt{}, map[string]int{}
	for _, a := range m.attempts {
		totals[a.tracking]++
		if f, ok := first[a.tracking]; !ok || later(f.at, f.eventID, a.at, a.eventID) {
			first[a.tracking] = a
		}
		if l, ok := latest[a.tracking]; !ok || later(a.at, a.eventID, l.at, l.eventID) {
			latest[a.tracking] = a
		}
	}
	return first, latest, totals
}

// addReturns counts the returns of the range at the station of the package's
// latest attempt (UNKNOWN without one).
func (m *MemoryStore) addReturns(r report.Range, latest map[string]memAttempt, at func(time.Time, string) *report.AttemptDay) {
	for _, ret := range m.returns {
		if !inRange(ret.at, r) {
			continue
		}
		station := report.UnknownStation()
		if l, ok := latest[ret.tracking]; ok {
			station = m.stationOf(l)
		}
		at(ret.at, station).ReturnsInitiated++
	}
}

// WaveUtilisation implements report.Reader.
func (m *MemoryStore) WaveUtilisation(_ context.Context, r report.Range) ([]report.WaveDay, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	groups := map[dayStation]*report.WaveDay{}
	for id, w := range m.waves {
		if !inRange(w.date, r) {
			continue
		}
		k := dayStation{w.date, w.station}
		d := groups[k]
		if d == nil {
			d = &report.WaveDay{Day: k.day, Station: k.station}
			groups[k] = d
		}
		d.WavesScheduled++
		d.StopCapacity += w.capacity
		d.StopsInducted += m.inductions[id]
		if m.cancelled[id] {
			d.WavesCancelled++
		}
	}
	out := []report.WaveDay{}
	for _, k := range sortedKeys(groups) {
		out = append(out, *groups[k])
	}
	return out, nil
}

// LastEventAt implements report.Reader.
func (m *MemoryStore) LastEventAt(context.Context) (*time.Time, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var last *time.Time
	for _, at := range m.processed {
		if last == nil || at.After(*last) {
			a := at
			last = &a
		}
	}
	return last, nil
}

// Ping is the twin's readiness check: always ready.
func (m *MemoryStore) Ping(context.Context) error { return nil }
