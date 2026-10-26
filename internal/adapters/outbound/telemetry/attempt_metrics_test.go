package telemetry_test

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/claudioed/last-mile-delivery/internal/adapters/outbound/telemetry"
)

func TestAttemptMetricsCountsByOutcomeAndReason(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	previous := otel.GetMeterProvider()
	otel.SetMeterProvider(sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader)))
	t.Cleanup(func() { otel.SetMeterProvider(previous) })

	metrics, err := telemetry.NewAttemptMetrics()
	if err != nil {
		t.Fatalf("NewAttemptMetrics: %v", err)
	}
	ctx := context.Background()
	metrics.AttemptDelivered(ctx)
	metrics.AttemptDelivered(ctx)
	metrics.AttemptFailed(ctx)
	metrics.AttemptRejected(ctx, "route-not-started")
	metrics.AttemptRejected(ctx, "already-delivered")
	metrics.AttemptRejected(ctx, "already-delivered")

	got := collectAttemptPoints(t, reader)
	want := map[string]int64{"delivered/": 2, "failed/": 1, "rejected/route-not-started": 1, "rejected/already-delivered": 2}
	if len(got) != len(want) {
		t.Fatalf("data points = %v, want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %d, want %d (all: %v)", k, got[k], v, got)
		}
	}
}

// collectAttemptPoints reads the last_mile_delivery.package.attempted data
// points as "outcome/reason" -> value, checking the unit and description.
func collectAttemptPoints(t *testing.T, reader sdkmetric.Reader) map[string]int64 {
	t.Helper()
	var collected metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &collected); err != nil {
		t.Fatalf("Collect: %v", err)
	}
	got := map[string]int64{}
	for _, scope := range collected.ScopeMetrics {
		for _, m := range scope.Metrics {
			if m.Name == "last_mile_delivery.package.attempted" {
				addPoints(t, m, got)
			}
		}
	}
	return got
}

func addPoints(t *testing.T, m metricdata.Metrics, got map[string]int64) {
	t.Helper()
	if m.Unit != "{package}" || m.Description == "" {
		t.Errorf("unit = %q, description empty = %v; the convention wants a UCUM unit and a description", m.Unit, m.Description == "")
	}
	sum, ok := m.Data.(metricdata.Sum[int64])
	if !ok {
		t.Fatalf("%s is a %T, want an int64 Sum", m.Name, m.Data)
	}
	for _, point := range sum.DataPoints {
		outcome, _ := point.Attributes.Value("outcome")
		reason, _ := point.Attributes.Value("reason")
		got[outcome.AsString()+"/"+reason.AsString()] = point.Value
	}
}

// Whatever the global provider is, recording is fire-and-forget.
func TestAttemptMetricsRecordingNeverPanics(t *testing.T) {
	metrics, err := telemetry.NewAttemptMetrics()
	if err != nil {
		t.Fatal(err)
	}
	metrics.AttemptDelivered(context.Background())
	metrics.AttemptFailed(context.Background())
	metrics.AttemptRejected(context.Background(), "internal-error")
}
