package telemetry_test

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/claudioed/last-mile-delivery/internal/adapters/outbound/telemetry"
)

func TestEtaMetricsCountsAssignedAndSkipped(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	previous := otel.GetMeterProvider()
	otel.SetMeterProvider(sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader)))
	t.Cleanup(func() { otel.SetMeterProvider(previous) })

	metrics, err := telemetry.NewEtaMetrics()
	if err != nil {
		t.Fatalf("NewEtaMetrics: %v", err)
	}
	ctx := context.Background()
	metrics.EtaAssigned(ctx, 3)
	metrics.EtaAssigned(ctx, 2)
	metrics.EtaSkipped(ctx, "window_elapsed")

	var collected metricdata.ResourceMetrics
	if err := reader.Collect(ctx, &collected); err != nil {
		t.Fatalf("Collect: %v", err)
	}
	got := map[string]int64{}
	for _, scope := range collected.ScopeMetrics {
		for _, m := range scope.Metrics {
			sum, ok := m.Data.(metricdata.Sum[int64])
			if !ok || m.Description == "" || m.Unit == "" {
				t.Fatalf("%s: %T unit %q: want an int64 Sum with a unit and a description", m.Name, m.Data, m.Unit)
			}
			for _, p := range sum.DataPoints {
				reason, _ := p.Attributes.Value("reason")
				got[m.Name+"/"+reason.AsString()] = p.Value
			}
		}
	}
	want := map[string]int64{"last_mile_delivery.stop_eta.assigned/": 5, "last_mile_delivery.stop_eta.skipped/window_elapsed": 1}
	if len(got) != len(want) {
		t.Fatalf("data points = %v, want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %d, want %d (all: %v)", k, got[k], v, got)
		}
	}
}
