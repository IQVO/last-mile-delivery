// Package memory holds in-memory TEST DOUBLES of the application's out ports.
// They are for unit, BDD and contract tests only: cmd/** never imports this
// package (enforced by internal/architecture) because the service runs on real
// Postgres and Kafka.
//
// Every repository round-trips its aggregate through Snapshot and Restore, so a
// loaded aggregate never aliases stored state, a corrupt row can not become a
// live aggregate and the doubles keep the persistence contract of the real
// adapters: a miss is ports.ErrNotFound, a stale or duplicate save is
// ports.ErrConcurrentUpdate.
//
// One Store backs every double. Its UnitOfWork is atomic: it holds the store for
// the whole unit and restores the state taken at the start when the unit fails or
// panics, outbox and inbox included.
package memory

import (
	"context"
	"sync"

	"github.com/claudioed/last-mile-delivery/internal/application/views"
	"github.com/claudioed/last-mile-delivery/internal/domain/history"
	"github.com/claudioed/last-mile-delivery/internal/domain/partner"
	"github.com/claudioed/last-mile-delivery/internal/domain/route"
	"github.com/claudioed/last-mile-delivery/internal/domain/shared"
	"github.com/claudioed/last-mile-delivery/internal/domain/wave"
)

type inboundKey struct{ load, tracking string }

// state is everything a unit of work can change. Stored snapshots are replaced,
// never edited in place, so copying the maps is enough.
type state struct {
	waves     map[wave.ID]wave.Snapshot
	routes    map[route.ID]route.Snapshot
	partners  map[partner.ID]partner.Snapshot
	histories map[string]history.Snapshot
	stations  map[string]views.Station
	areas     map[string]views.ServiceArea
	stops     map[string]views.PackageStop
	inbound   map[inboundKey]views.InboundPackage
	inbox     map[string]string
	outbox    []shared.Event
}

func newState() state {
	return state{
		waves: map[wave.ID]wave.Snapshot{}, routes: map[route.ID]route.Snapshot{}, partners: map[partner.ID]partner.Snapshot{},
		histories: map[string]history.Snapshot{}, stations: map[string]views.Station{}, areas: map[string]views.ServiceArea{},
		stops: map[string]views.PackageStop{}, inbound: map[inboundKey]views.InboundPackage{}, inbox: map[string]string{},
	}
}

func cloneMap[K comparable, V any](m map[K]V) map[K]V {
	out := make(map[K]V, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func (st state) clone() state {
	return state{
		waves: cloneMap(st.waves), routes: cloneMap(st.routes), partners: cloneMap(st.partners), histories: cloneMap(st.histories),
		stations: cloneMap(st.stations), areas: cloneMap(st.areas), stops: cloneMap(st.stops), inbound: cloneMap(st.inbound),
		inbox: cloneMap(st.inbox), outbox: append([]shared.Event(nil), st.outbox...),
	}
}

// Store is the shared state of the in-memory doubles.
type Store struct {
	mu sync.Mutex
	st state
}

// NewStore returns an empty store.
func NewStore() *Store { return &Store{st: newState()} }

// txKey marks a ctx that carries the open unit of work of one store.
type txKey struct{ store *Store }

// enter locks the store for one repository call, unless ctx already carries the
// unit of work that holds it. The returned func releases the lock.
func (s *Store) enter(ctx context.Context) func() {
	if ctx.Value(txKey{s}) != nil {
		return func() {}
	}
	s.mu.Lock()
	return s.mu.Unlock
}

// Waves returns the WaveRepository double.
func (s *Store) Waves() *WaveRepository { return &WaveRepository{s} }

// Routes returns the RouteRepository double.
func (s *Store) Routes() *RouteRepository { return &RouteRepository{s} }

// Partners returns the PartnerRepository double.
func (s *Store) Partners() *PartnerRepository { return &PartnerRepository{s} }

// Histories returns the HistoryRepository double.
func (s *Store) Histories() *HistoryRepository { return &HistoryRepository{s} }

// Stations returns the StationRepository double.
func (s *Store) Stations() *StationRepository { return &StationRepository{s} }

// ServiceAreas returns the ServiceAreaRepository double.
func (s *Store) ServiceAreas() *ServiceAreaRepository { return &ServiceAreaRepository{s} }

// PackageStops returns the PackageStopRepository double.
func (s *Store) PackageStops() *PackageStopRepository { return &PackageStopRepository{s} }

// Inbound returns the InboundPackageRepository double.
func (s *Store) Inbound() *InboundPackageRepository { return &InboundPackageRepository{s} }

// Inbox returns the Inbox double.
func (s *Store) Inbox() *Inbox { return &Inbox{s} }

// Outbox returns the EventOutbox double.
func (s *Store) Outbox() *Outbox { return &Outbox{s} }

// UnitOfWork returns the UnitOfWork double.
func (s *Store) UnitOfWork() *UnitOfWork { return &UnitOfWork{s} }

// Counts is how many rows each table of the store holds.
type Counts struct {
	Waves, Routes, Partners, Histories, Stations, ServiceAreas, PackageStops, InboundPackages, ProcessedEvents, OutboxEvents int
	// VersionSum is the sum of every aggregate version: any saved change moves it.
	VersionSum int64
}

// Counts returns the size of every table and the sum of the aggregate versions,
// so a test can prove that a failed use case stored and changed nothing.
func (s *Store) Counts() Counts {
	s.mu.Lock()
	defer s.mu.Unlock()
	var versions int64
	for _, w := range s.st.waves {
		versions += w.Version
	}
	for _, r := range s.st.routes {
		versions += r.Version
	}
	for _, p := range s.st.partners {
		versions += p.Version
	}
	for _, h := range s.st.histories {
		versions += h.Version
	}
	return Counts{
		Waves: len(s.st.waves), Routes: len(s.st.routes), Partners: len(s.st.partners), Histories: len(s.st.histories),
		Stations: len(s.st.stations), ServiceAreas: len(s.st.areas), PackageStops: len(s.st.stops),
		InboundPackages: len(s.st.inbound), ProcessedEvents: len(s.st.inbox), OutboxEvents: len(s.st.outbox), VersionSum: versions,
	}
}
