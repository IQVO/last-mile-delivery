package postgres

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// IdempotencyStore is the Postgres idempotency_keys store (24 h) of the
// Idempotency-Key middleware. Run holds a transaction-level advisory lock on
// (scope, key) for the whole request, in a connection of its own: a concurrent
// request with the same key waits for the first one and then replays its stored
// response, so a key never runs twice. Only 2xx responses are stored.
type IdempotencyStore struct {
	pool   *pgxpool.Pool
	reused error
}

// NewIdempotencyStore builds the store; reused is returned for a key stored
// with another request hash (the HTTP adapter's ErrKeyReused).
func NewIdempotencyStore(pool *pgxpool.Pool, reused error) *IdempotencyStore {
	return &IdempotencyStore{pool: pool, reused: reused}
}

func lockID(scope, key string) int64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(scope))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(key))
	return int64(h.Sum64()) //nolint:gosec // the 64 bits are only a lock key: wrapping to negative is fine
}

// Run implements the HTTP adapter's IdempotencyStore.
func (s *IdempotencyStore) Run(ctx context.Context, scope, key, hash string, fn func(context.Context) (int, []byte)) (int, []byte, bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, nil, false, fmt.Errorf("idempotency begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, lockID(scope, key)); err != nil {
		return 0, nil, false, fmt.Errorf("idempotency lock: %w", err)
	}
	var storedHash string
	var status int
	var body []byte
	err = tx.QueryRow(ctx, `SELECT request_hash, response_status, response_body FROM idempotency_keys
		WHERE route = $1 AND idempotency_key = $2 AND created_at > now() - interval '24 hours'`, scope, key).Scan(&storedHash, &status, &body)
	switch {
	case err == nil:
		if storedHash != hash {
			return 0, nil, false, s.reused
		}
		return status, body, true, nil
	case !errors.Is(err, pgx.ErrNoRows):
		return 0, nil, false, fmt.Errorf("idempotency lookup: %w", err)
	}
	status, body = fn(ctx)
	if status >= 200 && status < 300 {
		if _, err := tx.Exec(ctx, `INSERT INTO idempotency_keys (route, idempotency_key, request_hash, response_status, response_body)
			VALUES ($1, $2, $3, $4, $5)
			ON CONFLICT (route, idempotency_key) DO UPDATE SET request_hash = EXCLUDED.request_hash,
				response_status = EXCLUDED.response_status, response_body = EXCLUDED.response_body, created_at = now()`,
			scope, key, hash, status, body); err != nil {
			return 0, nil, false, fmt.Errorf("idempotency store: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, nil, false, fmt.Errorf("idempotency commit: %w", err)
	}
	return status, body, false, nil
}
