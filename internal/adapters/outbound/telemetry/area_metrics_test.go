package telemetry_test

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/claudioed/last-mile-delivery/internal/adapters/outbound/telemetry"
)

func TestAreaMetricsCountsCarrierAreaSkipped(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	previous := otel.GetMeterProvider()
	otel.SetMeterProvider(sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader)))
	t.Cleanup(func() { otel.SetMeterProvider(previous) })

	metrics, err := telemetry.NewAreaMetrics()
	if err != nil {
		t.Fatalf("NewAreaMetrics: %v", err)
	}
	ctx := context.Background()
	metrics.CarrierAreaSkipped(ctx)
	metrics.CarrierAreaSkipped(ctx)

	var collected metricdata.ResourceMetrics
	if err := reader.Collect(ctx, &collected); err != nil {
		t.Fatalf("Collect: %v", err)
	}
	found := false
	for _, scope := range collected.ScopeMetrics {
		for _, m := range scope.Metrics {
			if m.Name != "last_mile_delivery.carrier_area.skipped" {
				continue
			}
			found = true
			sum, ok := m.Data.(metricdata.Sum[int64])
			if !ok || m.Description == "" || m.Unit == "" {
				t.Fatalf("%s: %T unit %q: want an int64 Sum with a unit and a description", m.Name, m.Data, m.Unit)
			}
			for _, p := range sum.DataPoints {
				if p.Value != 2 {
					t.Fatalf("%s = %d, want 2", m.Name, p.Value)
				}
			}
		}
	}
	if !found {
		t.Fatal("the carrier_area.skipped counter was not registered")
	}
}
