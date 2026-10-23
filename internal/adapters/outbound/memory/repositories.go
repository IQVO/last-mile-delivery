package memory

import (
	"context"
	"sort"

	"github.com/claudioed/last-mile-delivery/internal/application/ports"
	"github.com/claudioed/last-mile-delivery/internal/application/repository"
	"github.com/claudioed/last-mile-delivery/internal/application/views"
	"github.com/claudioed/last-mile-delivery/internal/domain/history"
	"github.com/claudioed/last-mile-delivery/internal/domain/partner"
	"github.com/claudioed/last-mile-delivery/internal/domain/route"
	"github.com/claudioed/last-mile-delivery/internal/domain/wave"
)

// checkVersion applies the optimistic-concurrency rule of the ports package:
// a create (version 1) expects the id to be absent, an update expects the
// stored version to be version-1.
func checkVersion(exists bool, stored, version int64) error {
	if version == 1 {
		if exists {
			return ports.ErrConcurrentUpdate
		}
		return nil
	}
	if !exists || stored != version-1 {
		return ports.ErrConcurrentUpdate
	}
	return nil
}

// WaveRepository is the in-memory ports.WaveRepository.
type WaveRepository struct{ s *Store }

// Get returns the wave or ports.ErrNotFound.
func (r *WaveRepository) Get(ctx context.Context, id wave.ID) (*wave.DeliveryWave, error) {
	defer r.s.enter(ctx)()
	snap, ok := r.s.st.waves[id]
	if !ok {
		return nil, ports.ErrNotFound
	}
	return wave.Restore(snap)
}

// List returns the matching waves ordered by cutoff then id.
func (r *WaveRepository) List(ctx context.Context, f repository.WaveFilter) ([]*wave.DeliveryWave, error) {
	defer r.s.enter(ctx)()
	var snaps []wave.Snapshot
	for _, s := range r.s.st.waves {
		if matches(s, f) {
			snaps = append(snaps, s)
		}
	}
	sort.Slice(snaps, func(i, j int) bool {
		if !snaps[i].InductCutoffAt.Equal(snaps[j].InductCutoffAt) {
			return snaps[i].InductCutoffAt.Before(snaps[j].InductCutoffAt)
		}
		return snaps[i].ID < snaps[j].ID
	})
	out := make([]*wave.DeliveryWave, 0, len(snaps))
	for _, s := range snaps {
		w, err := wave.Restore(s)
		if err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, nil
}

func matches(s wave.Snapshot, f repository.WaveFilter) bool {
	return (f.StationNodeID == "" || s.StationNodeID == f.StationNodeID) &&
		(f.ServiceDate == "" || s.ServiceDate == f.ServiceDate) &&
		(f.State == "" || s.State == f.State) &&
		(f.InductCutoffAfter.IsZero() || s.InductCutoffAt.After(f.InductCutoffAfter))
}

// Save stores the wave under the concurrency rule.
func (r *WaveRepository) Save(ctx context.Context, w *wave.DeliveryWave) error {
	defer r.s.enter(ctx)()
	snap := w.Snapshot()
	old, ok := r.s.st.waves[snap.ID]
	if err := checkVersion(ok, old.Version, snap.Version); err != nil {
		return err
	}
	r.s.st.waves[snap.ID] = snap
	return nil
}

// RouteRepository is the in-memory ports.RouteRepository.
type RouteRepository struct{ s *Store }

// Get returns the route or ports.ErrNotFound.
func (r *RouteRepository) Get(ctx context.Context, id route.ID) (*route.Route, error) {
	defer r.s.enter(ctx)()
	snap, ok := r.s.st.routes[id]
	if !ok {
		return nil, ports.ErrNotFound
	}
	return route.Restore(snap)
}

// PackagesInWave returns every tracking number of every route of the wave.
func (r *RouteRepository) PackagesInWave(ctx context.Context, id wave.ID) ([]string, error) {
	defer r.s.enter(ctx)()
	out := []string{}
	for _, s := range r.s.st.routes {
		if s.WaveID != id {
			continue
		}
		for _, st := range s.Stops {
			out = append(out, st.TrackingNumbers...)
		}
	}
	sort.Strings(out)
	return out, nil
}

// CountUncompleted returns how many routes of the wave are not COMPLETED.
func (r *RouteRepository) CountUncompleted(ctx context.Context, id wave.ID) (int, error) {
	defer r.s.enter(ctx)()
	n := 0
	for _, s := range r.s.st.routes {
		if s.WaveID == id && s.State != route.StateCompleted {
			n++
		}
	}
	return n, nil
}

// Save stores the route under the concurrency rule.
func (r *RouteRepository) Save(ctx context.Context, rt *route.Route) error {
	defer r.s.enter(ctx)()
	snap := rt.Snapshot()
	old, ok := r.s.st.routes[snap.ID]
	if err := checkVersion(ok, old.Version, snap.Version); err != nil {
		return err
	}
	r.s.st.routes[snap.ID] = snap
	return nil
}

// PartnerRepository is the in-memory ports.PartnerRepository.
type PartnerRepository struct{ s *Store }

// Get returns the partner or ports.ErrNotFound.
func (r *PartnerRepository) Get(ctx context.Context, id partner.ID) (*partner.DeliveryServicePartner, error) {
	defer r.s.enter(ctx)()
	snap, ok := r.s.st.partners[id]
	if !ok {
		return nil, ports.ErrNotFound
	}
	return partner.Restore(snap)
}

// Save stores the partner under the concurrency rule.
func (r *PartnerRepository) Save(ctx context.Context, p *partner.DeliveryServicePartner) error {
	defer r.s.enter(ctx)()
	snap := p.Snapshot()
	old, ok := r.s.st.partners[snap.ID]
	if err := checkVersion(ok, old.Version, snap.Version); err != nil {
		return err
	}
	r.s.st.partners[snap.ID] = snap
	return nil
}

// HistoryRepository is the in-memory ports.HistoryRepository.
type HistoryRepository struct{ s *Store }

// Get returns the history or ports.ErrNotFound.
func (r *HistoryRepository) Get(ctx context.Context, trackingNumber string) (*history.History, error) {
	defer r.s.enter(ctx)()
	snap, ok := r.s.st.histories[trackingNumber]
	if !ok {
		return nil, ports.ErrNotFound
	}
	return history.Restore(snap)
}

// ListWaiting returns the AT_STATION histories of the station, oldest first.
func (r *HistoryRepository) ListWaiting(ctx context.Context, station string, limit int) ([]*history.History, error) {
	defer r.s.enter(ctx)()
	var snaps []history.Snapshot
	for _, s := range r.s.st.histories {
		if s.StationNodeID == station && s.Status == history.StatusAtStation {
			snaps = append(snaps, s)
		}
	}
	sort.Slice(snaps, func(i, j int) bool {
		if !snaps[i].ArrivedAt.Equal(snaps[j].ArrivedAt) {
			return snaps[i].ArrivedAt.Before(snaps[j].ArrivedAt)
		}
		return snaps[i].TrackingNumber < snaps[j].TrackingNumber
	})
	if limit >= 0 && len(snaps) > limit {
		snaps = snaps[:limit]
	}
	out := make([]*history.History, 0, len(snaps))
	for _, s := range snaps {
		h, err := history.Restore(s)
		if err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, nil
}

// Save stores the history under the concurrency rule.
func (r *HistoryRepository) Save(ctx context.Context, h *history.History) error {
	defer r.s.enter(ctx)()
	snap := h.Snapshot()
	old, ok := r.s.st.histories[snap.TrackingNumber]
	if err := checkVersion(ok, old.Version, snap.Version); err != nil {
		return err
	}
	r.s.st.histories[snap.TrackingNumber] = snap
	return nil
}

// StationRepository is the in-memory ports.StationRepository.
type StationRepository struct{ s *Store }

// Get returns the station or ports.ErrNotFound.
func (r *StationRepository) Get(ctx context.Context, nodeID string) (views.Station, error) {
	defer r.s.enter(ctx)()
	st, ok := r.s.st.stations[nodeID]
	if !ok {
		return views.Station{}, ports.ErrNotFound
	}
	return st, nil
}

// Save stores the station unless the node is already known.
func (r *StationRepository) Save(ctx context.Context, st views.Station) error {
	defer r.s.enter(ctx)()
	if _, ok := r.s.st.stations[st.NodeID]; !ok {
		r.s.st.stations[st.NodeID] = st
	}
	return nil
}

// ServiceAreaRepository is the in-memory ports.ServiceAreaRepository.
type ServiceAreaRepository struct{ s *Store }

// Get returns the assignment or ports.ErrNotFound.
func (r *ServiceAreaRepository) Get(ctx context.Context, prefix string) (views.ServiceArea, error) {
	defer r.s.enter(ctx)()
	a, ok := r.s.st.areas[prefix]
	if !ok {
		return views.ServiceArea{}, ports.ErrNotFound
	}
	return a, nil
}

// Save stores the assignment when its version is higher than the stored one.
func (r *ServiceAreaRepository) Save(ctx context.Context, a views.ServiceArea) (bool, error) {
	defer r.s.enter(ctx)()
	if old, ok := r.s.st.areas[a.PostalPrefix]; ok && old.AssignmentVersion >= a.AssignmentVersion {
		return false, nil
	}
	r.s.st.areas[a.PostalPrefix] = a
	return true, nil
}

// PackageStopRepository is the in-memory ports.PackageStopRepository.
type PackageStopRepository struct{ s *Store }

// Get returns the label data or ports.ErrNotFound.
func (r *PackageStopRepository) Get(ctx context.Context, trackingNumber string) (views.PackageStop, error) {
	defer r.s.enter(ctx)()
	p, ok := r.s.st.stops[trackingNumber]
	if !ok {
		return views.PackageStop{}, ports.ErrNotFound
	}
	return p, nil
}

// Save stores the label data unless the package already has some.
func (r *PackageStopRepository) Save(ctx context.Context, p views.PackageStop) error {
	defer r.s.enter(ctx)()
	if _, ok := r.s.st.stops[p.TrackingNumber]; !ok {
		r.s.st.stops[p.TrackingNumber] = p
	}
	return nil
}

// InboundPackageRepository is the in-memory ports.InboundPackageRepository.
type InboundPackageRepository struct{ s *Store }

// Get returns the arrival or ports.ErrNotFound.
func (r *InboundPackageRepository) Get(ctx context.Context, loadID, trackingNumber string) (views.InboundPackage, error) {
	defer r.s.enter(ctx)()
	p, ok := r.s.st.inbound[inboundKey{loadID, trackingNumber}]
	if !ok {
		return views.InboundPackage{}, ports.ErrNotFound
	}
	return p, nil
}

// Save records the arrival unless the (load, package) pair is already stored.
func (r *InboundPackageRepository) Save(ctx context.Context, p views.InboundPackage) error {
	defer r.s.enter(ctx)()
	k := inboundKey{p.LoadID, p.TrackingNumber}
	if _, ok := r.s.st.inbound[k]; !ok {
		r.s.st.inbound[k] = p
	}
	return nil
}
