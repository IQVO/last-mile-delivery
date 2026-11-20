package memory_test

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/claudioed/last-mile-delivery/internal/adapters/outbound/memory"
	"github.com/claudioed/last-mile-delivery/internal/adapters/outbound/repocontract"
)

// The memory double passes the same retention contract as the Postgres store.
func TestIdempotencyMemoryPassesThePurgeContract(t *testing.T) {
	repocontract.RunIdempotencyPurge(t, func(*testing.T) repocontract.IdempotencyFixture {
		var skew atomic.Int64
		now := func() time.Time { return time.Unix(1_700_000_000, 0).Add(time.Duration(skew.Load())) }
		return repocontract.IdempotencyFixture{
			Store: memory.NewIdempotency(now),
			Age:   func(_ *testing.T, d time.Duration) { skew.Add(int64(d)) },
		}
	})
}

func TestIdempotencyMemoryPurgeCountsAndHonoursALimitOfZero(t *testing.T) {
	var skew atomic.Int64
	s := memory.NewIdempotency(func() time.Time { return time.Unix(1_700_000_000, 0).Add(time.Duration(skew.Load())) })
	if _, _, err := s.Do(t.Context(), "k", "h", respond(200, false)); err != nil {
		t.Fatal(err)
	}
	skew.Add(int64(memory.IdempotencyTTL)) // exactly the TTL: no longer replayed, so purgeable
	for _, c := range []struct{ limit, want, left int }{{0, 0, 1}, {-3, 0, 1}, {5, 1, 0}} {
		if n, err := s.PurgeExpired(t.Context(), c.limit); err != nil || n != c.want || s.Stored() != c.left {
			t.Fatalf("limit %d = %d %v stored %d, want %d and %d left", c.limit, n, err, s.Stored(), c.want, c.left)
		}
	}
}
