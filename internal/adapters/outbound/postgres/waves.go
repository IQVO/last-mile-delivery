package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/claudioed/last-mile-delivery/internal/application/ports"
	"github.com/claudioed/last-mile-delivery/internal/application/repository"
	"github.com/claudioed/last-mile-delivery/internal/domain/wave"
)

// WaveRepository is the pgx-backed ports.WaveRepository.
type WaveRepository struct{ pool *pgxpool.Pool }

// NewWaveRepository constructs the repository over pool.
func NewWaveRepository(pool *pgxpool.Pool) *WaveRepository { return &WaveRepository{pool} }

const waveColumns = `wave_id, station_node_id, service_date::text, induct_cutoff_at, window_start, window_end,
	stop_capacity, assigned_stops, state, COALESCE(cancel_reason, ''), version`

func scanWave(row interface{ Scan(...any) error }) (wave.Snapshot, error) {
	var s wave.Snapshot
	var id, state string
	err := row.Scan(&id, &s.StationNodeID, &s.ServiceDate, &s.InductCutoffAt, &s.WindowStart, &s.WindowEnd,
		&s.StopCapacity, &s.AssignedStops, &state, &s.CancelReason, &s.Version)
	s.ID, s.State = wave.ID(id), wave.State(state)
	return s, err
}

// Get returns the wave with its inductions, or ports.ErrNotFound.
func (r *WaveRepository) Get(ctx context.Context, id wave.ID) (*wave.DeliveryWave, error) {
	q := queryFor(ctx, r.pool)
	snap, err := scanWave(q.QueryRow(ctx, `SELECT `+waveColumns+` FROM delivery_waves WHERE wave_id = $1`, string(id)))
	if err != nil {
		return nil, missOr(err)
	}
	inductions, err := loadInductions(ctx, q, []string{string(id)})
	if err != nil {
		return nil, err
	}
	snap.Inductions = inductions[string(id)]
	return wave.Restore(snap)
}

func loadInductions(ctx context.Context, q querier, ids []string) (map[string][]wave.Induction, error) {
	rows, err := q.Query(ctx, `
		SELECT wave_id, tracking_number, stop_key, inducted_at FROM wave_inductions
		WHERE wave_id = ANY($1) ORDER BY wave_id, position`, ids)
	if err != nil {
		return nil, fmt.Errorf("load inductions: %w", err)
	}
	defer rows.Close()
	out := map[string][]wave.Induction{}
	for rows.Next() {
		var id string
		var in wave.Induction
		if err := rows.Scan(&id, &in.TrackingNumber, &in.StopKey, &in.At); err != nil {
			return nil, fmt.Errorf("scan induction: %w", err)
		}
		out[id] = append(out[id], in)
	}
	return out, rows.Err()
}

// List returns the matching waves ordered by cutoff then id.
func (r *WaveRepository) List(ctx context.Context, f repository.WaveFilter) ([]*wave.DeliveryWave, error) {
	q := queryFor(ctx, r.pool)
	var after any
	if !f.InductCutoffAfter.IsZero() {
		after = f.InductCutoffAfter
	}
	rows, err := q.Query(ctx, `SELECT `+waveColumns+` FROM delivery_waves
		WHERE ($1 = '' OR station_node_id = $1) AND ($2 = '' OR service_date::text = $2) AND ($3 = '' OR state = $3)
		  AND ($4::timestamptz IS NULL OR induct_cutoff_at > $4)
		ORDER BY induct_cutoff_at, wave_id`, f.StationNodeID, f.ServiceDate, string(f.State), after)
	if err != nil {
		return nil, fmt.Errorf("list waves: %w", err)
	}
	var snaps []wave.Snapshot
	var ids []string
	for rows.Next() {
		s, err := scanWave(rows)
		if err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan wave: %w", err)
		}
		snaps = append(snaps, s)
		ids = append(ids, string(s.ID))
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]*wave.DeliveryWave, 0, len(snaps))
	if len(snaps) == 0 {
		return out, nil
	}
	inductions, err := loadInductions(ctx, q, ids)
	if err != nil {
		return nil, err
	}
	for _, s := range snaps {
		s.Inductions = inductions[string(s.ID)]
		w, err := wave.Restore(s)
		if err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, nil
}

// Save persists the wave under the optimistic rule of the ports package and
// appends the inductions not stored yet.
func (r *WaveRepository) Save(ctx context.Context, w *wave.DeliveryWave) error {
	s := w.Snapshot()
	return inTx(ctx, r.pool, func(q querier) error {
		if err := writeWaveRow(ctx, q, s); err != nil {
			return err
		}
		var stored int
		if err := q.QueryRow(ctx, `SELECT count(*) FROM wave_inductions WHERE wave_id = $1`, string(s.ID)).Scan(&stored); err != nil {
			return fmt.Errorf("count inductions: %w", err)
		}
		for i := stored; i < len(s.Inductions); i++ {
			in := s.Inductions[i]
			if _, err := q.Exec(ctx, `INSERT INTO wave_inductions (wave_id, tracking_number, stop_key, inducted_at, position)
				VALUES ($1, $2, $3, $4, $5)`, string(s.ID), in.TrackingNumber, in.StopKey, in.At, i); err != nil {
				return fmt.Errorf("insert induction: %w", err)
			}
		}
		return nil
	})
}

func writeWaveRow(ctx context.Context, q querier, s wave.Snapshot) error {
	if s.Version == 1 {
		_, err := q.Exec(ctx, `INSERT INTO delivery_waves (wave_id, station_node_id, service_date, induct_cutoff_at, window_start, window_end,
			stop_capacity, assigned_stops, state, cancel_reason, version) VALUES ($1, $2, $3::date, $4, $5, $6, $7, $8, $9, $10, $11)`,
			string(s.ID), s.StationNodeID, s.ServiceDate, s.InductCutoffAt, s.WindowStart, s.WindowEnd, s.StopCapacity,
			s.AssignedStops, string(s.State), nullable(s.CancelReason), s.Version)
		if isUniqueViolation(err) {
			return ports.ErrConcurrentUpdate
		}
		return err
	}
	tag, err := q.Exec(ctx, `UPDATE delivery_waves SET assigned_stops = $2, state = $3, cancel_reason = $4, version = $5
		WHERE wave_id = $1 AND version = $5 - 1`, string(s.ID), s.AssignedStops, string(s.State), nullable(s.CancelReason), s.Version)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ports.ErrConcurrentUpdate
	}
	return nil
}
