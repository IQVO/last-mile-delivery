package route

import (
	"fmt"
	"time"

	"github.com/claudioed/last-mile-delivery/internal/domain/shared"
)

// Start moves PLANNED to OUT_FOR_DELIVERY and raises one PackageOutForDelivery
// per tracking number, in stop order.
func (r *Route) Start(now time.Time) ([]shared.Event, error) {
	if r.state != StatePlanned {
		return nil, fmt.Errorf("%w: StartRoute needs route %s PLANNED but it is %s", shared.ErrIllegalTransition, r.id, r.state)
	}
	r.state = StateOutForDelivery
	r.version++
	var events []shared.Event
	for _, s := range r.stops {
		for _, tn := range s.TrackingNumbers {
			events = append(events, PackageOutForDelivery{
				Header: shared.Header{Version: r.version, At: shared.UTC(now)}, RouteID: r.id, TrackingNumber: tn,
				StationNodeID: r.station, At: shared.UTC(now),
			})
		}
	}
	return events, nil
}
