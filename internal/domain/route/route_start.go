package route

import (
	"fmt"
	"time"

	"github.com/claudioed/last-mile-delivery/internal/domain/shared"
	"github.com/claudioed/last-mile-delivery/internal/domain/wave"
)

// Start moves PLANNED to OUT_FOR_DELIVERY and raises one event per tracking
// number in stop order — PackageOutForDelivery for a DELIVERY induction,
// PackagePickupOut for a PICKUP one (ADR 0017) — then one StopEtaWindowAssigned
// per stop in stop order (ADR 0013). wv is the route's wave: its delivery
// window bounds the ETA windows and its inductions give each stop its stopKey
// and each package its kind. When too little of the window is left no ETA
// event is raised; the route starts anyway.
func (r *Route) Start(now time.Time, wv *wave.DeliveryWave) ([]shared.Event, error) {
	if r.state != StatePlanned {
		return nil, fmt.Errorf("%w: StartRoute needs route %s PLANNED but it is %s", shared.ErrIllegalTransition, r.id, r.state)
	}
	if wv == nil {
		return nil, shared.Invalid("the wave of route %s is required to start it", r.id)
	}
	refs, err := r.stopRefs(wv)
	if err != nil {
		return nil, err
	}
	r.state = StateOutForDelivery
	r.version++
	h := shared.Header{Version: r.version, At: shared.UTC(now)}
	var events []shared.Event
	for _, s := range refs {
		for _, tn := range s.TrackingNumbers {
			in, ok := wv.Induction(tn)
			if !ok {
				return nil, shared.Corrupt("package %s of route %s is not inducted in wave %s", tn, r.id, wv.ID())
			}
			if in.Kind == wave.KindPickup {
				events = append(events, PackagePickupOut{Header: h, RouteID: r.id, TrackingNumber: tn, StopKey: s.StopKey, StationNodeID: r.station, At: h.At})
				continue
			}
			events = append(events, PackageOutForDelivery{Header: h, RouteID: r.id, TrackingNumber: tn, StationNodeID: r.station, At: h.At})
		}
	}
	for _, e := range EtaWindows(EtaInput{Now: now, WindowStart: wv.WindowStart(), WindowEnd: wv.WindowEnd(), Stops: refs}) {
		events = append(events, StopEtaWindowAssigned{
			Header: h, RouteID: r.id, StopNo: e.No, StopKey: e.StopKey, TrackingNumbers: e.TrackingNumbers,
			EtaStart: shared.UTC(e.Start), EtaEnd: shared.UTC(e.End),
		})
	}
	return events, nil
}

// stopRefs reads each stop's stopKey from the wave induction of its first
// package. Plan guarantees every package is inducted, so a miss is corrupt data.
func (r *Route) stopRefs(wv *wave.DeliveryWave) ([]StopRef, error) {
	refs := make([]StopRef, 0, len(r.stops))
	for _, s := range r.stops {
		if len(s.TrackingNumbers) == 0 {
			return nil, shared.Corrupt("stop %d of route %s has no package", s.No, r.id)
		}
		in, ok := wv.Induction(s.TrackingNumbers[0])
		if !ok {
			return nil, shared.Corrupt("package %s of route %s is not inducted in wave %s", s.TrackingNumbers[0], r.id, wv.ID())
		}
		refs = append(refs, StopRef{No: s.No, StopKey: in.StopKey, TrackingNumbers: s.TrackingNumbers})
	}
	return refs, nil
}
