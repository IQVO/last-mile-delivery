//go:build integration

package postgres_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/claudioed/last-mile-delivery/internal/adapters/outbound/postgres"
	"github.com/claudioed/last-mile-delivery/internal/testing/pgtest"
)

var errReused = errors.New("reused")

func TestIdempotencyStoreReplaysAndRefusesAReusedKey(t *testing.T) {
	store := postgres.NewIdempotencyStore(pgtest.NewPool(t), errReused)
	ctx := context.Background()
	var runs int
	fn := func(context.Context) (int, []byte) { runs++; return 201, []byte(`{"ok":true}`) }

	status, body, replayed, err := store.Run(ctx, "POST /waves", "k", "h1", fn)
	if err != nil || status != 201 || replayed || string(body) != `{"ok":true}` {
		t.Fatalf("first = %d %s %v %v", status, body, replayed, err)
	}
	status, body, replayed, err = store.Run(ctx, "POST /waves", "k", "h1", fn)
	if err != nil || status != 201 || !replayed || string(body) != `{"ok":true}` || runs != 1 {
		t.Fatalf("replay = %d %s %v %v (runs %d)", status, body, replayed, err, runs)
	}
	if _, _, _, err := store.Run(ctx, "POST /waves", "k", "other", fn); !errors.Is(err, errReused) {
		t.Fatalf("reuse = %v", err)
	}
	if _, _, replayed, _ := store.Run(ctx, "POST /routes", "k", "h1", fn); replayed || runs != 2 {
		t.Fatalf("another scope must run again (runs %d)", runs)
	}
}

func TestIdempotencyStoreStoresOnlyASuccess(t *testing.T) {
	store := postgres.NewIdempotencyStore(pgtest.NewPool(t), errReused)
	ctx := context.Background()
	if status, _, _, err := store.Run(ctx, "s", "k", "h", func(context.Context) (int, []byte) { return 409, []byte("no") }); err != nil || status != 409 {
		t.Fatalf("failure = %d %v", status, err)
	}
	status, _, replayed, err := store.Run(ctx, "s", "k", "h", func(context.Context) (int, []byte) { return 200, []byte("yes") })
	if err != nil || status != 200 || replayed {
		t.Fatalf("a failure must not be replayed: %d %v %v", status, replayed, err)
	}
}

func TestIdempotencyStoreExpiresAfterADay(t *testing.T) {
	pool := pgtest.NewPool(t)
	store := postgres.NewIdempotencyStore(pool, errReused)
	ctx := context.Background()
	fn := func(context.Context) (int, []byte) { return 200, []byte("v") }
	if _, _, _, err := store.Run(ctx, "s", "k", "h", fn); err != nil {
		t.Fatal(err)
	}
	pgtest.Exec(t, pool, `UPDATE idempotency_keys SET created_at = now() - interval '25 hours'`)
	var runs int
	_, _, replayed, err := store.Run(ctx, "s", "k", "other-hash", func(context.Context) (int, []byte) { runs++; return 200, []byte("v2") })
	if err != nil || replayed || runs != 1 {
		t.Fatalf("an expired key must run again with any body: replayed %v runs %d err %v", replayed, runs, err)
	}
}

// Two concurrent requests with one key run the body once; the second waits and
// replays the first's response.
func TestIdempotencyStoreRunsAConcurrentKeyOnce(t *testing.T) {
	store := postgres.NewIdempotencyStore(pgtest.NewPool(t), errReused)
	var runs, replays atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _, replayed, err := store.Run(context.Background(), "s", "same", "h", func(context.Context) (int, []byte) {
				runs.Add(1)
				return 201, []byte("created")
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
