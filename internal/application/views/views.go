// Package views holds the read DTOs of the application: what a query or a command
// returns, and the read-model rows that events feed. They carry no behaviour and
// no persistence detail; the ports package stays interfaces only.
package views

import (
	"time"

	"github.com/claudioed/last-mile-delivery/internal/domain/history"
	"github.com/claudioed/last-mile-delivery/internal/domain/partner"
	"github.com/claudioed/last-mile-delivery/internal/domain/route"
	"github.com/claudioed/last-mile-delivery/internal/domain/wave"
)

// Wave is a delivery wave as returned by the API.
type Wave struct {
	WaveID         string
	StationNodeID  string
	ServiceDate    string
	InductCutoffAt time.Time
	WindowStart    time.Time
	WindowEnd      time.Time
	StopCapacity   int
	AssignedStops  int
	RemainingStops int
	State          string
	CancelReason   string
}

// WaveFrom builds the view of a wave.
func WaveFrom(w *wave.DeliveryWave) Wave {
	return Wave{
		WaveID: string(w.ID()), StationNodeID: w.StationNodeID(), ServiceDate: w.ServiceDate(), InductCutoffAt: w.InductCutoffAt(),
		WindowStart: w.WindowStart(), WindowEnd: w.WindowEnd(), StopCapacity: w.StopCapacity(), AssignedStops: w.AssignedStops(),
		RemainingStops: w.RemainingStops(), State: string(w.State()), CancelReason: w.CancelReason(),
	}
}

// WaveStop is one stop of a wave: the packages sharing a stopKey.
type WaveStop struct {
	StopKey                 string
	DestinationPostalPrefix string
	TrackingNumbers         []string
}

// Stop is one stop of a route with its derived outcome (empty until decided).
type Stop struct {
	StopNo          int
	TrackingNumbers []string
	Outcome         string
}

// Attempt is one recorded delivery attempt.
type Attempt struct {
	RouteID        string
	TrackingNumber string
	Outcome        string
	FailureReason  string
	At             time.Time
}

// Route is a route as returned by the API.
type Route struct {
	RouteID       string
	WaveID        string
	DSPID         string
	StationNodeID string
	State         string
	StopCount     int
	Stops         []Stop
	Attempts      []Attempt
}

// RouteFrom builds the view of a route.
func RouteFrom(r *route.Route) Route {
	out := Route{
		RouteID: string(r.ID()), WaveID: string(r.WaveID()), DSPID: string(r.DSPID()), StationNodeID: r.StationNodeID(),
		State: string(r.State()), StopCount: len(r.Stops()), Stops: []Stop{}, Attempts: []Attempt{},
	}
	for _, s := range r.Stops() {
		outcome, _ := r.StopOutcome(s.No)
		out.Stops = append(out.Stops, Stop{StopNo: s.No, TrackingNumbers: s.TrackingNumbers, Outcome: string(outcome)})
	}
	for _, a := range r.Attempts() {
		out.Attempts = append(out.Attempts, AttemptFrom(r.ID(), a))
	}
	return out
}

// AttemptFrom builds the view of one attempt of a route.
func AttemptFrom(id route.ID, a route.Attempt) Attempt {
	return Attempt{RouteID: string(id), TrackingNumber: a.TrackingNumber, Outcome: string(a.Outcome), FailureReason: a.FailureReason, At: a.At}
}

// Partner is a delivery service partner as returned by the API.
type Partner struct {
	DSPID         string
	StationNodeID string
	VanCapacity   int
	State         string
}

// PartnerFrom builds the view of a partner.
func PartnerFrom(p *partner.DeliveryServicePartner) Partner {
	return Partner{DSPID: string(p.ID()), StationNodeID: p.StationNodeID(), VanCapacity: p.VanCapacity(), State: string(p.State())}
}

// PackageHistory is the history of one package (for tests and diagnostics).
type PackageHistory struct {
	TrackingNumber string
	Status         string
	FailedAttempts int
}

// PackageHistoryFrom builds the view of a package history.
func PackageHistoryFrom(h *history.History) PackageHistory {
	return PackageHistory{TrackingNumber: h.TrackingNumber(), Status: string(h.Status()), FailedAttempts: h.FailedAttempts()}
}

// Station is a row of the StationView read model (node-topology NodeRegistered).
type Station struct {
	NodeID   string
	NodeType string
	RegionID string
	Timezone string
}

// ServiceArea is a row of the StationView read model (ServiceAreaAssigned).
type ServiceArea struct {
	PostalPrefix      string
	StationNodeID     string
	RegionID          string
	AssignmentVersion int64
}

// PackageStop is a row of the PackageStopView read model (LabelIssued).
type PackageStop struct {
	TrackingNumber          string
	StopKey                 string
	DestinationPostalPrefix string
	AssignmentID            string
}

// InboundPackage is a row of the InboundPackagesView read model (LoadArrived).
type InboundPackage struct {
	LoadID         string
	TrackingNumber string
	DepartureID    string
	StationNodeID  string
	ArrivedAt      time.Time
}

// DeliveryStation is the node type that can host waves and partners.
const DeliveryStation = "DELIVERY_STATION"
