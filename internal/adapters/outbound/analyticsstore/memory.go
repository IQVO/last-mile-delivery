package analyticsstore

import (
	"context"
	"fmt"
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
	etas       map[string]map[string]memEta // routeId -> trackingNumber -> window
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

type memEta struct{ start, end time.Time }

type memReturn struct {
	tracking string
	at       time.Time
}

// NewMemoryStore returns an empty twin.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		processed: map[string]time.Time{}, waves: map[string]memWave{}, inductions: map[string]int{},
		cancelled: map[string]bool{}, costs: map[string]memCost{}, plans: map[string]memPlan{},
		packages: map[string]map[string]string{}, etas: map[string]map[string]memEta{},
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
	case report.KindStopEtaWindowAssigned:
		m.assignEta(e)
	default: // report.KindReturnInitiated: Validate admitted only the known kinds
		m.returns = append(m.returns, memReturn{tracking: e.TrackingNumber, at: e.OccurredAt.UTC()})
	}
}

// assignEta records the ETA window of each package of the stop; the first row
// of a (route, package) stays.
func (m *MemoryStore) assignEta(e report.Event) {
	pkgs := m.etas[e.RouteID]
	if pkgs == nil {
		pkgs = map[string]memEta{}
		m.etas[e.RouteID] = pkgs
	}
	for _, tn := range e.TrackingNumbers {
		if _, ok := pkgs[tn]; !ok {
			pkgs[tn] = memEta{start: e.EtaStart.UTC(), end: e.EtaEnd.UTC()}
		}
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
