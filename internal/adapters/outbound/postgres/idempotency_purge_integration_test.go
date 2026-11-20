//go:build integration

package postgres_test

import (
	"context"
	"testing"
	"time"

	"github.com/claudioed/last-mile-delivery/internal/adapters/outbound/postgres"
	"github.com/claudioed/last-mile-delivery/internal/adapters/outbound/repocontract"
	"github.com/claudioed/last-mile-delivery/internal/application/idempotency"
	"github.com/claudioed/last-mile-delivery/internal/testing/pgtest"
)

// ageSQL moves a row's created_at to ttlPlus(d) seconds ago: d past the TTL
// when positive, still live when negative.
const ageSQL = `UPDATE idempotency_keys SET created_at = now() - make_interval(secs => $2) WHERE idempotency_key = $1`

func ttlPlus(d time.Duration) float64 { return (idempotency.TTL + d).Seconds() }

// An expired row is deleted, a live row (one a minute short of the TTL
// included) is kept and still replays.
func TestIdempotencyPurgeDeletesAnExpiredRowAndKeepsALiveOne(t *testing.T) {
	pool := pgtest.NewPool(t)
	store := postgres.NewIdempotencyStore(pool)
	ctx := context.Background()
	for _, k := range []string{"old", "edge", "live"} {
		if _, _, err := store.Do(ctx, k, "h", answer(201, "v-"+k)); err != nil {
			t.Fatal(err)
		}
	}
	pgtest.Exec(t, pool, ageSQL, "old", ttlPlus(time.Minute))
	pgtest.Exec(t, pool, ageSQL, "edge", ttlPlus(-time.Minute))
	n, err := store.PurgeExpired(ctx, 100)
	if err != nil || n != 1 || pgtest.RowCount(t, pool, "idempotency_keys") != 2 {
		t.Fatalf("purge = %d %v, rows left %d; want 1 deleted, 2 left", n, err, pgtest.RowCount(t, pool, "idempotency_keys"))
	}
	for _, k := range []string{"edge", "live"} {
		if rec, replayed, err := store.Do(ctx, k, "h", answer(200, "other")); err != nil || !replayed || string(rec.Body) != "v-"+k {
			t.Fatalf("%s after the purge = %+v %v %v, want the stored answer replayed", k, rec, replayed, err)
		}
	}
	if n, err := store.PurgeExpired(ctx, 100); err != nil || n != 0 {
		t.Fatalf("second purge = %d %v, want 0", n, err)
	}
}

// One pass deletes at most batch rows; the next passes finish the backlog.
func TestIdempotencyPurgeDeletesAtMostOneBatchPerPass(t *testing.T) {
	pool := pgtest.NewPool(t)
	store := postgres.NewIdempotencyStore(pool)
	ctx := context.Background()
	for _, k := range []string{"a", "b", "c", "d", "e"} {
		if _, _, err := store.Do(ctx, k, "h", answer(200, k)); err != nil {
			t.Fatal(err)
		}
		pgtest.Exec(t, pool, ageSQL, k, ttlPlus(time.Hour))
	}
	for _, want := range []int{2, 2, 1, 0} {
		if n, err := store.PurgeExpired(ctx, 2); err != nil || n != want {
			t.Fatalf("purge = %d %v, want %d", n, err, want)
		}
	}
	if left := pgtest.RowCount(t, pool, "idempotency_keys"); left != 0 {
		t.Fatalf("rows left = %d, want 0", left)
	}
}

// A purge never waits for, nor breaks, a request that holds the key: Do keeps
// its advisory lock and transaction open while the purge runs over the expired
// row of that same key; the request still answers fresh, stores its row, and
// the next call replays it.
func TestIdempotencyPurgeDoesNotBreakARequestHoldingTheKey(t *testing.T) {
	pool := pgtest.NewPool(t)
	store := postgres.NewIdempotencyStore(pool)
	ctx := context.Background()
	if _, _, err := store.Do(ctx, "held", "old-hash", answer(200, "stale")); err != nil {
		t.Fatal(err)
	}
	pgtest.Exec(t, pool, ageSQL, "held", ttlPlus(time.Hour))

	inFlight, release := make(chan struct{}), make(chan struct{})
	type result struct {
		rec      idempotency.Record
		replayed bool
		err      error
	}
	done := make(chan result, 1)
	go func() {
		rec, replayed, err := store.Do(ctx, "held", "new-hash", func(context.Context) (idempotency.Record, error) {
			close(inFlight)
			<-release
			return idempotency.Record{Status: 201, ContentType: "application/json", Body: []byte("fresh")}, nil
		})
		done <- result{rec, replayed, err}
	}()
	<-inFlight

	purged := make(chan error, 1)
	go func() { _, err := store.PurgeExpired(ctx, 100); purged <- err }()
	select {
	case err := <-purged:
		if err != nil {
			t.Fatalf("purge during an in-flight request: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the purge blocked on a request that holds the key")
	}

	close(release)
	r := <-done
	if r.err != nil || r.replayed || string(r.rec.Body) != "fresh" {
		t.Fatalf("the request holding the key = %+v %v %v, want a fresh 201", r.rec, r.replayed, r.err)
	}
	rec, replayed, err := store.Do(ctx, "held", "new-hash", answer(200, "again"))
	if err != nil || !replayed || string(rec.Body) != "fresh" {
		t.Fatalf("after purge + request = %+v %v %v, want the fresh answer replayed", rec, replayed, err)
	}
}

// The Postgres store passes the same retention contract as the memory double.
func TestIdempotencyPostgresPassesThePurgeContract(t *testing.T) {
	repocontract.RunIdempotencyPurge(t, func(t *testing.T) repocontract.IdempotencyFixture {
		pool := pgtest.NewPool(t)
		return repocontract.IdempotencyFixture{
			Store: postgres.NewIdempotencyStore(pool),
			Age: func(t *testing.T, d time.Duration) {
				pgtest.Exec(t, pool, `UPDATE idempotency_keys SET created_at = created_at - make_interval(secs => $1)`, d.Seconds())
			},
		}
	})
}
