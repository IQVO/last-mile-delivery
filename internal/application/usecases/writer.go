// Package usecases holds the application's use cases, one per operation of
// apis/openapi.yaml and per consumed event. Every command follows one shape
// inside ONE ports.UnitOfWork: load the aggregates, call the domain, save them
// and append the raised events to the outbox, then return a view. Domain, shared
// and port errors pass through unchanged (errors.Is keeps working): the HTTP
// adapter maps them to problem types.
package usecases

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/claudioed/last-mile-delivery/internal/application/ports"
	"github.com/claudioed/last-mile-delivery/internal/application/views"
	"github.com/claudioed/last-mile-delivery/internal/domain/shared"
)

// ErrUnknownStation: the node is not a known DELIVERY_STATION of the StationView
// read model (problem type unknown-station, 422; ADR 0002, A1).
var ErrUnknownStation = errors.New("unknown station")

// Deps bundles the ports the use cases need.
type Deps struct {
	Waves        ports.WaveRepository
	Routes       ports.RouteRepository
	Partners     ports.PartnerRepository
	Histories    ports.HistoryRepository
	Stations     ports.StationRepository
	ServiceAreas ports.ServiceAreaRepository
	PackageStops ports.PackageStopRepository
	Inbound      ports.InboundPackageRepository
	Inbox        ports.Inbox
	Outbox       ports.EventOutbox
	UoW          ports.UnitOfWork
	Clock        ports.Clock
	IDs          ports.IDGenerator
	// Metrics records the business outcome of each delivery attempt; nil
	// records nothing, so unit tests need no wiring.
	Metrics ports.AttemptMetrics
}

// metrics returns the configured AttemptMetrics or a no-op.
func (d Deps) metrics() ports.AttemptMetrics {
	if d.Metrics != nil {
		return d.Metrics
	}
	return noMetrics{}
}

// noMetrics is the AttemptMetrics of a Deps without one.
type noMetrics struct{}

func (noMetrics) AttemptDelivered(context.Context)        {}
func (noMetrics) AttemptFailed(context.Context)           {}
func (noMetrics) AttemptRejected(context.Context, string) {}

// writer is what every use case embeds.
type writer struct{ Deps }

// now returns the clock's current time in UTC.
func (w writer) now() time.Time { return w.Clock.Now().UTC() }

// publish appends the raised events to the outbox, on the ctx of the unit of work.
func (w writer) publish(ctx context.Context, events []shared.Event) error {
	if err := w.Outbox.Append(ctx, events...); err != nil {
		return fmt.Errorf("append events to outbox: %w", err)
	}
	return nil
}

// transact runs attempt in a unit of work. A ports.ErrConcurrentUpdate means
// another writer got in between the load and the save, so the whole attempt
// (load, domain call, save, outbox) runs once more on fresh state before the
// conflict is reported.
func transact[T any](ctx context.Context, uow ports.UnitOfWork, attempt func(ctx context.Context) (T, error)) (T, error) {
	var out T
	run := func(ctx context.Context) error {
		v, err := attempt(ctx)
		if err != nil {
			return err
		}
		out = v
		return nil
	}
	err := uow.Do(ctx, run)
	if errors.Is(err, ports.ErrConcurrentUpdate) {
		err = uow.Do(ctx, run)
	}
	if err != nil {
		var zero T
		return zero, err
	}
	return out, nil
}

// found reports whether a lookup hit: a nil error is a hit, ports.ErrNotFound a
// miss, anything else a failure.
func found(err error) (bool, error) {
	if err == nil {
		return true, nil
	}
	if errors.Is(err, ports.ErrNotFound) {
		return false, nil
	}
	return false, err
}

// requireStation fails with ErrUnknownStation unless the node is a known
// DELIVERY_STATION (ADR 0002, A1).
func (w writer) requireStation(ctx context.Context, nodeID string) error {
	st, err := w.Stations.Get(ctx, nodeID)
	ok, err := found(err)
	if err != nil {
		return err
	}
	if !ok || st.NodeType != views.DeliveryStation {
		return fmt.Errorf("%w: %q", ErrUnknownStation, nodeID)
	}
	return nil
}
