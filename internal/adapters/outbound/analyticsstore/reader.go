package analyticsstore

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/claudioed/last-mile-delivery/internal/analytics/report"
)

// Reader is the Postgres READER (report.Reader). Every query filters the
// business instant with `>= from AND < to`, and days are UTC calendar days.
// SQL only counts and sums; the outcome vocabulary and the unknown station
// are passed in from internal/analytics/report, and every derived figure
// (means, rates) comes from there.
type Reader struct {
	pool *pgxpool.Pool
}

// NewReader constructs a Reader over the (read-only) reports pool.
func NewReader(pool *pgxpool.Pool) *Reader { return &Reader{pool: pool} }

var _ report.Reader = (*Reader)(nil)

// routeSQL: per route its packages and how many have an attempt on it, and its
// last attempt; the completed ones (report.IsCompletedRoute: every package
// attempted) of the range with their plan and their wave's declared cost;
// then per day and station the counts and sums.
//
//	$1 from, $2 to
const routeSQL = `
	WITH per_package AS (
		SELECT route_id, tracking_number, max(attempted_at) AS last_at
		FROM delivery_attempts GROUP BY route_id, tracking_number
	), per_route AS (
		SELECT p.route_id, min(p.station_node_id) AS station, count(*) AS packages,
		       count(a.tracking_number) AS attempted, max(a.last_at) AS completed_at
		FROM route_packages p
		LEFT JOIN per_package a ON a.route_id = p.route_id AND a.tracking_number = p.tracking_number
		GROUP BY p.route_id
	), completed AS (
		SELECT (r.completed_at AT TIME ZONE 'UTC')::date AS day, r.station, r.packages, pl.stop_count,
		       c.cost_per_stop_cents AS cost
		FROM per_route r
		JOIN route_plans pl ON pl.route_id = r.route_id
		LEFT JOIN wave_costs c ON c.wave_id = pl.wave_id
		WHERE r.packages > 0 AND r.attempted = r.packages AND r.completed_at >= $1 AND r.completed_at < $2
	)
	SELECT day, station, count(*), sum(stop_count)::bigint, sum(packages)::bigint,
	       COALESCE(sum(stop_count) FILTER (WHERE cost IS NOT NULL), 0)::bigint,
	       COALESCE(sum(packages) FILTER (WHERE cost IS NOT NULL), 0)::bigint,
	       COALESCE(sum(cost * stop_count) FILTER (WHERE cost IS NOT NULL), 0)::bigint
	FROM completed
	GROUP BY day, station
	ORDER BY day, station COLLATE "C"`

// RouteEfficiency implements report.Reader.
func (r *Reader) RouteEfficiency(ctx context.Context, rg report.Range) ([]report.RouteDay, error) {
	return collect(ctx, r.pool, routeSQL, func(rows pgx.Rows) (report.RouteDay, error) {
		var d report.RouteDay
		err := rows.Scan(&d.Day, &d.Station, &d.Routes, &d.Stops, &d.Packages, &d.CostedStops, &d.CostedPackages, &d.CostedCents)
		d.Day = d.Day.UTC()
		return d, err
	}, rg.From, rg.To)
}

// attemptSQL: every attempt with its route's station (UNKNOWN until the
// out-for-delivery fact is projected); each package's first and latest attempt
// (by instant, then event id) and its attempt count; then three facets per day
// and station, summed: the attempts of the day by outcome, the packages whose
// first attempt fell on the day, and the returns initiated that day at the
// station of the package's latest attempt. A success is DELIVERED or COLLECTED
// (ADR 0017); both count in deliveredAttempts and a collected first attempt is
// a first-attempt success.
//
//	$1 from, $2 to, $3 unknown station, $4 DELIVERED, $5 FAILED, $6 COLLECTED
const attemptSQL = `
	WITH attempts AS (
		SELECT a.event_id, a.tracking_number, a.outcome, a.attempted_at, COALESCE(p.station_node_id, $3) AS station
		FROM delivery_attempts a
		LEFT JOIN route_packages p ON p.route_id = a.route_id AND p.tracking_number = a.tracking_number
	), firsts AS (
		SELECT DISTINCT ON (tracking_number) tracking_number, outcome, attempted_at, station
		FROM attempts ORDER BY tracking_number, attempted_at, event_id COLLATE "C"
	), latest AS (
		SELECT DISTINCT ON (tracking_number) tracking_number, station
		FROM attempts ORDER BY tracking_number, attempted_at DESC, event_id COLLATE "C" DESC
	), totals AS (
		SELECT tracking_number, count(*) AS n FROM delivery_attempts GROUP BY tracking_number
	), facets AS (
		SELECT (attempted_at AT TIME ZONE 'UTC')::date AS day, station,
		       (outcome IN ($4, $6))::int AS delivered, (outcome = $5)::int AS failed,
		       0 AS packages, 0 AS first_ok, 0::bigint AS package_attempts, 0 AS returns
		FROM attempts WHERE attempted_at >= $1 AND attempted_at < $2
		UNION ALL
		SELECT (f.attempted_at AT TIME ZONE 'UTC')::date, f.station, 0, 0, 1, (f.outcome IN ($4, $6))::int, t.n, 0
		FROM firsts f JOIN totals t ON t.tracking_number = f.tracking_number
		WHERE f.attempted_at >= $1 AND f.attempted_at < $2
		UNION ALL
		SELECT (r.initiated_at AT TIME ZONE 'UTC')::date, COALESCE(l.station, $3), 0, 0, 0, 0, 0, 1
		FROM package_returns r LEFT JOIN latest l ON l.tracking_number = r.tracking_number
		WHERE r.initiated_at >= $1 AND r.initiated_at < $2
	)
	SELECT day, station, sum(delivered)::bigint, sum(failed)::bigint, sum(packages)::bigint,
	       sum(first_ok)::bigint, sum(package_attempts)::bigint, sum(returns)::bigint
	FROM facets
	GROUP BY day, station
	ORDER BY day, station COLLATE "C"`

// AttemptOutcomes implements report.Reader.
func (r *Reader) AttemptOutcomes(ctx context.Context, rg report.Range) ([]report.AttemptDay, error) {
	return collect(ctx, r.pool, attemptSQL, func(rows pgx.Rows) (report.AttemptDay, error) {
		var d report.AttemptDay
		err := rows.Scan(&d.Day, &d.Station, &d.DeliveredAttempts, &d.FailedAttempts, &d.PackagesAttempted,
			&d.PackagesFirstAttemptSuccess, &d.AttemptsOfPackagesAttempted, &d.ReturnsInitiated)
		d.Day = d.Day.UTC()
		return d, err
	}, rg.From, rg.To, report.UnknownStation(), report.OutcomeDelivered, report.OutcomeFailed, report.OutcomeCollected)
}

// waveSQL: the waves whose service date is in the range, per service date and
// station: the count, the cancelled ones, the scheduled capacity and the
// inducted stops (0 for a wave without an induction).
//
//	$1 first date, $2 the date after the last
const waveSQL = `
	SELECT w.service_date, w.station_node_id, count(*), count(c.wave_id),
	       sum(w.stop_capacity)::bigint, COALESCE(sum(i.assigned_stops), 0)::bigint
	FROM waves w
	LEFT JOIN wave_cancellations c ON c.wave_id = w.wave_id
	LEFT JOIN wave_inductions i ON i.wave_id = w.wave_id
	WHERE w.service_date >= $1::date AND w.service_date < $2::date
	GROUP BY w.service_date, w.station_node_id
	ORDER BY w.service_date, w.station_node_id COLLATE "C"`

// WaveUtilisation implements report.Reader.
func (r *Reader) WaveUtilisation(ctx context.Context, rg report.Range) ([]report.WaveDay, error) {
	return collect(ctx, r.pool, waveSQL, func(rows pgx.Rows) (report.WaveDay, error) {
		var d report.WaveDay
		err := rows.Scan(&d.Day, &d.Station, &d.WavesScheduled, &d.WavesCancelled, &d.StopCapacity, &d.StopsInducted)
		d.Day = time.Date(d.Day.Year(), d.Day.Month(), d.Day.Day(), 0, 0, 0, 0, time.UTC)
		return d, err
	}, rg.From, rg.To)
}

// LastEventAt implements report.Reader: the newest CloudEvents time among the
// applied events, nil while none was applied.
func (r *Reader) LastEventAt(ctx context.Context) (*time.Time, error) {
	var at *time.Time
	if err := r.pool.QueryRow(ctx, `SELECT max(occurred_at) FROM analytics_processed_events`).Scan(&at); err != nil {
		return nil, fmt.Errorf("analyticsstore: last event: %w", err)
	}
	if at != nil {
		utc := at.UTC()
		at = &utc
	}
	return at, nil
}

// Ping checks the analytical database answers (the reports' /readyz).
func (r *Reader) Ping(ctx context.Context) error { return r.pool.Ping(ctx) }

// collect runs query with args and scans every row; the result is a non-nil
// slice even when empty.
func collect[T any](ctx context.Context, pool *pgxpool.Pool, query string, scan func(pgx.Rows) (T, error), args ...any) ([]T, error) {
	rows, err := pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("analyticsstore: query: %w", err)
	}
	defer rows.Close()
	out := []T{}
	for rows.Next() {
		v, err := scan(rows)
		if err != nil {
			return nil, fmt.Errorf("analyticsstore: scan: %w", err)
		}
		out = append(out, v)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("analyticsstore: rows: %w", err)
	}
	return out, nil
}
