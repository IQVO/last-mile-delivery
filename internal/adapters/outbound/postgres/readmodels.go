package postgres

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/claudioed/last-mile-delivery/internal/application/views"
)

// StationRepository is the pgx-backed ports.StationRepository (rm_stations).
type StationRepository struct{ pool *pgxpool.Pool }

// NewStationRepository constructs the repository over pool.
func NewStationRepository(pool *pgxpool.Pool) *StationRepository { return &StationRepository{pool} }

// Get returns the station or ports.ErrNotFound.
func (r *StationRepository) Get(ctx context.Context, nodeID string) (views.Station, error) {
	var s views.Station
	err := queryFor(ctx, r.pool).QueryRow(ctx, `SELECT node_id, node_type, COALESCE(region_id, ''), COALESCE(timezone, '') FROM rm_stations WHERE node_id = $1`, nodeID).
		Scan(&s.NodeID, &s.NodeType, &s.RegionID, &s.Timezone)
	return s, missOr(err)
}

// Save stores the station; a known node is a no-op.
func (r *StationRepository) Save(ctx context.Context, s views.Station) error {
	_, err := queryFor(ctx, r.pool).Exec(ctx, `INSERT INTO rm_stations (node_id, node_type, region_id, timezone) VALUES ($1, $2, $3, $4)
		ON CONFLICT (node_id) DO NOTHING`, s.NodeID, s.NodeType, nullable(s.RegionID), nullable(s.Timezone))
	return err
}

// ServiceAreaRepository is the pgx-backed ports.ServiceAreaRepository.
type ServiceAreaRepository struct{ pool *pgxpool.Pool }

// NewServiceAreaRepository constructs the repository over pool.
func NewServiceAreaRepository(pool *pgxpool.Pool) *ServiceAreaRepository {
	return &ServiceAreaRepository{pool}
}

// Get returns the assignment or ports.ErrNotFound.
func (r *ServiceAreaRepository) Get(ctx context.Context, prefix string) (views.ServiceArea, error) {
	var a views.ServiceArea
	err := queryFor(ctx, r.pool).QueryRow(ctx, `SELECT postal_prefix, station_node_id, COALESCE(region_id, ''), assignment_version
		FROM rm_station_service_areas WHERE postal_prefix = $1`, prefix).Scan(&a.PostalPrefix, &a.StationNodeID, &a.RegionID, &a.AssignmentVersion)
	return a, missOr(err)
}

// Save stores the assignment only when its version is higher than the stored one.
func (r *ServiceAreaRepository) Save(ctx context.Context, a views.ServiceArea) (bool, error) {
	tag, err := queryFor(ctx, r.pool).Exec(ctx, `INSERT INTO rm_station_service_areas (postal_prefix, station_node_id, region_id, assignment_version)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (postal_prefix) DO UPDATE SET station_node_id = EXCLUDED.station_node_id, region_id = EXCLUDED.region_id,
			assignment_version = EXCLUDED.assignment_version
		WHERE rm_station_service_areas.assignment_version < EXCLUDED.assignment_version`,
		a.PostalPrefix, a.StationNodeID, nullable(a.RegionID), a.AssignmentVersion)
	return tag.RowsAffected() == 1, err
}

// PackageStopRepository is the pgx-backed ports.PackageStopRepository.
type PackageStopRepository struct{ pool *pgxpool.Pool }

// NewPackageStopRepository constructs the repository over pool.
func NewPackageStopRepository(pool *pgxpool.Pool) *PackageStopRepository {
	return &PackageStopRepository{pool}
}

// Get returns the label data or ports.ErrNotFound.
func (r *PackageStopRepository) Get(ctx context.Context, trackingNumber string) (views.PackageStop, error) {
	var p views.PackageStop
	err := queryFor(ctx, r.pool).QueryRow(ctx, `SELECT tracking_number, stop_key, COALESCE(destination_postal_prefix, ''), COALESCE(assignment_id, '')
		FROM rm_package_stops WHERE tracking_number = $1`, trackingNumber).Scan(&p.TrackingNumber, &p.StopKey, &p.DestinationPostalPrefix, &p.AssignmentID)
	return p, missOr(err)
}

// Save stores the label data; the first stored values win.
func (r *PackageStopRepository) Save(ctx context.Context, p views.PackageStop) error {
	_, err := queryFor(ctx, r.pool).Exec(ctx, `INSERT INTO rm_package_stops (tracking_number, stop_key, destination_postal_prefix, assignment_id)
		VALUES ($1, $2, $3, $4) ON CONFLICT (tracking_number) DO NOTHING`,
		p.TrackingNumber, p.StopKey, nullable(p.DestinationPostalPrefix), nullable(p.AssignmentID))
	return err
}

// InboundPackageRepository is the pgx-backed ports.InboundPackageRepository.
type InboundPackageRepository struct{ pool *pgxpool.Pool }

// NewInboundPackageRepository constructs the repository over pool.
func NewInboundPackageRepository(pool *pgxpool.Pool) *InboundPackageRepository {
	return &InboundPackageRepository{pool}
}

// Get returns the arrival or ports.ErrNotFound.
func (r *InboundPackageRepository) Get(ctx context.Context, loadID, trackingNumber string) (views.InboundPackage, error) {
	var p views.InboundPackage
	err := queryFor(ctx, r.pool).QueryRow(ctx, `SELECT load_id, tracking_number, COALESCE(departure_id, ''), station_node_id, arrived_at
		FROM rm_inbound_packages WHERE load_id = $1 AND tracking_number = $2`, loadID, trackingNumber).
		Scan(&p.LoadID, &p.TrackingNumber, &p.DepartureID, &p.StationNodeID, &p.ArrivedAt)
	return p, missOr(err)
}

// Save records an arrival; a stored (load, package) pair is a no-op.
func (r *InboundPackageRepository) Save(ctx context.Context, p views.InboundPackage) error {
	_, err := queryFor(ctx, r.pool).Exec(ctx, `INSERT INTO rm_inbound_packages (load_id, tracking_number, departure_id, station_node_id, arrived_at)
		VALUES ($1, $2, $3, $4, $5) ON CONFLICT (load_id, tracking_number) DO NOTHING`,
		p.LoadID, p.TrackingNumber, nullable(p.DepartureID), p.StationNodeID, p.ArrivedAt)
	return err
}

// Inbox is the pgx-backed ports.Inbox (processed_events).
type Inbox struct{ pool *pgxpool.Pool }

// NewInbox constructs the inbox over pool.
func NewInbox(pool *pgxpool.Pool) *Inbox { return &Inbox{pool} }

// Claim records the CloudEvents id; called inside a unit of work the INSERT is
// part of the SAME transaction as the effect, so a rollback un-claims it. A
// concurrent claim of the same id blocks on the primary key until the other
// transaction ends.
func (i *Inbox) Claim(ctx context.Context, eventID, eventType string) (bool, error) {
	tag, err := queryFor(ctx, i.pool).Exec(ctx, `INSERT INTO processed_events (event_id, event_type) VALUES ($1, $2) ON CONFLICT (event_id) DO NOTHING`, eventID, eventType)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}
