// Package ports declares the application's OUT ports: interfaces only (enforced
// by internal/architecture). Their non-interface vocabulary is the two sentinels
// of errors.go, the filters of internal/application/repository and the read DTOs
// of internal/application/views.
//
// Optimistic concurrency: every aggregate Save expects the stored version to be
// aggregate.Version()-1; a create (Version() 1) expects that the id is not
// stored. Any mismatch, including an update of a row that no longer exists, is
// ErrConcurrentUpdate. Inside a UnitOfWork every repository joins the
// transaction carried by the ctx the unit handed to its function.
package ports

import (
	"context"
	"time"

	"github.com/claudioed/last-mile-delivery/internal/application/outbox"
	"github.com/claudioed/last-mile-delivery/internal/application/repository"
	"github.com/claudioed/last-mile-delivery/internal/application/views"
	"github.com/claudioed/last-mile-delivery/internal/domain/history"
	"github.com/claudioed/last-mile-delivery/internal/domain/partner"
	"github.com/claudioed/last-mile-delivery/internal/domain/route"
	"github.com/claudioed/last-mile-delivery/internal/domain/shared"
	"github.com/claudioed/last-mile-delivery/internal/domain/wave"
)

// WaveRepository persists DeliveryWave aggregates with their inductions.
type WaveRepository interface {
	// Get returns the wave with every induction, or ErrNotFound.
	Get(ctx context.Context, id wave.ID) (*wave.DeliveryWave, error)
	// List returns the waves matching filter ordered by inductCutoffAt then
	// waveId ascending; no match is an empty list, never an error.
	List(ctx context.Context, filter repository.WaveFilter) ([]*wave.DeliveryWave, error)
	// Save persists the wave under the concurrency rule of the package
	// comment. An adapter stores the inductions the wave holds that are not
	// stored yet and never deletes one.
	Save(ctx context.Context, w *wave.DeliveryWave) error
}

// RouteRepository persists Route aggregates with their stops and attempts.
type RouteRepository interface {
	// Get returns the route, or ErrNotFound.
	Get(ctx context.Context, id route.ID) (*route.Route, error)
	// PackagesInWave returns every tracking number held by any route of the
	// wave (empty when none), ordered ascending.
	PackagesInWave(ctx context.Context, id wave.ID) ([]string, error)
	// CountUncompleted returns how many routes of the wave are not COMPLETED.
	CountUncompleted(ctx context.Context, id wave.ID) (int, error)
	// Save persists the route under the concurrency rule of the package
	// comment; an adapter appends the attempts not stored yet.
	Save(ctx context.Context, r *route.Route) error
}

// PartnerRepository persists DeliveryServicePartner aggregates.
type PartnerRepository interface {
	// Get returns the partner, or ErrNotFound.
	Get(ctx context.Context, id partner.ID) (*partner.DeliveryServicePartner, error)
	// Save persists the partner under the concurrency rule of the package comment.
	Save(ctx context.Context, p *partner.DeliveryServicePartner) error
}

// HistoryRepository persists PackageAttemptHistory aggregates by tracking number.
type HistoryRepository interface {
	// Get returns the history of the tracking number, or ErrNotFound.
	Get(ctx context.Context, trackingNumber string) (*history.History, error)
	// ListWaiting returns at most limit histories of the station in status
	// AT_STATION ordered by arrivedAt then trackingNumber ascending.
	ListWaiting(ctx context.Context, stationNodeID string, limit int) ([]*history.History, error)
	// Save persists the history under the concurrency rule of the package comment.
	Save(ctx context.Context, h *history.History) error
}

// StationRepository is the StationView read model fed by NodeRegistered.
type StationRepository interface {
	// Get returns the delivery station, or ErrNotFound.
	Get(ctx context.Context, nodeID string) (views.Station, error)
	// Save stores the station; saving a known node is a no-op.
	Save(ctx context.Context, s views.Station) error
}

// ServiceAreaRepository is the StationView read model fed by ServiceAreaAssigned.
type ServiceAreaRepository interface {
	// Get returns the assignment of the prefix, or ErrNotFound.
	Get(ctx context.Context, postalPrefix string) (views.ServiceArea, error)
	// Save stores the assignment only when its version is higher than the
	// stored one (or none is stored) and reports whether it did.
	Save(ctx context.Context, a views.ServiceArea) (bool, error)
}

// PackageStopRepository is the PackageStopView read model fed by LabelIssued.
type PackageStopRepository interface {
	// Get returns the label data of the package, or ErrNotFound.
	Get(ctx context.Context, trackingNumber string) (views.PackageStop, error)
	// Save stores the label data; the first stored values of a package win.
	Save(ctx context.Context, s views.PackageStop) error
}

// InboundPackageRepository is the InboundPackagesView read model fed by LoadArrived.
type InboundPackageRepository interface {
	// Get returns the arrival of the package in the load, or ErrNotFound.
	Get(ctx context.Context, loadID, trackingNumber string) (views.InboundPackage, error)
	// Save records an arrival; a (load, package) pair already stored is a no-op.
	Save(ctx context.Context, p views.InboundPackage) error
}

// Inbox makes every consumer idempotent on the CloudEvents id.
type Inbox interface {
	// Claim records the event id as processed inside the current unit of work
	// and reports whether it was new; false means it was applied before.
	Claim(ctx context.Context, eventID, eventType string) (bool, error)
}

// EventOutbox is the write side of the transactional outbox. Called with the ctx
// handed to UnitOfWork.Do it joins the SAME transaction as the aggregate Save
// calls, so the aggregates and their raised events commit or roll back together.
// The adapter encodes each event into its CloudEvents envelope (the id is minted
// once, at append) and stores one row per event in order; a relay drains the rows
// to Kafka afterwards. Appending no events is a no-op.
type EventOutbox interface {
	Append(ctx context.Context, events ...shared.Event) error
}

// EventEncoder turns domain events into the already-encoded outbox messages (the
// CloudEvents envelope, the topic and the Kafka key). The CloudEvents id is
// minted here, once, and stored with the row.
type EventEncoder interface {
	Encode(events ...shared.Event) ([]outbox.Message, error)
}

// UnitOfWork runs fn as ONE atomic unit: every repository and outbox call fn
// makes with the ctx it is handed commits together when fn returns nil, or rolls
// back together when fn returns an error (or panics).
//
//   - fn MUST use the ctx passed to it for every call that should participate.
//   - Do is re-entrant: when ctx already carries an open unit, Do joins it and
//     the outermost Do owns commit and rollback.
//   - The error fn returns is returned from Do unchanged (errors.Is keeps
//     working); a commit failure is returned wrapped.
type UnitOfWork interface {
	Do(ctx context.Context, fn func(ctx context.Context) error) error
}

// Clock is the service's notion of "now"; use cases stamp aggregates with it.
type Clock interface {
	Now() time.Time
}

// IDGenerator mints the opaque ids of new aggregates (ADR 0002, A27).
type IDGenerator interface {
	NewWaveID() wave.ID
	NewRouteID() route.ID
	NewPartnerID() partner.ID
}
