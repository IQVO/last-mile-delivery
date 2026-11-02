package postgres

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/claudioed/last-mile-delivery/internal/adapters/outbound/postgres/pgtx"
	"github.com/claudioed/last-mile-delivery/internal/application/idempotency"
)

// httpServerError is the first status that is never stored (a 5xx).
const httpServerError = 500

// httpClientError is the first status that is an error answer (a 4xx).
const httpClientError = 400

// IdempotencyStore is the Postgres idempotency_keys store (24 h) of the
// Idempotency-Key middleware. Do takes a transaction-level advisory lock on the
// key and runs fn inside that same transaction (carried in the ctx fn gets), so
// the key row, the aggregate change and the outbox rows commit together; a
// concurrent request with the same key waits and then replays the stored
// answer. A 4xx is stored, a 5xx or a panic rolls back and is never stored.
type IdempotencyStore struct{ pool *pgxpool.Pool }

var _ idempotency.Store = (*IdempotencyStore)(nil)

// NewIdempotencyStore builds the store.
func NewIdempotencyStore(pool *pgxpool.Pool) *IdempotencyStore {
	return &IdempotencyStore{pool: pool}
}

func lockID(key string) int64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(key))
	return int64(h.Sum64()) //nolint:gosec // the 64 bits are only a lock key: wrapping to negative is fine
}

// healAbortedHandler releases the handler_run savepoint fn ran behind.
//
// A mapped domain conflict (a lost race on a unique index, 23505 -> 409) fails
// a statement on the server and leaves the shared transaction ABORTED even
// though the handler answers a truthful 4xx; storing that answer would then hit
// SQLSTATE 25P02 and the client would see a 500. The release fails in that
// case: ROLLBACK TO SAVEPOINT discards only the handler's partial writes and
// heals the transaction, so the 4xx is stored and committed like any other. A
// success answer (status < 400) over an aborted transaction is a lie, its
// writes are gone: it is returned as an error, the deferred rollback drops
// everything and the same key retries the real work.
func healAbortedHandler(ctx context.Context, tx pgx.Tx, status int) error {
	releaseErr := func() error {
		_, err := tx.Exec(ctx, `RELEASE SAVEPOINT handler_run`)
		return err
	}()
	if releaseErr == nil {
		return nil
	}
	if _, err := tx.Exec(ctx, `ROLLBACK TO SAVEPOINT handler_run`); err != nil {
		return fmt.Errorf("idempotency heal: %w", errors.Join(releaseErr, err))
	}
	if status < httpClientError {
		return fmt.Errorf("idempotency: a %d answer over an aborted transaction is not stored: %w", status, releaseErr)
	}
	return nil
}

// Do implements idempotency.Store.
func (s *IdempotencyStore) Do(ctx context.Context, key, hash string, fn func(context.Context) (idempotency.Record, error)) (idempotency.Record, bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return idempotency.Record{}, false, fmt.Errorf("idempotency begin: %w", err)
	}
	// A rollback after Commit is a harmless no-op; it also covers a panic in fn.
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, lockID(key)); err != nil {
		return idempotency.Record{}, false, fmt.Errorf("idempotency lock: %w", err)
	}
	var rec idempotency.Record
	err = tx.QueryRow(ctx, `SELECT request_hash, response_status, content_type, response_body FROM idempotency_keys
		WHERE idempotency_key = $1 AND created_at > now() - interval '24 hours'`, key).
		Scan(&rec.RequestHash, &rec.Status, &rec.ContentType, &rec.Body)
	switch {
	case err == nil:
		return rec, true, nil
	case !errors.Is(err, pgx.ErrNoRows):
		return idempotency.Record{}, false, fmt.Errorf("idempotency lookup: %w", err)
	}
	if _, err := tx.Exec(ctx, `SAVEPOINT handler_run`); err != nil {
		return idempotency.Record{}, false, fmt.Errorf("idempotency savepoint: %w", err)
	}
	if rec, err = fn(pgtx.With(ctx, tx)); err != nil {
		return idempotency.Record{}, false, err
	}
	if rec.Status >= httpServerError {
		return rec, false, nil
	}
	if err := healAbortedHandler(ctx, tx, rec.Status); err != nil {
		return idempotency.Record{}, false, err
	}
	body := rec.Body
	if body == nil {
		body = []byte{}
	}
	if _, err := tx.Exec(ctx, `INSERT INTO idempotency_keys (idempotency_key, request_hash, response_status, content_type, response_body)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (idempotency_key) DO UPDATE SET request_hash = EXCLUDED.request_hash,
			response_status = EXCLUDED.response_status, content_type = EXCLUDED.content_type,
			response_body = EXCLUDED.response_body, created_at = now()`,
		key, hash, rec.Status, rec.ContentType, body); err != nil {
		return idempotency.Record{}, false, fmt.Errorf("idempotency store: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return idempotency.Record{}, false, fmt.Errorf("idempotency commit: %w", err)
	}
	rec.RequestHash = hash
	return rec, false, nil
}
