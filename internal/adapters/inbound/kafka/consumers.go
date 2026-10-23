package kafka

import (
	"context"
	"log/slog"
	"time"

	ce "github.com/cloudevents/sdk-go/v2/event"

	"github.com/claudioed/last-mile-delivery/internal/application/usecases"
)

// Full CloudEvents types of the four consumed events (ADR 0003).
const (
	TypeLoadArrived         = "com.retailnetwork.logistics.linehaul-transportation.load.LoadArrived"
	TypeNodeRegistered      = "com.retailnetwork.logistics.node-topology.node.NodeRegistered"
	TypeServiceAreaAssigned = "com.retailnetwork.logistics.node-topology.servicearea.ServiceAreaAssigned"
	TypeLabelIssued         = "com.retailnetwork.logistics.shipping-and-tracking.label.LabelIssued"
)

// Only the fields this service reads are decoded; unknown fields are ignored.

type loadArrivedData struct {
	LoadID            string    `json:"loadId"`
	DepartureID       string    `json:"departureId"`
	DestinationNodeID string    `json:"destinationNodeId"`
	NodeType          string    `json:"nodeType"`
	TrackingNumbers   []string  `json:"trackingNumbers"`
	ArrivedAt         time.Time `json:"arrivedAt"`
}

type nodeRegisteredData struct {
	NodeID   string `json:"nodeId"`
	NodeType string `json:"nodeType"`
	RegionID string `json:"regionId"`
	Timezone string `json:"timezone"`
}

type serviceAreaAssignedData struct {
	PostalPrefix          string `json:"postalPrefix"`
	DeliveryStationNodeID string `json:"deliveryStationNodeId"`
	RegionID              string `json:"regionId"`
	AssignmentVersion     int64  `json:"assignmentVersion"`
}

type labelIssuedData struct {
	TrackingNumber          string `json:"trackingNumber"`
	AssignmentID            string `json:"assignmentId"`
	StopKey                 string `json:"stopKey"`
	DestinationPostalPrefix string `json:"destinationPostalPrefix"`
}

// LinehaulHandlers dispatches LoadArrived to the InductOnArrival policy.
func LinehaulHandlers(uc *usecases.InductOnArrival) map[string]HandlerFunc {
	return map[string]HandlerFunc{
		TypeLoadArrived: func(ctx context.Context, e ce.Event) error {
			d, err := decodeData[loadArrivedData](e)
			if err != nil {
				return err
			}
			return uc.Execute(ctx, usecases.LoadArrivedCommand{
				EventID: e.ID(), EventType: e.Type(), LoadID: d.LoadID, DepartureID: d.DepartureID, DestinationNodeID: d.DestinationNodeID,
				NodeType: d.NodeType, TrackingNumbers: d.TrackingNumbers, ArrivedAt: d.ArrivedAt,
			})
		},
	}
}

// TopologyHandlers feeds the station read models.
func TopologyHandlers(nodes *usecases.ApplyNodeRegistered, areas *usecases.ApplyServiceAreaAssigned) map[string]HandlerFunc {
	return map[string]HandlerFunc{
		TypeNodeRegistered: func(ctx context.Context, e ce.Event) error {
			d, err := decodeData[nodeRegisteredData](e)
			if err != nil {
				return err
			}
			return nodes.Execute(ctx, usecases.NodeRegisteredCommand{
				EventID: e.ID(), EventType: e.Type(), NodeID: d.NodeID, NodeType: d.NodeType, RegionID: d.RegionID, Timezone: d.Timezone,
			})
		},
		TypeServiceAreaAssigned: func(ctx context.Context, e ce.Event) error {
			d, err := decodeData[serviceAreaAssignedData](e)
			if err != nil {
				return err
			}
			return areas.Execute(ctx, usecases.ServiceAreaAssignedCommand{
				EventID: e.ID(), EventType: e.Type(), PostalPrefix: d.PostalPrefix, StationNodeID: d.DeliveryStationNodeID,
				RegionID: d.RegionID, AssignmentVersion: d.AssignmentVersion,
			})
		},
	}
}

// ShippingHandlers feeds the package-stop read model from LabelIssued.
func ShippingHandlers(uc *usecases.ApplyLabelIssued) map[string]HandlerFunc {
	return map[string]HandlerFunc{
		TypeLabelIssued: func(ctx context.Context, e ce.Event) error {
			d, err := decodeData[labelIssuedData](e)
			if err != nil {
				return err
			}
			return uc.Execute(ctx, usecases.LabelIssuedCommand{
				EventID: e.ID(), EventType: e.Type(), TrackingNumber: d.TrackingNumber, AssignmentID: d.AssignmentID,
				StopKey: d.StopKey, DestinationPostalPrefix: d.DestinationPostalPrefix,
			})
		},
	}
}

// NewLinehaulConsumer builds the consumer of the linehaul topic.
func NewLinehaulConsumer(brokers []string, groupID string, uc *usecases.InductOnArrival, logger *slog.Logger) *Consumer {
	return NewLinehaulConsumerForTopic(brokers, LinehaulTopic, groupID, uc, logger)
}

// NewLinehaulConsumerForTopic is NewLinehaulConsumer on another topic (tests).
func NewLinehaulConsumerForTopic(brokers []string, topic, groupID string, uc *usecases.InductOnArrival, logger *slog.Logger) *Consumer {
	return newConsumer("linehaul consumer", brokers, topic, groupID, LinehaulHandlers(uc), logger)
}

// NewTopologyConsumer builds the consumer of the node-topology topic.
func NewTopologyConsumer(brokers []string, groupID string, nodes *usecases.ApplyNodeRegistered, areas *usecases.ApplyServiceAreaAssigned, logger *slog.Logger) *Consumer {
	return NewTopologyConsumerForTopic(brokers, TopologyTopic, groupID, nodes, areas, logger)
}

// NewTopologyConsumerForTopic is NewTopologyConsumer on another topic (tests).
func NewTopologyConsumerForTopic(brokers []string, topic, groupID string, nodes *usecases.ApplyNodeRegistered, areas *usecases.ApplyServiceAreaAssigned, logger *slog.Logger) *Consumer {
	return newConsumer("node-topology consumer", brokers, topic, groupID, TopologyHandlers(nodes, areas), logger)
}

// NewShippingConsumer builds the consumer of the shipping-and-tracking topic.
func NewShippingConsumer(brokers []string, groupID string, uc *usecases.ApplyLabelIssued, logger *slog.Logger) *Consumer {
	return NewShippingConsumerForTopic(brokers, ShippingTopic, groupID, uc, logger)
}

// NewShippingConsumerForTopic is NewShippingConsumer on another topic (tests).
func NewShippingConsumerForTopic(brokers []string, topic, groupID string, uc *usecases.ApplyLabelIssued, logger *slog.Logger) *Consumer {
	return newConsumer("shipping consumer", brokers, topic, groupID, ShippingHandlers(uc), logger)
}
