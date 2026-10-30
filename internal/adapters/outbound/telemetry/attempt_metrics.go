package telemetry

import (
	"context"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/claudioed/last-mile-delivery/internal/application/ports"
)

// meterName scopes this service's own instruments, keeping them distinct from
// the ones otelchi and the runtime collector register.
const meterName = "github.com/claudioed/last-mile-delivery"

// packagesAttemptedName is the Tier 2 business metric of the context (ADR 0008,
// the fleet's standard metrics convention): every delivery attempt a driver
// reports, by outcome.
const packagesAttemptedName = "last_mile_delivery.package.attempted"

const (
	outcomeKey = attribute.Key("outcome")
	reasonKey  = attribute.Key("reason")

	outcomeDelivered = "delivered"
	outcomeFailed    = "failed"
	outcomeRejected  = "rejected"
)

// AttemptMetrics implements ports.AttemptMetrics against the global
// MeterProvider. Until Setup installs a real provider the global one is a
// no-op, so recording is cheap and safe in tests and local runs.
type AttemptMetrics struct {
	counter metric.Int64Counter
}

var _ ports.AttemptMetrics = (*AttemptMetrics)(nil)

// NewAttemptMetrics registers the counter. It only fails if the instrument name
// is invalid, which is a programming error, not a runtime condition. Call it
// after Setup so the instrument binds to the real MeterProvider.
func NewAttemptMetrics() (*AttemptMetrics, error) {
	counter, err := otel.Meter(meterName).Int64Counter(
		packagesAttemptedName,
		metric.WithDescription("Delivery attempts reported on routes, by outcome (delivered, failed or rejected; reason says why a report "+
			"was rejected, as a problem slug such as route-not-started or already-delivered). "+
			"A falling delivered share with a rising failed share means packages are not reaching recipients on the first van run "+
			"(each package gets at most three failed attempts before it returns to its sender); "+
			"a rising rejected share means driver or dispatch clients report attempts the route cannot take, "+
			"and a rising internal-error reason means the service itself is failing."),
		metric.WithUnit("{package}"),
	)
	if err != nil {
		return nil, err
	}
	return &AttemptMetrics{counter: counter}, nil
}

// AttemptDelivered counts a recorded attempt that delivered the package.
func (m *AttemptMetrics) AttemptDelivered(ctx context.Context) {
	m.counter.Add(ctx, 1, metric.WithAttributes(outcomeKey.String(outcomeDelivered)))
}

// AttemptFailed counts a recorded attempt that did not deliver the package.
func (m *AttemptMetrics) AttemptFailed(ctx context.Context) {
	m.counter.Add(ctx, 1, metric.WithAttributes(outcomeKey.String(outcomeFailed)))
}

// AttemptRejected counts an attempt report that recorded nothing, with the
// reason why.
func (m *AttemptMetrics) AttemptRejected(ctx context.Context, reason string) {
	m.counter.Add(ctx, 1, metric.WithAttributes(outcomeKey.String(outcomeRejected), reasonKey.String(reason)))
}
