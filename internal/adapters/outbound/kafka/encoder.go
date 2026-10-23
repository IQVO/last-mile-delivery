package kafka

import (
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/claudioed/last-mile-delivery/internal/adapters/kafka/cloudevents"
	"github.com/claudioed/last-mile-delivery/internal/application/outbox"
	"github.com/claudioed/last-mile-delivery/internal/application/ports"
	"github.com/claudioed/last-mile-delivery/internal/domain/history"
	"github.com/claudioed/last-mile-delivery/internal/domain/route"
	"github.com/claudioed/last-mile-delivery/internal/domain/shared"
	"github.com/claudioed/last-mile-delivery/internal/domain/wave"
)

// The two topics of this service. Integration events go ONLY to EventsTopic;
// RoutePlanned (analytics only, ADR 0004) goes ONLY to AnalyticsTopic.
const (
	EventsTopic    = "retailnetwork.last-mile-delivery.events"
	AnalyticsTopic = "retailnetwork.last-mile-delivery.analytics"
)

// schemaVersion is the dataschema version of every payload below
// (urn:retailnetwork:last-mile-delivery:<stream>:<EventName>:v1).
const schemaVersion = 1

// Wire payloads (CloudEvents `data`, camelCase JSON, pinned in
// apis/asyncapi.yaml). Optional fields are omitted when unset; timestamps are
// RFC 3339 UTC.

type waveScheduledData struct {
	WaveID         string `json:"waveId"`
	StationNodeID  string `json:"stationNodeId"`
	ServiceDate    string `json:"serviceDate"`
	InductCutoffAt string `json:"inductCutoffAt"`
	WindowStart    string `json:"windowStart"`
	WindowEnd      string `json:"windowEnd"`
	StopCapacity   int    `json:"stopCapacity"`
}

type waveCapacityData struct {
	WaveID         string `json:"waveId"`
	StopCapacity   int    `json:"stopCapacity"`
	AssignedStops  int    `json:"assignedStops"`
	RemainingStops int    `json:"remainingStops"`
}

type waveCancelledData struct {
	WaveID string `json:"waveId"`
	Reason string `json:"reason"`
}

type routePlannedData struct {
	RouteID   string `json:"routeId"`
	WaveID    string `json:"waveId"`
	DSPID     string `json:"dspId"`
	StopCount int    `json:"stopCount"`
}

type outForDeliveryData struct {
	RouteID        string `json:"routeId"`
	TrackingNumber string `json:"trackingNumber"`
	StationNodeID  string `json:"stationNodeId"`
	At             string `json:"at"`
}

type attemptedData struct {
	RouteID        string `json:"routeId"`
	TrackingNumber string `json:"trackingNumber"`
	Outcome        string `json:"outcome"`
	FailureReason  string `json:"failureReason,omitempty"`
	At             string `json:"at"`
}

type inductionDeferredData struct {
	TrackingNumber string `json:"trackingNumber"`
	AssignmentID   string `json:"assignmentId,omitempty"`
}

type returnInitiatedData struct {
	TrackingNumber string `json:"trackingNumber"`
}

// Encoder implements ports.EventEncoder: each domain event becomes one outbox
// message holding the CloudEvents bytes, the Kafka key (the subject) and the
// content-type header. The CloudEvents `id` is minted HERE, once, and persisted
// with the outbox row, so a relay retry republishes the same id.
type Encoder struct {
	// NewID mints a CloudEvents id; uuid.NewString when nil.
	NewID func() string
	// EventsTopic and AnalyticsTopic override the destination topics (the
	// package constants when empty); integration tests use unique topics.
	EventsTopic    string
	AnalyticsTopic string
}

// NewEncoder returns an Encoder minting random UUID v4 ids.
func NewEncoder() *Encoder { return &Encoder{NewID: uuid.NewString} }

var _ ports.EventEncoder = (*Encoder)(nil)

// Encode encodes events in order. An event type it does not publish is a
// programming error and fails the whole call.
func (e *Encoder) Encode(events ...shared.Event) ([]outbox.Message, error) {
	newID := e.NewID
	if newID == nil {
		newID = uuid.NewString
	}
	out := make([]outbox.Message, 0, len(events))
	for _, ev := range events {
		data, stream, err := payloadFor(ev)
		if err != nil {
			return nil, err
		}
		msg, err := e.build(ev, newID(), stream, data)
		if err != nil {
			return nil, err
		}
		out = append(out, msg)
	}
	return out, nil
}

func (e *Encoder) topic(stream string) string {
	if stream == cloudevents.StreamAnalytics {
		if e.AnalyticsTopic != "" {
			return e.AnalyticsTopic
		}
		return AnalyticsTopic
	}
	if e.EventsTopic != "" {
		return e.EventsTopic
	}
	return EventsTopic
}

func (e *Encoder) build(ev shared.Event, id, stream string, data any) (outbox.Message, error) {
	value, err := cloudevents.New(cloudevents.Spec{
		ID: id, Entity: ev.Entity(), EventName: ev.EventName(), Subject: ev.SubjectID(), Time: ev.OccurredAt(),
		Stream: stream, Version: schemaVersion, Data: data,
	})
	if err != nil {
		return outbox.Message{}, fmt.Errorf("encode %s: %w", ev.EventName(), err)
	}
	ct := cloudevents.ContentTypeHeader()
	return outbox.Message{
		EventID: id, Topic: e.topic(stream), EventType: cloudevents.Type(ev.Entity(), ev.EventName()), Subject: ev.SubjectID(),
		Key: []byte(ev.SubjectID()), DataSchema: cloudevents.DataSchema(stream, ev.EventName(), schemaVersion), Value: value,
		Headers: []outbox.Header{{Key: ct.Key, Value: string(ct.Value)}},
	}, nil
}

func ts(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }

// payloadFor maps one domain event to its wire payload and its stream.
func payloadFor(ev shared.Event) (any, string, error) {
	events := cloudevents.StreamEvents
	switch e := ev.(type) {
	case wave.DeliveryWaveScheduled:
		return waveScheduledData{
			WaveID: string(e.WaveID), StationNodeID: e.StationNodeID, ServiceDate: e.ServiceDate, InductCutoffAt: ts(e.InductCutoffAt),
			WindowStart: ts(e.WindowStart), WindowEnd: ts(e.WindowEnd), StopCapacity: e.StopCapacity,
		}, events, nil
	case wave.DeliveryWaveCapacityChanged:
		return waveCapacityData{WaveID: string(e.WaveID), StopCapacity: e.StopCapacity, AssignedStops: e.AssignedStops, RemainingStops: e.RemainingStops}, events, nil
	case wave.DeliveryWaveCancelled:
		return waveCancelledData{WaveID: string(e.WaveID), Reason: e.Reason}, events, nil
	case route.RoutePlanned:
		return routePlannedData{RouteID: string(e.RouteID), WaveID: string(e.WaveID), DSPID: string(e.DSPID), StopCount: e.StopCount}, cloudevents.StreamAnalytics, nil
	case route.PackageOutForDelivery:
		return outForDeliveryData{RouteID: string(e.RouteID), TrackingNumber: e.TrackingNumber, StationNodeID: e.StationNodeID, At: ts(e.At)}, events, nil
	case route.DeliveryAttempted:
		return attemptedData{
			RouteID: string(e.RouteID), TrackingNumber: e.TrackingNumber, Outcome: string(e.Outcome), FailureReason: e.FailureReason, At: ts(e.At),
		}, events, nil
	case history.PackageInductionDeferred:
		return inductionDeferredData{TrackingNumber: e.TrackingNumber, AssignmentID: e.AssignmentID}, events, nil
	case history.PackageReturnInitiated:
		return returnInitiatedData{TrackingNumber: e.TrackingNumber}, events, nil
	default:
		return nil, "", fmt.Errorf("kafka encoder: %s is not a published event", ev.EventName())
	}
}
