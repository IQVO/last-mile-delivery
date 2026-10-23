package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/claudioed/last-mile-delivery/internal/application/ports"
	"github.com/claudioed/last-mile-delivery/internal/domain/partner"
	"github.com/claudioed/last-mile-delivery/internal/domain/route"
	"github.com/claudioed/last-mile-delivery/internal/domain/wave"
)

// RouteRepository is the pgx-backed ports.RouteRepository.
type RouteRepository struct{ pool *pgxpool.Pool }

// NewRouteRepository constructs the repository over pool.
func NewRouteRepository(pool *pgxpool.Pool) *RouteRepository { return &RouteRepository{pool} }

// Get returns the route with its stops and attempts, or ports.ErrNotFound.
func (r *RouteRepository) Get(ctx context.Context, id route.ID) (*route.Route, error) {
	q := queryFor(ctx, r.pool)
	var s route.Snapshot
	var rid, waveID, dsp, state string
	err := q.QueryRow(ctx, `SELECT route_id, wave_id, dsp_id, station_node_id, state, version FROM routes WHERE route_id = $1`, string(id)).
		Scan(&rid, &waveID, &dsp, &s.StationNodeID, &state, &s.Version)
	if err != nil {
		return nil, missOr(err)
	}
	s.ID, s.WaveID, s.DSPID, s.State = route.ID(rid), wave.ID(waveID), partner.ID(dsp), route.State(state)
	if s.Stops, err = loadStops(ctx, q, rid); err != nil {
		return nil, err
	}
	if s.Attempts, err = loadAttempts(ctx, q, rid); err != nil {
		return nil, err
	}
	return route.Restore(s)
}

func loadStops(ctx context.Context, q querier, routeID string) ([]route.Stop, error) {
	rows, err := q.Query(ctx, `SELECT stop_no, tracking_number FROM route_stop_packages WHERE route_id = $1 ORDER BY stop_no, position`, routeID)
	if err != nil {
		return nil, fmt.Errorf("load stops: %w", err)
	}
	defer rows.Close()
	var stops []route.Stop
	for rows.Next() {
		var no int
		var tn string
		if err := rows.Scan(&no, &tn); err != nil {
			return nil, fmt.Errorf("scan stop package: %w", err)
		}
		if len(stops) == 0 || stops[len(stops)-1].No != no {
			stops = append(stops, route.Stop{No: no})
		}
		last := &stops[len(stops)-1]
		last.TrackingNumbers = append(last.TrackingNumbers, tn)
	}
	return stops, rows.Err()
}

func loadAttempts(ctx context.Context, q querier, routeID string) ([]route.Attempt, error) {
	rows, err := q.Query(ctx, `SELECT tracking_number, outcome, COALESCE(failure_reason, ''), attempted_at
		FROM route_attempts WHERE route_id = $1 ORDER BY position`, routeID)
	if err != nil {
		return nil, fmt.Errorf("load attempts: %w", err)
	}
	defer rows.Close()
	var out []route.Attempt
	for rows.Next() {
		var a route.Attempt
		var outcome string
		if err := rows.Scan(&a.TrackingNumber, &outcome, &a.FailureReason, &a.At); err != nil {
			return nil, fmt.Errorf("scan attempt: %w", err)
		}
		a.Outcome = route.Outcome(outcome)
		out = append(out, a)
	}
	return out, rows.Err()
}

// PackagesInWave returns every tracking number of every route of the wave.
func (r *RouteRepository) PackagesInWave(ctx context.Context, id wave.ID) ([]string, error) {
	rows, err := queryFor(ctx, r.pool).Query(ctx, `SELECT p.tracking_number FROM route_stop_packages p
		JOIN routes r ON r.route_id = p.route_id WHERE r.wave_id = $1 ORDER BY p.tracking_number`, string(id))
	if err != nil {
		return nil, fmt.Errorf("packages in wave: %w", err)
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var tn string
		if err := rows.Scan(&tn); err != nil {
			return nil, err
		}
		out = append(out, tn)
	}
	return out, rows.Err()
}

// CountUncompleted returns how many routes of the wave are not COMPLETED.
func (r *RouteRepository) CountUncompleted(ctx context.Context, id wave.ID) (int, error) {
	var n int
	err := queryFor(ctx, r.pool).QueryRow(ctx, `SELECT count(*) FROM routes WHERE wave_id = $1 AND state <> 'COMPLETED'`, string(id)).Scan(&n)
	return n, err
}

// Save persists the route under the optimistic rule and appends the stops (on
// create) and the attempts not stored yet.
func (r *RouteRepository) Save(ctx context.Context, rt *route.Route) error {
	s := rt.Snapshot()
	return inTx(ctx, r.pool, func(q querier) error {
		if s.Version == 1 {
			if err := insertRoute(ctx, q, s); err != nil {
				return err
			}
		} else if err := updateRoute(ctx, q, s); err != nil {
			return err
		}
		var stored int
		if err := q.QueryRow(ctx, `SELECT count(*) FROM route_attempts WHERE route_id = $1`, string(s.ID)).Scan(&stored); err != nil {
			return fmt.Errorf("count attempts: %w", err)
		}
		for i := stored; i < len(s.Attempts); i++ {
			a := s.Attempts[i]
			if _, err := q.Exec(ctx, `INSERT INTO route_attempts (route_id, tracking_number, outcome, failure_reason, attempted_at, position)
				VALUES ($1, $2, $3, $4, $5, $6)`, string(s.ID), a.TrackingNumber, string(a.Outcome), nullable(a.FailureReason), a.At, i); err != nil {
				return fmt.Errorf("insert attempt: %w", err)
			}
		}
		return nil
	})
}

func insertRoute(ctx context.Context, q querier, s route.Snapshot) error {
	_, err := q.Exec(ctx, `INSERT INTO routes (route_id, wave_id, dsp_id, station_node_id, state, version) VALUES ($1, $2, $3, $4, $5, $6)`,
		string(s.ID), string(s.WaveID), string(s.DSPID), s.StationNodeID, string(s.State), s.Version)
	if isUniqueViolation(err) {
		return ports.ErrConcurrentUpdate
	}
	if err != nil {
		return err
	}
	for _, st := range s.Stops {
		if _, err := q.Exec(ctx, `INSERT INTO route_stops (route_id, stop_no) VALUES ($1, $2)`, string(s.ID), st.No); err != nil {
			return fmt.Errorf("insert stop: %w", err)
		}
		for i, tn := range st.TrackingNumbers {
			if _, err := q.Exec(ctx, `INSERT INTO route_stop_packages (route_id, stop_no, tracking_number, position) VALUES ($1, $2, $3, $4)`,
				string(s.ID), st.No, tn, i); err != nil {
				return fmt.Errorf("insert stop package: %w", err)
			}
		}
	}
	return nil
}

func updateRoute(ctx context.Context, q querier, s route.Snapshot) error {
	tag, err := q.Exec(ctx, `UPDATE routes SET state = $2, version = $3 WHERE route_id = $1 AND version = $3 - 1`, string(s.ID), string(s.State), s.Version)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ports.ErrConcurrentUpdate
	}
	return nil
}

// PartnerRepository is the pgx-backed ports.PartnerRepository.
type PartnerRepository struct{ pool *pgxpool.Pool }

// NewPartnerRepository constructs the repository over pool.
func NewPartnerRepository(pool *pgxpool.Pool) *PartnerRepository { return &PartnerRepository{pool} }

// Get returns the partner or ports.ErrNotFound.
func (r *PartnerRepository) Get(ctx context.Context, id partner.ID) (*partner.DeliveryServicePartner, error) {
	var s partner.Snapshot
	var pid, state string
	err := queryFor(ctx, r.pool).QueryRow(ctx, `SELECT dsp_id, station_node_id, van_capacity, state, version FROM delivery_service_partners WHERE dsp_id = $1`, string(id)).
		Scan(&pid, &s.StationNodeID, &s.VanCapacity, &state, &s.Version)
	if err != nil {
		return nil, missOr(err)
	}
	s.ID, s.State = partner.ID(pid), partner.State(state)
	return partner.Restore(s)
}

// Save persists the partner under the optimistic rule.
func (r *PartnerRepository) Save(ctx context.Context, p *partner.DeliveryServicePartner) error {
	s := p.Snapshot()
	q := queryFor(ctx, r.pool)
	if s.Version == 1 {
		_, err := q.Exec(ctx, `INSERT INTO delivery_service_partners (dsp_id, station_node_id, van_capacity, state, version) VALUES ($1, $2, $3, $4, $5)`,
			string(s.ID), s.StationNodeID, s.VanCapacity, string(s.State), s.Version)
		if isUniqueViolation(err) {
			return ports.ErrConcurrentUpdate
		}
		return err
	}
	tag, err := q.Exec(ctx, `UPDATE delivery_service_partners SET state = $2, version = $3 WHERE dsp_id = $1 AND version = $3 - 1`, string(s.ID), string(s.State), s.Version)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ports.ErrConcurrentUpdate
	}
	return nil
}
