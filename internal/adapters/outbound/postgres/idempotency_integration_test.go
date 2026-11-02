//go:build integration

package postgres_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/claudioed/last-mile-delivery/internal/adapters/outbound/postgres"
	"github.com/claudioed/last-mile-delivery/internal/adapters/outbound/postgres/pgtx"
	"github.com/claudioed/last-mile-delivery/internal/application/idempotency"
	"github.com/claudioed/last-mile-delivery/internal/testing/pgtest"
)

func answer(status int, body string) func(context.Context) (idempotency.Record, error) {
	return func(context.Context) (idempotency.Record, error) {
		return idempotency.Record{Status: status, ContentType: "application/json", Body: []byte(body)}, nil
	}
}

func TestIdempotencyStoreReplaysAKeyAndReturnsTheStoredFingerprint(t *testing.T) {
	store := postgres.NewIdempotencyStore(pgtest.NewPool(t))
	ctx := context.Background()
	var runs int
	fn := func(context.Context) (idempotency.Record, error) {
		runs++
		return idempotency.Record{Status: 201, ContentType: "application/json", Body: []byte(`{"ok":true}`)}, nil
	}
	rec, replayed, err := store.Do(ctx, "k", "h1", fn)
	if err != nil || rec.Status != 201 || replayed || string(rec.Body) != `{"ok":true}` {
		t.Fatalf("first = %+v %v %v", rec, replayed, err)
	}
	rec, replayed, err = store.Do(ctx, "k", "h1", fn)
	if err != nil || rec.Status != 201 || !replayed || rec.RequestHash != "h1" || rec.ContentType != "application/json" || runs != 1 {
		t.Fatalf("replay = %+v %v %v (runs %d)", rec, replayed, err, runs)
	}
	// another fingerprint under the same key: the stored one comes back for the 409
	rec, replayed, err = store.Do(ctx, "k", "other", fn)
	if err != nil || !replayed || rec.RequestHash != "h1" || runs != 1 {
		t.Fatalf("reuse = %+v %v %v (runs %d)", rec, replayed, err, runs)
	}
}

func TestIdempotencyStoreStoresAFourXXAndNeverAFiveXX(t *testing.T) {
	store := postgres.NewIdempotencyStore(pgtest.NewPool(t))
	ctx := context.Background()
	if rec, _, err := store.Do(ctx, "k4", "h", answer(409, "no")); err != nil || rec.Status != 409 {
		t.Fatalf("4xx = %+v %v", rec, err)
	}
	if rec, replayed, _ := store.Do(ctx, "k4", "h", answer(200, "yes")); !replayed || rec.Status != 409 {
		t.Fatalf("a 4xx must be replayed: %+v %v", rec, replayed)
	}
	if rec, _, err := store.Do(ctx, "k5", "h", answer(503, "down")); err != nil || rec.Status != 503 {
		t.Fatalf("5xx = %+v %v", rec, err)
	}
	if rec, replayed, _ := store.Do(ctx, "k5", "h", answer(200, "yes")); replayed || rec.Status != 200 {
		t.Fatalf("a 5xx must not be stored: %+v %v", rec, replayed)
	}
}

// The writes fn makes through the ctx it is handed roll back with a 5xx and
// commit with the key row otherwise.
func TestIdempotencyStoreCommitsFnWritesWithTheKeyRow(t *testing.T) {
	pool := pgtest.NewPool(t)
	store := postgres.NewIdempotencyStore(pool)
	ctx := context.Background()
	write := func(status int) func(context.Context) (idempotency.Record, error) {
		return func(c context.Context) (idempotency.Record, error) {
			tx, ok := pgtx.From(c)
			if !ok {
				t.Fatal("fn must run inside the store's transaction")
			}
			if _, err := tx.Exec(c, `INSERT INTO delivery_service_partners (dsp_id, station_node_id, van_capacity, state, version) VALUES ($1, 'node-ds-01', 2, 'ACTIVE', 1)`, "dsp-"+string(rune('a'+status%26))); err != nil {
				t.Fatal(err)
			}
			return idempotency.Record{Status: status}, nil
		}
	}
	if _, _, err := store.Do(ctx, "w5", "h", write(500)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.Do(ctx, "w2", "h", write(201)); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM delivery_service_partners`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("partners = %d (%v), want only the 2xx write", n, err)
	}
}

func TestIdempotencyStoreExpiresAfterADay(t *testing.T) {
	pool := pgtest.NewPool(t)
	store := postgres.NewIdempotencyStore(pool)
	ctx := context.Background()
	if _, _, err := store.Do(ctx, "k", "h", answer(200, "v")); err != nil {
		t.Fatal(err)
	}
	pgtest.Exec(t, pool, `UPDATE idempotency_keys SET created_at = now() - interval '25 hours'`)
	rec, replayed, err := store.Do(ctx, "k", "other-hash", answer(200, "v2"))
	if err != nil || replayed || string(rec.Body) != "v2" {
		t.Fatalf("an expired key must run again with any body: %+v %v %v", rec, replayed, err)
	}
}

// Concurrent requests with one key run the body once; the others wait and
// replay the first's answer.
func TestIdempotencyStoreRunsAConcurrentKeyOnce(t *testing.T) {
	store := postgres.NewIdempotencyStore(pgtest.NewPool(t))
	var runs, replays atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, replayed, err := store.Do(context.Background(), "same", "h", func(context.Context) (idempotency.Record, error) {
				runs.Add(1)
				return idempotency.Record{Status: 201, Body: []byte("created")}, nil
			})
			if err != nil {
				t.Error(err)
			}
			if replayed {
				replays.Add(1)
			}
		}()
	}
	wg.Wait()
	if runs.Load() != 1 || replays.Load() != 7 {
		t.Fatalf("runs %d replays %d, want 1 and 7", runs.Load(), replays.Load())
	}
}
