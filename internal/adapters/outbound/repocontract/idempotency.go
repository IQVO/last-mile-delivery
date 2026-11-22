package repocontract

import (
	"context"
	"testing"
	"time"

	"github.com/claudioed/last-mile-delivery/internal/application/idempotency"
)

// IdempotencyFixture is one idempotency.Store over ONE empty database.
type IdempotencyFixture struct {
	Store idempotency.Store
	// Age makes every answer stored so far d older (the memory double advances
	// its clock, Postgres moves created_at back): the only way a test reaches
	// an expired row without waiting a day.
	Age func(t *testing.T, d time.Duration)
}

// IdempotencyFactory returns a fresh, empty IdempotencyFixture and registers
// its own cleanup with t.
type IdempotencyFactory func(t *testing.T) IdempotencyFixture

// RunIdempotencyPurge is the retention contract of idempotency.Store.PurgeExpired
// (the replay contract of Do lives in the adapters' own tests): the memory
// double and the Postgres store both run it.
func RunIdempotencyPurge(t *testing.T, factory IdempotencyFactory) {
	t.Helper()
	cases := []struct {
		name string
		run  func(*testing.T, IdempotencyFixture)
	}{
		{"deletes expired keeps live", purgeKeepsLive},
		{"one batch per pass", purgeBatches},
		{"oldest first", purgeOldestFirst},
		{"a request holding the key", purgeHeldKey},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) { c.run(t, factory(t)) })
	}
}

// purgeKeepsLive: a row older than the TTL goes, a row a minute short of it
// and a fresh one stay and replay; a second pass has nothing left to delete.
func purgeKeepsLive(t *testing.T, f IdempotencyFixture) {
	ctx := context.Background()
	put(t, f.Store, "old", "v-old")
	f.Age(t, idempotency.TTL+time.Minute)
	put(t, f.Store, "edge", "v-edge")
	f.Age(t, idempotency.TTL-time.Minute)
	put(t, f.Store, "live", "v-live")
	if n, err := f.Store.PurgeExpired(ctx, 100); err != nil || n != 1 {
		t.Fatalf("purge = %d %v, want 1 (only the expired row)", n, err)
	}
	wantReplay(t, f.Store, "edge", "v-edge")
	wantReplay(t, f.Store, "live", "v-live")
	if n, err := f.Store.PurgeExpired(ctx, 100); err != nil || n != 0 {
		t.Fatalf("second purge = %d %v, want 0", n, err)
	}
}

// purgeBatches: one pass deletes at most limit rows, the next ones finish.
func purgeBatches(t *testing.T, f IdempotencyFixture) {
	for _, k := range []string{"a", "b", "c", "d", "e"} {
		put(t, f.Store, k, k)
	}
	f.Age(t, idempotency.TTL+time.Hour)
	for _, want := range []int{2, 2, 1, 0} {
		if n, err := f.Store.PurgeExpired(context.Background(), 2); err != nil || n != want {
			t.Fatalf("purge = %d %v, want %d", n, err, want)
		}
	}
}

// purgeOldestFirst: with two expired rows and a limit of one, the older goes.
// Ageing everything back by more than both ages makes the survivor live again,
// which is what tells the two apart (an expired row is never replayed).
func purgeOldestFirst(t *testing.T, f IdempotencyFixture) {
	put(t, f.Store, "older", "v-older")
	f.Age(t, time.Hour)
	put(t, f.Store, "newer", "v-newer")
	f.Age(t, idempotency.TTL)
	if n, err := f.Store.PurgeExpired(context.Background(), 1); err != nil || n != 1 {
		t.Fatalf("purge = %d %v, want 1", n, err)
	}
	f.Age(t, -(idempotency.TTL + time.Hour)) // both would be live again were they still stored
	wantReplay(t, f.Store, "newer", "v-newer")
}

// purgeHeldKey: a purge that overlaps a request holding the (expired) key
// neither fails nor breaks it: the request answers fresh, its row is stored
// and replays afterwards. (Postgres additionally never blocks the purge on
// that request: its own integration test.)
func purgeHeldKey(t *testing.T, f IdempotencyFixture) {
	ctx := context.Background()
	put(t, f.Store, "held", "stale")
	f.Age(t, idempotency.TTL+time.Hour)
	inFlight, release := make(chan struct{}), make(chan struct{})
	type result struct {
		rec      idempotency.Record
		replayed bool
		err      error
	}
	done := make(chan result, 1)
	go func() {
		rec, replayed, err := f.Store.Do(ctx, "held", "h", func(context.Context) (idempotency.Record, error) {
			close(inFlight)
			<-release
			return idempotency.Record{Status: 201, Body: []byte("fresh")}, nil
		})
		done <- result{rec, replayed, err}
	}()
	<-inFlight
	purged := make(chan error, 1)
	go func() { _, err := f.Store.PurgeExpired(ctx, 100); purged <- err }()
	time.Sleep(20 * time.Millisecond) // let the purge overlap the request
	close(release)
	if err := <-purged; err != nil {
		t.Fatalf("purge during an in-flight request: %v", err)
	}
	if r := <-done; r.err != nil || r.replayed || string(r.rec.Body) != "fresh" {
		t.Fatalf("the request holding the key = %+v replayed %v err %v, want a fresh answer", r.rec, r.replayed, r.err)
	}
	wantReplay(t, f.Store, "held", "fresh")
}

func put(t *testing.T, s idempotency.Store, key, body string) {
	t.Helper()
	fn := func(context.Context) (idempotency.Record, error) {
		return idempotency.Record{Status: 201, Body: []byte(body)}, nil
	}
	if _, replayed, err := s.Do(context.Background(), key, "h", fn); err != nil || replayed {
		t.Fatalf("store %s: replayed %v err %v", key, replayed, err)
	}
}

func wantReplay(t *testing.T, s idempotency.Store, key, body string) {
	t.Helper()
	fn := func(context.Context) (idempotency.Record, error) {
		return idempotency.Record{Status: 200, Body: []byte("ran")}, nil
	}
	rec, replayed, err := s.Do(context.Background(), key, "h", fn)
	if err != nil || !replayed || string(rec.Body) != body {
		t.Fatalf("%s = %+v replayed %v err %v, want the stored %q replayed", key, rec, replayed, err, body)
	}
}
