package kafka

import (
	"context"
	"log/slog"
	"reflect"
	"sync"
	"testing"
	"time"

	kafkago "github.com/segmentio/kafka-go"
)

// warnCounter counts the WARN records and keeps the last one's attributes.
type warnCounter struct {
	mu    sync.Mutex
	warns int
	last  map[string]any
}

func (w *warnCounter) Enabled(context.Context, slog.Level) bool { return true }

func (w *warnCounter) Handle(_ context.Context, r slog.Record) error {
	if r.Level != slog.LevelWarn {
		return nil
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.warns++
	w.last = map[string]any{}
	r.Attrs(func(a slog.Attr) bool { w.last[a.Key] = a.Value.Any(); return true })
	return nil
}

func (w *warnCounter) WithAttrs([]slog.Attr) slog.Handler { return w }
func (w *warnCounter) WithGroup(string) slog.Handler      { return w }

func TestAnalyticsConsumerSkipsNonCloudEventsAndUnknownTypesWithASampledWarn(t *testing.T) {
	warns := &warnCounter{}
	p, dlq := &fakeProjection{}, &analyticsDLQ{}
	c := analyticsConsumer(p, dlq, slog.New(warns))
	clock := time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)
	c.Now = func() time.Time { return clock }
	skip := []kafkago.Message{
		{Value: []byte(`{"event_id":"e-1","event_type":"DeliveryAttempted"}`)},
		{Value: []byte("not json")},
		{Value: ownEvent(t, entityRoute, "RouteTeleported", "R1", map[string]any{"routeId": "R1"})},
	}
	for _, m := range skip {
		if err := c.HandleMessage(context.Background(), m); err != nil {
			t.Fatalf("a skipped message must be committed past, got %v", err)
		}
	}
	if warns.warns != 1 {
		t.Fatalf("warns = %d, want 1 (sampled)", warns.warns)
	}
	clock = clock.Add(59 * time.Second)
	_ = c.HandleMessage(context.Background(), skip[0])
	if warns.warns != 1 {
		t.Fatalf("a skip inside the interval must stay silent, warns = %d", warns.warns)
	}
	clock = clock.Add(time.Second)
	_ = c.HandleMessage(context.Background(), skip[1])
	if warns.warns != 2 || warns.last["suppressed_since_last_warning"] != int64(3) {
		t.Fatalf("warns = %d last = %v, want a second warning reporting 3 suppressed", warns.warns, warns.last)
	}
	if len(p.events) != 0 || len(dlq.msgs) != 0 {
		t.Fatalf("a skipped message is neither projected nor dead-lettered: applied=%d dlq=%d", len(p.events), len(dlq.msgs))
	}
}

func TestAnalyticsConsumerAcknowledgesTheUnprojectedOwnTypeSilently(t *testing.T) {
	warns := &warnCounter{}
	p, dlq := &fakeProjection{}, &analyticsDLQ{}
	c := analyticsConsumer(p, dlq, slog.New(warns))
	msg := kafkago.Message{Value: ownEvent(t, entityHistory, "PackageInductionDeferred", "T1", map[string]any{"trackingNumber": "T1"})}
	if err := c.HandleMessage(context.Background(), msg); err != nil {
		t.Fatal(err)
	}
	if warns.warns != 0 || len(p.events) != 0 || len(dlq.msgs) != 0 {
		t.Fatalf("warns=%d applied=%d dlq=%d, want all zero", warns.warns, len(p.events), len(dlq.msgs))
	}
}

func TestSkipWarnInterval(t *testing.T) {
	if skipWarnEvery() != time.Minute {
		t.Fatalf("skipWarnEvery = %v", skipWarnEvery())
	}
}

func TestNewAnalyticsConsumerWiring(t *testing.T) {
	c := NewAnalyticsConsumer([]string{"localhost:1"}, OwnTopics(), AnalyticsDLQTopic, "group-x", &fakeProjection{}, nil)
	if c.Topic != OwnEventsTopic || c.DLQTopic != AnalyticsDLQTopic || c.Reader == nil || c.DLQ == nil || c.Logger == nil {
		t.Fatalf("consumer = %+v", c)
	}
	r, ok := c.Reader.(*kafkago.Reader)
	if !ok || !reflect.DeepEqual(r.Config().GroupTopics, OwnTopics()) || r.Config().GroupID != "group-x" || r.Config().Topic != "" {
		t.Fatalf("the reader must subscribe the group to both own topics, config = %+v", c.Reader)
	}
	want := []string{"retailnetwork.last-mile-delivery.events", "retailnetwork.last-mile-delivery.analytics"}
	if !reflect.DeepEqual(OwnTopics(), want) || AnalyticsDLQTopic != "retailnetwork.last-mile-delivery.analytics.dlq" {
		t.Fatal("the projector reads its own two topics and dead-letters to the analytics DLQ")
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	if empty := NewAnalyticsConsumer([]string{"localhost:1"}, []string{OwnEventsTopic}, AnalyticsDLQTopic, "g", &fakeProjection{}, nil); empty.Topic != OwnEventsTopic {
		t.Fatalf("one topic: span topic = %q", empty.Topic)
	}
}
