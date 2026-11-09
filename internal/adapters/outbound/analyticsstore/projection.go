package analyticsstore

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/claudioed/last-mile-delivery/internal/analytics/report"
)

// Projection is the Postgres WRITER (report.Projection). Apply claims the
// event id and projects the fact in ONE transaction: the id is recorded if and
// only if its effect is, so a failure anywhere leaves nothing behind and the
// redelivered event is applied afresh, while a replay of an applied id is a
// no-op.
type Projection struct {
	pool *pgxpool.Pool
}

// NewProjection constructs a Projection over the writer pool.
func NewProjection(pool *pgxpool.Pool) *Projection { return &Projection{pool: pool} }

var _ report.Projection = (*Projection)(nil)

const claimSQL = `
	INSERT INTO analytics_processed_events (event_id, event_type, occurred_at)
	VALUES ($1, $2, $3)
	ON CONFLICT (event_id) DO NOTHING`

// The fact statements. Every one is order-independent: a later fact never
// loses to an earlier one that arrives after it.
const (
	upsertWaveSQL = `
	INSERT INTO waves (wave_id, station_node_id, service_date, stop_capacity, event_id)
	VALUES ($1, $2, $3::date, $4, $5)
	ON CONFLICT (wave_id) DO UPDATE
	SET station_node_id = EXCLUDED.station_node_id, service_date = EXCLUDED.service_date,
	    stop_capacity = EXCLUDED.stop_capacity, event_id = EXCLUDED.event_id`

	// assignedStops only grows: keep the highest value seen.
	upsertInductionSQL = `
	INSERT INTO wave_inductions (wave_id, assigned_stops, event_id)
	VALUES ($1, $2, $3)
	ON CONFLICT (wave_id) DO UPDATE
	SET assigned_stops = EXCLUDED.assigned_stops, event_id = EXCLUDED.event_id
	WHERE EXCLUDED.assigned_stops > wave_inductions.assigned_stops`

	insertCancellationSQL = `
	INSERT INTO wave_cancellations (wave_id, cancelled_at, event_id)
	VALUES ($1, $2, $3)
	ON CONFLICT (wave_id) DO NOTHING`

	// The last declaration wins (ADR 0011): by declaredAt, then event id.
	upsertCostSQL = `
	INSERT INTO wave_costs (wave_id, cost_per_stop_cents, declared_at, event_id)
	VALUES ($1, $2, $3, $4)
	ON CONFLICT (wave_id) DO UPDATE
	SET cost_per_stop_cents = EXCLUDED.cost_per_stop_cents, declared_at = EXCLUDED.declared_at, event_id = EXCLUDED.event_id
	WHERE (EXCLUDED.declared_at, EXCLUDED.event_id COLLATE "C") > (wave_costs.declared_at, wave_costs.event_id COLLATE "C")`

	insertRoutePlanSQL = `
	INSERT INTO route_plans (route_id, wave_id, dsp_id, stop_count, event_id)
	VALUES ($1, $2, $3, $4, $5)
	ON CONFLICT (route_id) DO NOTHING`

	insertRoutePackageSQL = `
	INSERT INTO route_packages (route_id, tracking_number, station_node_id, out_at, event_id)
	VALUES ($1, $2, $3, $4, $5)
	ON CONFLICT (route_id, tracking_number) DO NOTHING`

	insertAttemptSQL = `
	INSERT INTO delivery_attempts (event_id, route_id, tracking_number, outcome, attempted_at)
	VALUES ($1, $2, $3, $4, $5)`

	insertReturnSQL = `
	INSERT INTO package_returns (event_id, tracking_number, initiated_at)
	VALUES ($1, $2, $3)`
)

// Apply implements report.Projection.
func (p *Projection) Apply(ctx context.Context, e report.Event) (bool, error) {
	if err := e.Validate(); err != nil {
		return false, fmt.Errorf("%w: %w", report.ErrRejected, err)
	}
	stmt, args, ok := statementFor(e)
	if !ok {
		return false, fmt.Errorf("%w: %w %q", report.ErrRejected, errUnknownKind, e.Kind)
	}
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("analyticsstore: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }() // no-op once committed

	tag, err := tx.Exec(ctx, claimSQL, e.EventID, string(e.Kind), e.At)
	if err != nil {
		return false, classify(fmt.Errorf("analyticsstore: claim event %s: %w", e.EventID, err))
	}
	if tag.RowsAffected() == 0 {
		return false, nil // already applied: the deferred rollback ends the empty tx
	}
	if _, err := tx.Exec(ctx, stmt, args...); err != nil {
		return false, classify(fmt.Errorf("analyticsstore: project %s %s: %w", e.Kind, e.EventID, err))
	}
	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("analyticsstore: commit: %w", err)
	}
	return true, nil
}

// errUnknownKind is deterministic: no retry can make the store accept it.
var errUnknownKind = errors.New("unknown event kind")

// statementFor is the fact statement and its arguments for e's Kind.
func statementFor(e report.Event) (string, []any, bool) {
	switch e.Kind {
	case report.KindWaveScheduled:
		return upsertWaveSQL, []any{e.WaveID, e.StationNodeID, e.ServiceDate, e.StopCapacity, e.EventID}, true
	case report.KindWaveCapacityChanged:
		return upsertInductionSQL, []any{e.WaveID, e.AssignedStops, e.EventID}, true
	case report.KindWaveCancelled:
		return insertCancellationSQL, []any{e.WaveID, e.OccurredAt, e.EventID}, true
	case report.KindWaveCostDeclared:
		return upsertCostSQL, []any{e.WaveID, e.CostPerStopCents, e.OccurredAt, e.EventID}, true
	case report.KindRoutePlanned:
		return insertRoutePlanSQL, []any{e.RouteID, e.WaveID, e.DSPID, e.StopCount, e.EventID}, true
	case report.KindOutForDelivery:
		return insertRoutePackageSQL, []any{e.RouteID, e.TrackingNumber, e.StationNodeID, e.OccurredAt, e.EventID}, true
	case report.KindDeliveryAttempted:
		return insertAttemptSQL, []any{e.EventID, e.RouteID, e.TrackingNumber, e.Outcome, e.OccurredAt}, true
	case report.KindReturnInitiated:
		return insertReturnSQL, []any{e.EventID, e.TrackingNumber, e.OccurredAt}, true
	default:
		return "", nil, false
	}
}

// classify wraps a Postgres data-exception (SQLSTATE class 22) or
// integrity-violation (class 23) error in report.ErrRejected: the same event
// can never succeed, so the consumer dead-letters it instead of retrying
// forever. Everything else (connection loss, timeouts, locks, failovers, a
// raised exception) stays transient.
func classify(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && deterministicClass(pgErr.Code) {
		return fmt.Errorf("%w: %w", report.ErrRejected, err)
	}
	return err
}

// deterministicClass reports whether an SQLSTATE is in class 22 or 23.
func deterministicClass(code string) bool {
	return len(code) == 5 && (code[:2] == "22" || code[:2] == "23")
}
