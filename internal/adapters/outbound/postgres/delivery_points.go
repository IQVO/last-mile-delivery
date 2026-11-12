package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/claudioed/last-mile-delivery/internal/application/ports"
	"github.com/claudioed/last-mile-delivery/internal/domain/deliverypoint"
)

// DeliveryPointRepository is the pgx-backed ports.DeliveryPointRepository.
type DeliveryPointRepository struct{ pool *pgxpool.Pool }

// NewDeliveryPointRepository constructs the repository over pool.
func NewDeliveryPointRepository(pool *pgxpool.Pool) *DeliveryPointRepository {
	return &DeliveryPointRepository{pool}
}

const deliveryPointColumns = `stop_key, attempts, delivered, safe_place_used, failed_not_home, failed_access, failed_other,
	strikes, flagged_at, last_attempt_at, version`

// strikeJSON is the stored shape of one strike in the strikes jsonb array.
type strikeJSON struct {
	RouteID        string    `json:"routeId"`
	TrackingNumber string    `json:"trackingNumber"`
	Category       string    `json:"category"`
	At             time.Time `json:"at"`
	Notified       bool      `json:"notified"`
}

func encodeStrikes(in []deliverypoint.Strike) (string, error) {
	out := make([]strikeJSON, len(in))
	for i, s := range in {
		out[i] = strikeJSON{RouteID: s.RouteID, TrackingNumber: s.TrackingNumber, Category: string(s.Category), At: s.At.UTC(), Notified: s.Notified}
	}
	b, err := json.Marshal(out)
	if err != nil {
		return "", fmt.Errorf("encode strikes: %w", err)
	}
	return string(b), nil
}

func decodeStrikes(raw []byte) ([]deliverypoint.Strike, error) {
	var in []strikeJSON
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, fmt.Errorf("decode strikes: %w", err)
	}
	out := make([]deliverypoint.Strike, len(in))
	for i, s := range in {
		out[i] = deliverypoint.Strike{
			RouteID: s.RouteID, TrackingNumber: s.TrackingNumber, Category: deliverypoint.Category(s.Category), At: s.At.UTC(), Notified: s.Notified,
		}
	}
	return out, nil
}

func scanDeliveryPoint(row interface{ Scan(...any) error }) (*deliverypoint.Profile, error) {
	var s deliverypoint.Snapshot
	var strikes []byte
	var flagged *time.Time
	err := row.Scan(&s.StopKey, &s.Attempts, &s.Delivered, &s.SafePlaceUsed, &s.FailedNotHome, &s.FailedAccess, &s.FailedOther,
		&strikes, &flagged, &s.LastAttemptAt, &s.Version)
	if err != nil {
		return nil, err
	}
	if flagged != nil {
		s.FlaggedAt = *flagged
	}
	if s.Strikes, err = decodeStrikes(strikes); err != nil {
		return nil, err
	}
	return deliverypoint.Restore(s)
}

// Get returns the profile or ports.ErrNotFound.
func (r *DeliveryPointRepository) Get(ctx context.Context, stopKey string) (*deliverypoint.Profile, error) {
	p, err := scanDeliveryPoint(queryFor(ctx, r.pool).QueryRow(ctx, `SELECT `+deliveryPointColumns+` FROM delivery_point_profiles WHERE stop_key = $1`, stopKey))
	if err != nil {
		return nil, missOr(err)
	}
	return p, nil
}

// GetMany returns the profiles of the stop keys that have one, in one query.
func (r *DeliveryPointRepository) GetMany(ctx context.Context, stopKeys []string) (map[string]*deliverypoint.Profile, error) {
	out := map[string]*deliverypoint.Profile{}
	if len(stopKeys) == 0 {
		return out, nil
	}
	rows, err := queryFor(ctx, r.pool).Query(ctx, `SELECT `+deliveryPointColumns+` FROM delivery_point_profiles WHERE stop_key = ANY($1)`, stopKeys)
	if err != nil {
		return nil, fmt.Errorf("get delivery point profiles: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		p, err := scanDeliveryPoint(rows)
		if err != nil {
			return nil, fmt.Errorf("scan delivery point profile: %w", err)
		}
		out[p.StopKey()] = p
	}
	return out, rows.Err()
}

// Save persists the profile under the optimistic rule: a create expects the key
// to be absent (a unique violation is ErrConcurrentUpdate), an update expects the
// stored version to be version-1.
func (r *DeliveryPointRepository) Save(ctx context.Context, p *deliverypoint.Profile) error {
	s := p.Snapshot()
	strikes, err := encodeStrikes(s.Strikes)
	if err != nil {
		return err
	}
	var flagged *time.Time
	if !s.FlaggedAt.IsZero() {
		at := s.FlaggedAt.UTC()
		flagged = &at
	}
	q := queryFor(ctx, r.pool)
	if s.Version == 1 {
		_, err := q.Exec(ctx, `INSERT INTO delivery_point_profiles (`+deliveryPointColumns+`) VALUES ($1, $2, $3, $4, $5, $6, $7, $8::jsonb, $9, $10, $11)`,
			s.StopKey, s.Attempts, s.Delivered, s.SafePlaceUsed, s.FailedNotHome, s.FailedAccess, s.FailedOther, strikes, flagged, s.LastAttemptAt.UTC(), s.Version)
		if isUniqueViolation(err) {
			return ports.ErrConcurrentUpdate
		}
		return err
	}
	tag, err := q.Exec(ctx, `UPDATE delivery_point_profiles SET attempts = $2, delivered = $3, safe_place_used = $4, failed_not_home = $5,
		failed_access = $6, failed_other = $7, strikes = $8::jsonb, flagged_at = $9, last_attempt_at = $10, version = $11
		WHERE stop_key = $1 AND version = $11 - 1`,
		s.StopKey, s.Attempts, s.Delivered, s.SafePlaceUsed, s.FailedNotHome, s.FailedAccess, s.FailedOther, strikes, flagged, s.LastAttemptAt.UTC(), s.Version)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ports.ErrConcurrentUpdate
	}
	return nil
}
