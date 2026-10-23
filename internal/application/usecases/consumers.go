package usecases

import (
	"context"
	"time"

	"github.com/claudioed/last-mile-delivery/internal/application/views"
	"github.com/claudioed/last-mile-delivery/internal/domain/shared"
)

// claimed claims the CloudEvents id in the unit of work; false means the event
// was applied before and the use case must do nothing.
func (w writer) claimed(ctx context.Context, eventID, eventType string) (bool, error) {
	return w.Inbox.Claim(ctx, eventID, eventType)
}

// LoadArrivedCommand is the input of InductOnArrival: the fields of
// linehaul-transportation's LoadArrived that this service relies on.
type LoadArrivedCommand struct {
	EventID           string
	EventType         string
	LoadID            string
	DepartureID       string
	DestinationNodeID string
	NodeType          string
	TrackingNumbers   []string
	ArrivedAt         time.Time
}

// InductOnArrival is policy InductOnArrival: a load reached a delivery station.
type InductOnArrival struct{ writer }

// NewInductOnArrival builds the use case.
func NewInductOnArrival(d Deps) *InductOnArrival { return &InductOnArrival{writer{d}} }

// Execute claims the event, ignores a load that did not reach a delivery
// station (A8), records every arrival and inducts every package (A6, A18, A19).
func (uc *InductOnArrival) Execute(ctx context.Context, cmd LoadArrivedCommand) error {
	_, err := transact(ctx, uc.UoW, func(ctx context.Context) (struct{}, error) {
		ok, err := uc.claimed(ctx, cmd.EventID, cmd.EventType)
		if err != nil || !ok {
			return struct{}{}, err
		}
		station, err := uc.isStation(ctx, cmd)
		if err != nil || !station {
			return struct{}{}, err
		}
		at := cmd.ArrivedAt
		if at.IsZero() {
			at = uc.now()
		}
		var events []shared.Event
		for _, tn := range cmd.TrackingNumbers {
			if !shared.ValidID(tn) {
				continue
			}
			if err := uc.Inbound.Save(ctx, views.InboundPackage{
				LoadID: cmd.LoadID, TrackingNumber: tn, DepartureID: cmd.DepartureID, StationNodeID: cmd.DestinationNodeID, ArrivedAt: at.UTC(),
			}); err != nil {
				return struct{}{}, err
			}
			ev, err := uc.arrive(ctx, cmd.DestinationNodeID, tn, at)
			if err != nil {
				return struct{}{}, err
			}
			events = append(events, ev...)
		}
		return struct{}{}, uc.publish(ctx, events)
	})
	return err
}

// isStation applies ADR 0002 A8: the event's nodeType when present, else the
// StationView.
func (w writer) isStation(ctx context.Context, cmd LoadArrivedCommand) (bool, error) {
	if !shared.ValidID(cmd.DestinationNodeID) {
		return false, nil
	}
	if cmd.NodeType != "" {
		return cmd.NodeType == views.DeliveryStation, nil
	}
	st, err := w.Stations.Get(ctx, cmd.DestinationNodeID)
	ok, err := found(err)
	return ok && st.NodeType == views.DeliveryStation, err
}

// NodeRegisteredCommand is the input of ApplyNodeRegistered.
type NodeRegisteredCommand struct {
	EventID   string
	EventType string
	NodeID    string
	NodeType  string
	RegionID  string
	Timezone  string
}

// ApplyNodeRegistered feeds the StationView from node-topology's NodeRegistered.
type ApplyNodeRegistered struct{ writer }

// NewApplyNodeRegistered builds the use case.
func NewApplyNodeRegistered(d Deps) *ApplyNodeRegistered { return &ApplyNodeRegistered{writer{d}} }

// Execute claims the event and keeps the node when it is a DELIVERY_STATION.
func (uc *ApplyNodeRegistered) Execute(ctx context.Context, cmd NodeRegisteredCommand) error {
	_, err := transact(ctx, uc.UoW, func(ctx context.Context) (struct{}, error) {
		ok, err := uc.claimed(ctx, cmd.EventID, cmd.EventType)
		if err != nil || !ok || cmd.NodeType != views.DeliveryStation || !shared.ValidID(cmd.NodeID) {
			return struct{}{}, err
		}
		return struct{}{}, uc.Stations.Save(ctx, views.Station{NodeID: cmd.NodeID, NodeType: cmd.NodeType, RegionID: cmd.RegionID, Timezone: cmd.Timezone})
	})
	return err
}

// ServiceAreaAssignedCommand is the input of ApplyServiceAreaAssigned.
type ServiceAreaAssignedCommand struct {
	EventID           string
	EventType         string
	PostalPrefix      string
	StationNodeID     string
	RegionID          string
	AssignmentVersion int64
}

// ApplyServiceAreaAssigned feeds the StationView from ServiceAreaAssigned.
type ApplyServiceAreaAssigned struct{ writer }

// NewApplyServiceAreaAssigned builds the use case.
func NewApplyServiceAreaAssigned(d Deps) *ApplyServiceAreaAssigned {
	return &ApplyServiceAreaAssigned{writer{d}}
}

// Execute claims the event and keeps the assignment when its version is higher.
func (uc *ApplyServiceAreaAssigned) Execute(ctx context.Context, cmd ServiceAreaAssignedCommand) error {
	_, err := transact(ctx, uc.UoW, func(ctx context.Context) (struct{}, error) {
		ok, err := uc.claimed(ctx, cmd.EventID, cmd.EventType)
		if err != nil || !ok || cmd.PostalPrefix == "" || cmd.StationNodeID == "" || cmd.AssignmentVersion < 1 {
			return struct{}{}, err
		}
		_, err = uc.ServiceAreas.Save(ctx, views.ServiceArea{
			PostalPrefix: cmd.PostalPrefix, StationNodeID: cmd.StationNodeID, RegionID: cmd.RegionID, AssignmentVersion: cmd.AssignmentVersion,
		})
		return struct{}{}, err
	})
	return err
}

// LabelIssuedCommand is the input of ApplyLabelIssued.
type LabelIssuedCommand struct {
	EventID                 string
	EventType               string
	TrackingNumber          string
	AssignmentID            string
	StopKey                 string
	DestinationPostalPrefix string
}

// ApplyLabelIssued feeds the PackageStopView from shipping-and-tracking's LabelIssued.
type ApplyLabelIssued struct{ writer }

// NewApplyLabelIssued builds the use case.
func NewApplyLabelIssued(d Deps) *ApplyLabelIssued { return &ApplyLabelIssued{writer{d}} }

// Execute claims the event and keeps the label data; a label without a usable
// stopKey is ignored (A4).
func (uc *ApplyLabelIssued) Execute(ctx context.Context, cmd LabelIssuedCommand) error {
	_, err := transact(ctx, uc.UoW, func(ctx context.Context) (struct{}, error) {
		ok, err := uc.claimed(ctx, cmd.EventID, cmd.EventType)
		if err != nil || !ok || !shared.ValidID(cmd.TrackingNumber) || !shared.ValidStopKey(cmd.StopKey) {
			return struct{}{}, err
		}
		return struct{}{}, uc.PackageStops.Save(ctx, views.PackageStop{
			TrackingNumber: cmd.TrackingNumber, StopKey: cmd.StopKey, DestinationPostalPrefix: cmd.DestinationPostalPrefix, AssignmentID: cmd.AssignmentID,
		})
	})
	return err
}
