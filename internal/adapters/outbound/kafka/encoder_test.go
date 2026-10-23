package kafka

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/claudioed/last-mile-delivery/internal/application/outbox"
	"github.com/claudioed/last-mile-delivery/internal/domain/history"
	"github.com/claudioed/last-mile-delivery/internal/domain/route"
	"github.com/claudioed/last-mile-delivery/internal/domain/shared"
	"github.com/claudioed/last-mile-delivery/internal/domain/wave"
)

const fixedID = "5d3a8c1e-0a63-4f0b-9d54-7a1a6b0c2e11"

// golden is the exact CloudEvents JSON of every published event, byte for byte
// (the key order of the SDK's JSON event format included). apis/asyncapi.yaml
// documents the same shapes; changing one of these is a contract change.
var golden = map[string]string{
	"DeliveryWaveScheduled":       `{"specversion":"1.0","id":"5d3a8c1e-0a63-4f0b-9d54-7a1a6b0c2e11","source":"/retailnetwork/last-mile-delivery","type":"com.retailnetwork.logistics.last-mile-delivery.wave.DeliveryWaveScheduled","subject":"wave-3b9f","datacontenttype":"application/json","dataschema":"urn:retailnetwork:last-mile-delivery:events:DeliveryWaveScheduled:v1","time":"2026-10-24T06:15:00Z","data":{"waveId":"wave-3b9f","stationNodeId":"node-ds-01","serviceDate":"2026-10-24","inductCutoffAt":"2026-10-24T08:15:00Z","windowStart":"2026-10-24T12:15:00Z","windowEnd":"2026-10-24T20:15:00Z","stopCapacity":120}}`,
	"DeliveryWaveCapacityChanged": `{"specversion":"1.0","id":"5d3a8c1e-0a63-4f0b-9d54-7a1a6b0c2e11","source":"/retailnetwork/last-mile-delivery","type":"com.retailnetwork.logistics.last-mile-delivery.wave.DeliveryWaveCapacityChanged","subject":"wave-3b9f","datacontenttype":"application/json","dataschema":"urn:retailnetwork:last-mile-delivery:events:DeliveryWaveCapacityChanged:v1","time":"2026-10-24T06:15:00Z","data":{"waveId":"wave-3b9f","stopCapacity":120,"assignedStops":41,"remainingStops":79}}`,
	"DeliveryWaveCancelled":       `{"specversion":"1.0","id":"5d3a8c1e-0a63-4f0b-9d54-7a1a6b0c2e11","source":"/retailnetwork/last-mile-delivery","type":"com.retailnetwork.logistics.last-mile-delivery.wave.DeliveryWaveCancelled","subject":"wave-3b9f","datacontenttype":"application/json","dataschema":"urn:retailnetwork:last-mile-delivery:events:DeliveryWaveCancelled:v1","time":"2026-10-24T06:15:00Z","data":{"waveId":"wave-3b9f","reason":"station closed"}}`,
	"RoutePlanned":                `{"specversion":"1.0","id":"5d3a8c1e-0a63-4f0b-9d54-7a1a6b0c2e11","source":"/retailnetwork/last-mile-delivery","type":"com.retailnetwork.logistics.last-mile-delivery.route.RoutePlanned","subject":"route-81c2","datacontenttype":"application/json","dataschema":"urn:retailnetwork:last-mile-delivery:analytics:RoutePlanned:v1","time":"2026-10-24T06:15:00Z","data":{"routeId":"route-81c2","waveId":"wave-3b9f","dspId":"dsp-77a0","stopCount":32}}`,
	"PackageOutForDelivery":       `{"specversion":"1.0","id":"5d3a8c1e-0a63-4f0b-9d54-7a1a6b0c2e11","source":"/retailnetwork/last-mile-delivery","type":"com.retailnetwork.logistics.last-mile-delivery.route.PackageOutForDelivery","subject":"route-81c2","datacontenttype":"application/json","dataschema":"urn:retailnetwork:last-mile-delivery:events:PackageOutForDelivery:v1","time":"2026-10-24T06:15:00Z","data":{"routeId":"route-81c2","trackingNumber":"TRK-000123","stationNodeId":"node-ds-01","at":"2026-10-24T06:15:00Z"}}`,
	"DeliveryAttempted":           `{"specversion":"1.0","id":"5d3a8c1e-0a63-4f0b-9d54-7a1a6b0c2e11","source":"/retailnetwork/last-mile-delivery","type":"com.retailnetwork.logistics.last-mile-delivery.route.DeliveryAttempted","subject":"route-81c2","datacontenttype":"application/json","dataschema":"urn:retailnetwork:last-mile-delivery:events:DeliveryAttempted:v1","time":"2026-10-24T06:15:00Z","data":{"routeId":"route-81c2","trackingNumber":"TRK-000123","outcome":"FAILED","failureReason":"recipient absent","at":"2026-10-24T06:15:00Z"}}`,
	"PackageInductionDeferred":    `{"specversion":"1.0","id":"5d3a8c1e-0a63-4f0b-9d54-7a1a6b0c2e11","source":"/retailnetwork/last-mile-delivery","type":"com.retailnetwork.logistics.last-mile-delivery.packageattempthistory.PackageInductionDeferred","subject":"TRK-000123","datacontenttype":"application/json","dataschema":"urn:retailnetwork:last-mile-delivery:events:PackageInductionDeferred:v1","time":"2026-10-24T06:15:00Z","data":{"trackingNumber":"TRK-000123","assignmentId":"asg-5531"}}`,
	"PackageReturnInitiated":      `{"specversion":"1.0","id":"5d3a8c1e-0a63-4f0b-9d54-7a1a6b0c2e11","source":"/retailnetwork/last-mile-delivery","type":"com.retailnetwork.logistics.last-mile-delivery.packageattempthistory.PackageReturnInitiated","subject":"TRK-000123","datacontenttype":"application/json","dataschema":"urn:retailnetwork:last-mile-delivery:events:PackageReturnInitiated:v1","time":"2026-10-24T06:15:00Z","data":{"trackingNumber":"TRK-000123"}}`,
}

func fixedEvents() []shared.Event {
	at := time.Date(2026, 10, 24, 6, 15, 0, 0, time.UTC)
	h := shared.Header{Version: 2, At: at}
	return []shared.Event{
		wave.DeliveryWaveScheduled{Header: h, WaveID: "wave-3b9f", StationNodeID: "node-ds-01", ServiceDate: "2026-10-24", InductCutoffAt: at.Add(2 * time.Hour), WindowStart: at.Add(6 * time.Hour), WindowEnd: at.Add(14 * time.Hour), StopCapacity: 120},
		wave.DeliveryWaveCapacityChanged{Header: h, WaveID: "wave-3b9f", StopCapacity: 120, AssignedStops: 41, RemainingStops: 79},
		wave.DeliveryWaveCancelled{Header: h, WaveID: "wave-3b9f", Reason: "station closed"},
		route.RoutePlanned{Header: h, RouteID: "route-81c2", WaveID: "wave-3b9f", DSPID: "dsp-77a0", StopCount: 32},
		route.PackageOutForDelivery{Header: h, RouteID: "route-81c2", TrackingNumber: "TRK-000123", StationNodeID: "node-ds-01", At: at},
		route.DeliveryAttempted{Header: h, RouteID: "route-81c2", TrackingNumber: "TRK-000123", Outcome: route.OutcomeFailed, FailureReason: "recipient absent", At: at},
		history.PackageInductionDeferred{Header: h, TrackingNumber: "TRK-000123", AssignmentID: "asg-5531"},
		history.PackageReturnInitiated{Header: h, TrackingNumber: "TRK-000123"},
	}
}

func fixedEncoder() *Encoder { return &Encoder{NewID: func() string { return fixedID }} }

func TestEveryPublishedEventHasAnExactGolden(t *testing.T) {
	events := fixedEvents()
	if len(events) != len(golden) {
		t.Fatalf("%d events but %d goldens: every published event needs both", len(events), len(golden))
	}
	for _, ev := range events {
		t.Run(ev.EventName(), func(t *testing.T) {
			msgs, err := fixedEncoder().Encode(ev)
			if err != nil || len(msgs) != 1 {
				t.Fatalf("Encode = %v, %v", msgs, err)
			}
			if got, want := string(msgs[0].Value), golden[ev.EventName()]; got != want {
				t.Fatalf("CloudEvent JSON\n got %s\nwant %s", got, want)
			}
			if !json.Valid(msgs[0].Value) {
				t.Fatal("the value must be valid JSON")
			}
		})
	}
}

func TestMessageEnvelopeAroundTheValue(t *testing.T) {
	cases := []struct {
		event, topic, key, typ, schema string
	}{
		{"DeliveryWaveScheduled", EventsTopic, "wave-3b9f", "com.retailnetwork.logistics.last-mile-delivery.wave.DeliveryWaveScheduled", "urn:retailnetwork:last-mile-delivery:events:DeliveryWaveScheduled:v1"},
		{"DeliveryWaveCancelled", EventsTopic, "wave-3b9f", "com.retailnetwork.logistics.last-mile-delivery.wave.DeliveryWaveCancelled", "urn:retailnetwork:last-mile-delivery:events:DeliveryWaveCancelled:v1"},
		{"RoutePlanned", AnalyticsTopic, "route-81c2", "com.retailnetwork.logistics.last-mile-delivery.route.RoutePlanned", "urn:retailnetwork:last-mile-delivery:analytics:RoutePlanned:v1"},
		{"DeliveryAttempted", EventsTopic, "route-81c2", "com.retailnetwork.logistics.last-mile-delivery.route.DeliveryAttempted", "urn:retailnetwork:last-mile-delivery:events:DeliveryAttempted:v1"},
		{"PackageReturnInitiated", EventsTopic, "TRK-000123", "com.retailnetwork.logistics.last-mile-delivery.packageattempthistory.PackageReturnInitiated", "urn:retailnetwork:last-mile-delivery:events:PackageReturnInitiated:v1"},
	}
	byName := map[string]shared.Event{}
	for _, ev := range fixedEvents() {
		byName[ev.EventName()] = ev
	}
	for _, c := range cases {
		msgs, err := fixedEncoder().Encode(byName[c.event])
		if err != nil {
			t.Fatal(err)
		}
		m := msgs[0]
		if m.Topic != c.topic || string(m.Key) != c.key || m.Subject != c.key || m.EventType != c.typ || m.DataSchema != c.schema || m.EventID != fixedID {
			t.Errorf("%s: message = topic %s key %s type %s schema %s id %s", c.event, m.Topic, m.Key, m.EventType, m.DataSchema, m.EventID)
		}
		if len(m.Headers) != 1 || m.Headers[0] != (outbox.Header{Key: "content-type", Value: "application/cloudevents+json; charset=UTF-8"}) {
			t.Errorf("%s: headers = %+v", c.event, m.Headers)
		}
	}
}

func TestOnlyRoutePlannedIsAnalyticsOnly(t *testing.T) {
	msgs, err := fixedEncoder().Encode(fixedEvents()...)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range msgs {
		analytics := strings.HasSuffix(m.EventType, ".RoutePlanned")
		if analytics != (m.Topic == AnalyticsTopic) || (!analytics && m.Topic != EventsTopic) {
			t.Errorf("%s went to %s", m.EventType, m.Topic)
		}
	}
}

func TestOptionalFieldsAreOmitted(t *testing.T) {
	h := shared.Header{Version: 1, At: time.Date(2026, 10, 24, 6, 15, 0, 0, time.UTC)}
	delivered := route.DeliveryAttempted{Header: h, RouteID: "r", TrackingNumber: "T", Outcome: route.OutcomeDelivered, At: h.At}
	deferred := history.PackageInductionDeferred{Header: h, TrackingNumber: "T"}
	msgs, err := fixedEncoder().Encode(delivered, deferred)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(msgs[0].Value), "failureReason") || strings.Contains(string(msgs[1].Value), "assignmentId") {
		t.Fatalf("optional fields must be omitted: %s %s", msgs[0].Value, msgs[1].Value)
	}
}

func TestTopicOverridesAndRandomIDs(t *testing.T) {
	enc := &Encoder{EventsTopic: "t.events", AnalyticsTopic: "t.analytics"}
	msgs, err := enc.Encode(fixedEvents()...)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, m := range msgs {
		want := "t.events"
		if strings.HasSuffix(m.EventType, ".RoutePlanned") {
			want = "t.analytics"
		}
		if m.Topic != want || seen[m.EventID] || m.EventID == "" {
			t.Fatalf("topic %s id %q (want topic %s, a fresh id per event)", m.Topic, m.EventID, want)
		}
		seen[m.EventID] = true
	}
	if got, err := NewEncoder().Encode(); err != nil || len(got) != 0 {
		t.Fatalf("no events: %v %v", got, err)
	}
}

type foreignEvent struct{ shared.Header }

func (foreignEvent) EventName() string { return "Unknown" }
func (foreignEvent) Entity() string    { return "wave" }
func (foreignEvent) SubjectID() string { return "x" }

func TestAnUnpublishedEventFailsTheWholeCall(t *testing.T) {
	msgs, err := fixedEncoder().Encode(fixedEvents()[0], foreignEvent{})
	if err == nil || msgs != nil || !strings.Contains(err.Error(), "Unknown is not a published event") {
		t.Fatalf("Encode = %v, %v", msgs, err)
	}
	empty := history.PackageReturnInitiated{Header: shared.Header{Version: 1, At: time.Now()}}
	if _, err := fixedEncoder().Encode(empty); err == nil {
		t.Fatal("an event with an empty subject must not encode")
	}
}
