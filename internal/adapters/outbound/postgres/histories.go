package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/claudioed/last-mile-delivery/internal/application/ports"
	"github.com/claudioed/last-mile-delivery/internal/domain/history"
)

// HistoryRepository is the pgx-backed ports.HistoryRepository.
type HistoryRepository struct{ pool *pgxpool.Pool }

// NewHistoryRepository constructs the repository over pool.
func NewHistoryRepository(pool *pgxpool.Pool) *HistoryRepository { return &HistoryRepository{pool} }

const historyColumns = `tracking_number, station_node_id, COALESCE(assignment_id, ''), failed_attempts, status, arrived_at, version`

func scanHistory(row interface{ Scan(...any) error }) (history.Snapshot, error) {
	var s history.Snapshot
	var status string
	err := row.Scan(&s.TrackingNumber, &s.StationNodeID, &s.AssignmentID, &s.FailedAttempts, &status, &s.ArrivedAt, &s.Version)
	s.Status = history.Status(status)
	return s, err
}

// Get returns the history or ports.ErrNotFound.
func (r *HistoryRepository) Get(ctx context.Context, trackingNumber string) (*history.History, error) {
	s, err := scanHistory(queryFor(ctx, r.pool).QueryRow(ctx, `SELECT `+historyColumns+` FROM package_histories WHERE tracking_number = $1`, trackingNumber))
	if err != nil {
		return nil, missOr(err)
	}
	return history.Restore(s)
}

// ListWaiting returns the AT_STATION histories of the station, oldest first.
func (r *HistoryRepository) ListWaiting(ctx context.Context, station string, limit int) ([]*history.History, error) {
	rows, err := queryFor(ctx, r.pool).Query(ctx, `SELECT `+historyColumns+` FROM package_histories
		WHERE station_node_id = $1 AND status = 'AT_STATION' ORDER BY arrived_at, tracking_number LIMIT $2`, station, limit)
	if err != nil {
		return nil, fmt.Errorf("list waiting packages: %w", err)
	}
	defer rows.Close()
	out := []*history.History{}
	for rows.Next() {
		s, err := scanHistory(rows)
		if err != nil {
			return nil, fmt.Errorf("scan history: %w", err)
		}
		h, err := history.Restore(s)
		if err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

// Save persists the history under the optimistic rule.
func (r *HistoryRepository) Save(ctx context.Context, h *history.History) error {
	s := h.Snapshot()
	q := queryFor(ctx, r.pool)
	if s.Version == 1 {
		_, err := q.Exec(ctx, `INSERT INTO package_histories (tracking_number, station_node_id, assignment_id, failed_attempts, status, arrived_at, version)
			VALUES ($1, $2, $3, $4, $5, $6, $7)`, s.TrackingNumber, s.StationNodeID, nullable(s.AssignmentID), s.FailedAttempts, string(s.Status), s.ArrivedAt, s.Version)
		if isUniqueViolation(err) {
			return ports.ErrConcurrentUpdate
		}
		return err
	}
	tag, err := q.Exec(ctx, `UPDATE package_histories SET failed_attempts = $2, status = $3, version = $4 WHERE tracking_number = $1 AND version = $4 - 1`,
		s.TrackingNumber, s.FailedAttempts, string(s.Status), s.Version)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ports.ErrConcurrentUpdate
	}
	return nil
}
