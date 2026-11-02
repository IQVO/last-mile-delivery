//go:build integration

package postgres_test

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/claudioed/last-mile-delivery/internal/adapters/outbound/postgres"
	"github.com/claudioed/last-mile-delivery/internal/adapters/outbound/postgres/pgtx"
	"github.com/claudioed/last-mile-delivery/internal/application/idempotency"
	"github.com/claudioed/last-mile-delivery/internal/testing/pgtest"
)

// poisonTx simulates what a mapped domain conflict does to the store's
// transaction: a statement fails on the server (a unique violation, like a
// racing request losing on a unique index), the adapter would translate it to a
// typed error and the handler answers 4xx, but the transaction is ABORTED from
// then on.
func poisonTx(t *testing.T, ctx context.Context) {
	t.Helper()
	tx, ok := pgtx.From(ctx)
	if !ok {
		t.Fatal("fn must run inside the store's transaction")
	}
	for i := 0; i < 2; i++ {
		if _, err := tx.Exec(ctx, `INSERT INTO delivery_service_partners (dsp_id, station_node_id, van_capacity, state, version) VALUES ('dsp-poison', 'node-ds-01', 2, 'ACTIVE', 1)`); err != nil {
			return // the second insert aborts the transaction (23505)
		}
	}
	t.Fatal("the duplicate insert did not abort the transaction")
}

// A 409 over an aborted transaction is stored, committed and replayed; the
// handler's partial write is discarded and the handler never re-runs.
func TestIdempotencyStoreA409FromAnAbortedTransactionCommitsTheOutcome(t *testing.T) {
	pool := pgtest.NewPool(t)
	store := postgres.NewIdempotencyStore(pool)
	ctx := context.Background()
	var runs atomic.Int32
	fn := func(c context.Context) (idempotency.Record, error) {
		runs.Add(1)
		poisonTx(t, c)
		return idempotency.Record{Status: 409, ContentType: "application/problem+json", Body: []byte(`{"status":409}`)}, nil
	}
	rec, replayed, err := store.Do(ctx, "k-poison", "h", fn)
	if err != nil || replayed || rec.Status != 409 {
		t.Fatalf("first = %+v %v %v, want the truthful 409", rec, replayed, err)
	}
	var status int
	if err := pool.QueryRow(ctx, `SELECT response_status FROM idempotency_keys WHERE idempotency_key = 'k-poison'`).Scan(&status); err != nil || status != 409 {
		t.Fatalf("the 409 outcome was not committed: %d %v", status, err)
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM delivery_service_partners WHERE dsp_id = 'dsp-poison'`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("partial handler writes survived: %d %v", n, err)
	}
	rec, replayed, err = store.Do(ctx, "k-poison", "h", fn)
	if err != nil || !replayed || rec.Status != 409 || runs.Load() != 1 {
		t.Fatalf("replay = %+v %v %v (runs %d), want the cached 409 without a re-run", rec, replayed, err, runs.Load())
	}
}

// A 2xx over an aborted transaction is a lie: it fails, nothing is cached and
// the same key retries the real work.
func TestIdempotencyStoreA2xxFromAnAbortedTransactionIsNeverCached(t *testing.T) {
	pool := pgtest.NewPool(t)
	store := postgres.NewIdempotencyStore(pool)
	ctx := context.Background()
	var calls atomic.Int32
	fn := func(c context.Context) (idempotency.Record, error) {
		if calls.Add(1) == 1 {
			poisonTx(t, c)
		}
		return idempotency.Record{Status: 201, Body: []byte(`{"ok":true}`)}, nil
	}
	if _, _, err := store.Do(ctx, "k-lie", "h", fn); err == nil {
		t.Fatal("a 201 over an aborted transaction must fail the request")
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM idempotency_keys WHERE idempotency_key = 'k-lie'`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("a failed success left %d idempotency rows (%v)", n, err)
	}
	rec, replayed, err := store.Do(ctx, "k-lie", "h", fn)
	if err != nil || replayed || rec.Status != 201 || calls.Load() != 2 {
		t.Fatalf("retry = %+v %v %v (calls %d), want the real work to succeed", rec, replayed, err, calls.Load())
	}
}
