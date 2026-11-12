package memory

import (
	"context"

	"github.com/claudioed/last-mile-delivery/internal/application/ports"
	"github.com/claudioed/last-mile-delivery/internal/domain/deliverypoint"
)

// DeliveryPointRepository is the in-memory ports.DeliveryPointRepository.
type DeliveryPointRepository struct{ s *Store }

// Get returns the profile or ports.ErrNotFound.
func (r *DeliveryPointRepository) Get(ctx context.Context, stopKey string) (*deliverypoint.Profile, error) {
	defer r.s.enter(ctx)()
	snap, ok := r.s.st.points[stopKey]
	if !ok {
		return nil, ports.ErrNotFound
	}
	return deliverypoint.Restore(snap)
}

// GetMany returns the profiles of the keys that have one; a miss is absent.
func (r *DeliveryPointRepository) GetMany(ctx context.Context, stopKeys []string) (map[string]*deliverypoint.Profile, error) {
	defer r.s.enter(ctx)()
	out := make(map[string]*deliverypoint.Profile, len(stopKeys))
	for _, k := range stopKeys {
		snap, ok := r.s.st.points[k]
		if !ok {
			continue
		}
		p, err := deliverypoint.Restore(snap)
		if err != nil {
			return nil, err
		}
		out[k] = p
	}
	return out, nil
}

// Save stores the profile under the concurrency rule.
func (r *DeliveryPointRepository) Save(ctx context.Context, p *deliverypoint.Profile) error {
	defer r.s.enter(ctx)()
	snap := p.Snapshot()
	old, ok := r.s.st.points[snap.StopKey]
	if err := checkVersion(ok, old.Version, snap.Version); err != nil {
		return err
	}
	r.s.st.points[snap.StopKey] = snap
	return nil
}
