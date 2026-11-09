package kafka

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	kafkago "github.com/segmentio/kafka-go"

	"github.com/claudioed/last-mile-delivery/internal/adapters/kafka/cloudevents"
	"github.com/claudioed/last-mile-delivery/internal/analytics/report"
)

// fakeProjection records the applied events; errs are returned one per Apply
// call first (nil entries succeed).
type fakeProjection struct {
	mu     sync.Mutex
	events []report.Event
	errs   []error
}

func (f *fakeProjection) Apply(_ context.Context, e report.Event) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.errs) > 0 {
		err := f.errs[0]
		f.errs = f.errs[1:]
		if err != nil {
			return false, err
		}
	}
	f.events = append(f.events, e)
	return true, nil
}

// analyticsDLQ records the dead letters.
type analyticsDLQ struct {
	msgs []kafkago.Message
}

func (d *analyticsDLQ) WriteMessages(_ context.Context, m ...kafkago.Message) error {
	d.msgs = append(d.msgs, m...)
	return nil
}

func (d *analyticsDLQ) Close() error { return nil }

var (
	eventTime   = time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)
	businessAt  = time.Date(2026, 11, 3, 11, 30, 0, 0, time.UTC)
	businessRFC = "2026-11-03T11:30:00Z"
)

// ownEvent encodes one of this context's events through the shared envelope
// builder (the one the outbox encoder uses).
func ownEvent(t *testing.T, entity, name, subject string, data map[string]any) []byte {
	t.Helper()
	stream := cloudevents.StreamEvents
	if name == string(report.KindRoutePlanned) {
		stream = cloudevents.StreamAnalytics
	}
	b, err := cloudevents.New(cloudevents.Spec{ID: "evt-" + name, Entity: entity, EventName: name, Subject: subject,
		Time: eventTime, Stream: stream, Version: 1, Data: data})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func analyticsConsumer(p report.Projection, dlq DeadLetterWriter, logger *slog.Logger) *AnalyticsConsumer {
	if logger == nil {
		logger = quietLogger()
	}
	return &AnalyticsConsumer{Projection: p, DLQ: dlq, Topic: OwnEventsTopic, DLQTopic: AnalyticsDLQTopic, Logger: logger}
}

// projectedCase is one projected type: its wire form and the fact it must yield.
type projectedCase struct {
	entity, name, subject string
	data                  map[string]any
	want                  func(e report.Event) report.Event
}

var projectedCases = []projectedCase{
	{entityWave, "DeliveryWaveScheduled", "W1", map[string]any{"waveId": "W1", "stationNodeId": "ST1", "serviceDate": "2026-11-04",
		"inductCutoffAt": businessRFC, "windowStart": businessRFC, "windowEnd": businessRFC, "stopCapacity": 0},
		func(e report.Event) report.Event {
			e.Kind, e.WaveID, e.StationNodeID = report.KindWaveScheduled, "W1", "ST1"
			e.ServiceDate, e.OccurredAt = time.Date(2026, 11, 4, 0, 0, 0, 0, time.UTC), eventTime
			return e
		}},
	{entityWave, "DeliveryWaveCapacityChanged", "W1", map[string]any{"waveId": "W1", "stopCapacity": 9, "assignedStops": 3, "remainingStops": 6},
		func(e report.Event) report.Event {
			e.Kind, e.WaveID, e.StopCapacity, e.AssignedStops, e.OccurredAt = report.KindWaveCapacityChanged, "W1", 9, 3, eventTime
			return e
		}},
	{entityWave, "DeliveryWaveCancelled", "W1", map[string]any{"waveId": "W1", "reason": "storm"},
		func(e report.Event) report.Event {
			e.Kind, e.WaveID, e.OccurredAt = report.KindWaveCancelled, "W1", eventTime
			return e
		}},
	{entityWave, "WaveCostDeclared", "W1", map[string]any{"waveId": "W1", "costPerStopCents": 0, "declaredAt": businessRFC},
		func(e report.Event) report.Event {
			e.Kind, e.WaveID = report.KindWaveCostDeclared, "W1"
			return e
		}},
	{entityRoute, "RoutePlanned", "R1", map[string]any{"routeId": "R1", "waveId": "W1", "dspId": "D1", "stopCount": 2},
		func(e report.Event) report.Event {
			e.Kind, e.RouteID, e.WaveID, e.DSPID, e.StopCount, e.OccurredAt = report.KindRoutePlanned, "R1", "W1", "D1", 2, eventTime
			return e
		}},
	{entityRoute, "PackageOutForDelivery", "R1", map[string]any{"routeId": "R1", "trackingNumber": "T1", "stationNodeId": "ST1", "at": businessRFC},
		func(e report.Event) report.Event {
			e.Kind, e.RouteID, e.TrackingNumber, e.StationNodeID = report.KindOutForDelivery, "R1", "T1", "ST1"
			return e
		}},
	{entityRoute, "DeliveryAttempted", "R1", map[string]any{"routeId": "R1", "trackingNumber": "T1", "outcome": "FAILED",
		"failureReason": "nobody home", "at": businessRFC},
		func(e report.Event) report.Event {
			e.Kind, e.RouteID, e.TrackingNumber, e.Outcome = report.KindDeliveryAttempted, "R1", "T1", "FAILED"
			return e
		}},
	{entityHistory, "PackageReturnInitiated", "T1", map[string]any{"trackingNumber": "T1"},
		func(e report.Event) report.Event {
			e.Kind, e.TrackingNumber, e.OccurredAt = report.KindReturnInitiated, "T1", eventTime
			return e
		}},
}

func TestAnalyticsConsumerProjectsTheEightTypes(t *testing.T) {
	base := report.Event{At: eventTime, OccurredAt: businessAt}
	for _, c := range projectedCases {
		p, dlq := &fakeProjection{}, &analyticsDLQ{}
		err := analyticsConsumer(p, dlq, nil).HandleMessage(context.Background(),
			kafkago.Message{Value: ownEvent(t, c.entity, c.name, c.subject, c.data)})
		if err != nil || len(dlq.msgs) != 0 || len(p.events) != 1 {
			t.Fatalf("%s: err=%v dlq=%d applied=%d", c.name, err, len(dlq.msgs), len(p.events))
		}
		want := c.want(base)
		want.EventID = "evt-" + c.name
		if !reflect.DeepEqual(p.events[0], want) {
			t.Errorf("%s projected %+v\nwant %+v", c.name, p.events[0], want)
		}
	}
	if len(projectedCases) != len(projectedKinds()) {
		t.Fatalf("%d cases for %d projected kinds", len(projectedCases), len(projectedKinds()))
	}
}

// poisonCase is one deterministic poison message.
type poisonCase struct {
	name, entity, event, subject string
	data                         map[string]any
	errs                         []error
}

var poisonCases = []poisonCase{
	{"undecodable timestamp", entityRoute, "DeliveryAttempted", "R1",
		map[string]any{"routeId": "R1", "trackingNumber": "T1", "outcome": "DELIVERED", "at": "yesterday"}, nil},
	{"route subject is not the routeId", entityRoute, "PackageOutForDelivery", "R9",
		map[string]any{"routeId": "R1", "trackingNumber": "T1", "stationNodeId": "ST1", "at": businessRFC}, nil},
	{"wave subject is not the waveId", entityWave, "DeliveryWaveCancelled", "W9", map[string]any{"waveId": "W1"}, nil},
	{"history subject is not the trackingNumber", entityHistory, "PackageReturnInitiated", "T9", map[string]any{"trackingNumber": "T1"}, nil},
	{"a bad serviceDate", entityWave, "DeliveryWaveScheduled", "W1",
		map[string]any{"waveId": "W1", "stationNodeId": "ST1", "serviceDate": "04/11/2026", "stopCapacity": 3}, nil},
	{"an absent stopCapacity", entityWave, "DeliveryWaveScheduled", "W1",
		map[string]any{"waveId": "W1", "stationNodeId": "ST1", "serviceDate": "2026-11-04"}, nil},
	{"an absent costPerStopCents", entityWave, "WaveCostDeclared", "W1", map[string]any{"waveId": "W1", "declaredAt": businessRFC}, nil},
	{"an outcome outside the vocabulary", entityRoute, "DeliveryAttempted", "R1",
		map[string]any{"routeId": "R1", "trackingNumber": "T1", "outcome": "LOST", "at": businessRFC}, nil},
	{"the store rejects it", entityHistory, "PackageReturnInitiated", "T1", map[string]any{"trackingNumber": "T1"},
		[]error{fmt.Errorf("%w: check violation", report.ErrRejected)}},
}

func TestAnalyticsConsumerDeadLettersPoisonOnce(t *testing.T) {
	for _, c := range poisonCases {
		p, dlq := &fakeProjection{errs: c.errs}, &analyticsDLQ{}
		value := ownEvent(t, c.entity, c.event, c.subject, c.data)
		msg := kafkago.Message{Topic: OwnEventsTopic, Partition: 2, Offset: 7, Key: []byte(c.subject), Value: value}
		if err := analyticsConsumer(p, dlq, nil).HandleMessage(context.Background(), msg); err != nil {
			t.Fatalf("%s: poison must be dead-lettered and committed past, got %v", c.name, err)
		}
		if len(dlq.msgs) != 1 || len(p.events) != 0 {
			t.Fatalf("%s: dlq=%d applied=%d, want exactly one dead letter", c.name, len(dlq.msgs), len(p.events))
		}
		d := dlq.msgs[0]
		if d.Topic != AnalyticsDLQTopic || !bytes.Equal(d.Value, value) || string(d.Key) != c.subject || dlqHeader(d, "x-dlq-source-topic") != OwnEventsTopic {
			t.Errorf("%s: dead letter = topic %s key %s headers %v", c.name, d.Topic, d.Key, d.Headers)
		}
		if dlqHeader(d, "x-dlq-error") == "" {
			t.Errorf("%s: the dead letter must carry the cause", c.name)
		}
	}
}

func dlqHeader(m kafkago.Message, key string) string {
	for _, h := range m.Headers {
		if h.Key == key {
			return string(h.Value)
		}
	}
	return ""
}

func returnEvent(t *testing.T, subject string) []byte {
	t.Helper()
	return ownEvent(t, entityHistory, "PackageReturnInitiated", subject, map[string]any{"trackingNumber": "T1"})
}

func TestAnalyticsConsumerTransientFailureIsReturnedNotDeadLettered(t *testing.T) {
	p, dlq := &fakeProjection{errs: []error{errors.New("connection refused")}}, &analyticsDLQ{}
	if err := analyticsConsumer(p, dlq, nil).HandleMessage(context.Background(), kafkago.Message{Value: returnEvent(t, "T1")}); err == nil {
		t.Fatal("a transient failure must be returned for the loop to retry")
	}
	if len(dlq.msgs) != 0 {
		t.Fatal("a transient failure must never be dead-lettered")
	}
}

// failingDLQ refuses every write.
type failingDLQ struct{}

func (failingDLQ) WriteMessages(context.Context, ...kafkago.Message) error {
	return errors.New("broker down")
}
func (failingDLQ) Close() error { return nil }

func TestAnalyticsConsumerFailedDeadLetterIsRetried(t *testing.T) {
	err := analyticsConsumer(&fakeProjection{}, failingDLQ{}, nil).HandleMessage(context.Background(), kafkago.Message{Value: returnEvent(t, "T9")})
	if err == nil || !strings.Contains(err.Error(), AnalyticsDLQTopic) {
		t.Fatalf("a failed dead-letter write must be returned (so the message is retried), got %v", err)
	}
}

// waitForLog polls rec until its log holds want.
func waitForLog(t *testing.T, rec *loopRecorder, want, msg string) {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		if strings.Contains(strings.Join(rec.log(), " "), want) {
			return
		}
		select {
		case <-deadline:
			t.Fatalf("%s; events: %v", msg, rec.log())
		case <-time.After(5 * time.Millisecond):
		}
	}
}

func TestAnalyticsConsumerRunRetriesTransientFailuresForever(t *testing.T) {
	down := errors.New("down")
	p, dlq := &fakeProjection{errs: []error{down, down, down, down, down, down, nil}}, &analyticsDLQ{}
	c := analyticsConsumer(p, dlq, nil)
	rec := &loopRecorder{}
	c.Reader = &scriptedReader{rec: rec, msgs: []kafkago.Message{{Offset: 1, Value: returnEvent(t, "T1")}}}
	var sleeps []time.Duration
	c.Retry = RetryPolicy{Initial: time.Millisecond, Max: 4 * time.Millisecond}
	c.sleep = func(_ context.Context, d time.Duration) error { sleeps = append(sleeps, d); return nil }
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- c.Run(ctx) }()
	waitForLog(t, rec, "commit:1", "the message is committed once the projection succeeds")
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("Run = %v, want context.Canceled", err)
	}
	if len(p.events) != 1 || len(dlq.msgs) != 0 {
		t.Fatalf("applied=%d dlq=%d: six transient failures must be retried past the domain cap, never dead-lettered", len(p.events), len(dlq.msgs))
	}
	want := []time.Duration{time.Millisecond, 2 * time.Millisecond, 4 * time.Millisecond, 4 * time.Millisecond, 4 * time.Millisecond, 4 * time.Millisecond}
	if !reflect.DeepEqual(sleeps, want) {
		t.Fatalf("backoff = %v, want the capped sequence %v", sleeps, want)
	}
}
