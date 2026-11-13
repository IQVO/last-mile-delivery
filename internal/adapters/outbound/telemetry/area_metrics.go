package telemetry

import (
	"context"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/metric"

	"github.com/claudioed/last-mile-delivery/internal/application/ports"
)

// carrierAreaSkippedName counts the packages the carrier-area exclusion
// refused (ADR 0015): their destination prefix is assigned to a
// CARRIER_HANDOFF area, so a carrier carries the final leg and the package
// never enters a station wave here.
const carrierAreaSkippedName = "last_mile_delivery.carrier_area.skipped"

// AreaMetrics implements ports.AreaMetrics against the global MeterProvider.
type AreaMetrics struct {
	skipped metric.Int64Counter
}

var _ ports.AreaMetrics = (*AreaMetrics)(nil)

// NewAreaMetrics registers the counter. Call it after Setup so the instrument
// binds to the real MeterProvider.
func NewAreaMetrics() (*AreaMetrics, error) {
	skipped, err := otel.Meter(meterName).Int64Counter(
		carrierAreaSkippedName,
		metric.WithDescription("Arrivals whose induction was refused because the destination prefix is assigned to a "+
			"CARRIER_HANDOFF area (ADR 0015): a contracted carrier carries that final leg, so the package never enters a "+
			"station wave. A step up when no area was reassigned means events are arriving with an unexpected mode; "+
			"a step up right after a reassignment is the exclusion doing its job."),
		metric.WithUnit("{package}"),
	)
	if err != nil {
		return nil, err
	}
	return &AreaMetrics{skipped: skipped}, nil
}

// CarrierAreaSkipped counts one refused package.
func (m *AreaMetrics) CarrierAreaSkipped(ctx context.Context) {
	m.skipped.Add(ctx, 1)
}
