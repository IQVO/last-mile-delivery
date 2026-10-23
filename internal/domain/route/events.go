package route

import (
	"time"

	"github.com/claudioed/last-mile-delivery/internal/domain/partner"
	"github.com/claudioed/last-mile-delivery/internal/domain/shared"
	"github.com/claudioed/last-mile-delivery/internal/domain/wave"
)

const entity = "route"

// RoutePlanned is raised by Plan (analytics only).
type RoutePlanned struct {
	shared.Header
	RouteID   ID
	WaveID    wave.ID
	DSPID     partner.ID
	StopCount int
}

// EventName returns "RoutePlanned".
func (RoutePlanned) EventName() string { return "RoutePlanned" }

// Entity returns "route".
func (RoutePlanned) Entity() string { return entity }

// SubjectID returns the routeId.
func (e RoutePlanned) SubjectID() string { return string(e.RouteID) }

// PackageOutForDelivery is raised by Start, once per tracking number.
type PackageOutForDelivery struct {
	shared.Header
	RouteID        ID
	TrackingNumber string
	StationNodeID  string
	At             time.Time
}

// EventName returns "PackageOutForDelivery".
func (PackageOutForDelivery) EventName() string { return "PackageOutForDelivery" }

// Entity returns "route".
func (PackageOutForDelivery) Entity() string { return entity }

// SubjectID returns the routeId.
func (e PackageOutForDelivery) SubjectID() string { return string(e.RouteID) }

// DeliveryAttempted is raised by RecordAttempt, once per package.
type DeliveryAttempted struct {
	shared.Header
	RouteID        ID
	TrackingNumber string
	Outcome        Outcome
	FailureReason  string
	At             time.Time
}

// EventName returns "DeliveryAttempted".
func (DeliveryAttempted) EventName() string { return "DeliveryAttempted" }

// Entity returns "route".
func (DeliveryAttempted) Entity() string { return entity }

// SubjectID returns the routeId.
func (e DeliveryAttempted) SubjectID() string { return string(e.RouteID) }

var (
	_ shared.Event = RoutePlanned{}
	_ shared.Event = PackageOutForDelivery{}
	_ shared.Event = DeliveryAttempted{}
)
