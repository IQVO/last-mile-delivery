package analyticsstore

import (
	"context"

	"github.com/claudioed/last-mile-delivery/internal/analytics/report"
)

// EtaWindow implements report.Reader with the rules of etaSQL: DELIVERED
// attempts of the range, grouped by UTC day and station, matched to the ETA
// window of the same route and package; both bounds are inside.
func (m *MemoryStore) EtaWindow(_ context.Context, r report.Range) ([]report.EtaDay, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	groups := map[dayStation]*report.EtaDay{}
	for _, a := range m.attempts {
		if a.outcome != report.OutcomeDelivered || !inRange(a.at, r) {
			continue
		}
		k := dayStation{utcDay(a.at), m.stationOf(a)}
		d := groups[k]
		if d == nil {
			d = &report.EtaDay{Day: k.day, Station: k.station}
			groups[k] = d
		}
		w, ok := m.etas[a.routeID][a.tracking]
		if !ok {
			continue
		}
		d.DeliveriesWithEta++
		switch {
		case a.at.Before(w.start):
			d.DeliveriesBefore++
		case a.at.After(w.end):
			d.DeliveriesAfter++
		default:
			d.DeliveriesInside++
		}
	}
	out := []report.EtaDay{}
	for _, k := range sortedKeys(groups) {
		out = append(out, *groups[k])
	}
	return out, nil
}
