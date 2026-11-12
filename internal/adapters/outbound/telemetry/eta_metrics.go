package telemetry

import (
	"context"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/metric"

	"github.com/claudioed/last-mile-delivery/internal/application/ports"
)

// The two ETA counters of ADR 0013.
const (
	stopEtaAssignedName = "last_mile_delivery.stop_eta.assigned"
	stopEtaSkippedName  = "last_mile_delivery.stop_eta.skipped"
)

// EtaMetrics implements ports.EtaMetrics against the global MeterProvider.
type EtaMetrics struct {
	assigned metric.Int64Counter
	skipped  metric.Int64Counter
}

var _ ports.EtaMetrics = (*EtaMetrics)(nil)

// NewEtaMetrics registers the two counters. Call it after Setup so the
// instruments bind to the real MeterProvider.
func NewEtaMetrics() (*EtaMetrics, error) {
	meter := otel.Meter(meterName)
	assigned, err := meter.Int64Counter(
		stopEtaAssignedName,
		metric.WithDescription("StopEtaWindowAssigned events raised by StartRoute, one per stop of a started route. "+
			"A flat line while routes start means the wave windows leave less than 30 minutes at the start instant or the events are not raised."),
		metric.WithUnit("{stop}"),
	)
	if err != nil {
		return nil, err
	}
	skipped, err := meter.Int64Counter(
		stopEtaSkippedName,
		metric.WithDescription("Route starts that raised no ETA window, by reason (window_elapsed: less than 30 minutes of the wave's delivery window left). "+
			"The route starts normally; a rising count means routes start too late in the wave window to give shoppers an ETA."),
		metric.WithUnit("{route}"),
	)
	if err != nil {
		return nil, err
	}
	return &EtaMetrics{assigned: assigned, skipped: skipped}, nil
}

// EtaAssigned counts n ETA events of one committed start.
func (m *EtaMetrics) EtaAssigned(ctx context.Context, n int) {
	m.assigned.Add(ctx, int64(n))
}

// EtaSkipped counts a committed start that raised no ETA.
func (m *EtaMetrics) EtaSkipped(ctx context.Context, reason string) {
	m.skipped.Add(ctx, 1, metric.WithAttributes(reasonKey.String(reason)))
}
