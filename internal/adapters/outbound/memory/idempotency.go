package memory

import (
	"context"
	"sync"
	"time"

	"github.com/claudioed/last-mile-delivery/internal/application/idempotency"
)

// IdempotencyTTL is how long a stored answer is replayed.
const IdempotencyTTL = idempotency.TTL

type storedAnswer struct {
	rec     idempotency.Record
	created time.Time
}

// Idempotency is the in-memory double of idempotency.Store: the same contract
// as the Postgres store, serialised by one mutex. There is no transaction to
// join, so a 4xx or 5xx handler leaves whatever it wrote in the memory
// repositories (their own writes are atomic per call).
type Idempotency struct {
	mu   sync.Mutex
	rows map[string]storedAnswer
	now  func() time.Time
}

var _ idempotency.Store = (*Idempotency)(nil)

// NewIdempotency returns an empty store; now is the clock (time.Now when nil).
func NewIdempotency(now func() time.Time) *Idempotency {
	if now == nil {
		now = time.Now
	}
	return &Idempotency{rows: map[string]storedAnswer{}, now: now}
}

// Do implements idempotency.Store.
func (s *Idempotency) Do(ctx context.Context, key, hash string, fn func(context.Context) (idempotency.Record, error)) (idempotency.Record, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if row, ok := s.rows[key]; ok {
		if s.now().Sub(row.created) < IdempotencyTTL {
			rec := row.rec
			rec.Body = append([]byte(nil), rec.Body...)
			return rec, true, nil
		}
		delete(s.rows, key)
	}
	rec, err := fn(ctx)
	if err != nil {
		return idempotency.Record{}, false, err
	}
	rec.RequestHash = hash
	if rec.Status < 500 {
		stored := rec
		stored.Body = append([]byte(nil), rec.Body...)
		s.rows[key] = storedAnswer{rec: stored, created: s.now()}
	}
	return rec, false, nil
}

// Stored returns how many answers are stored.
func (s *Idempotency) Stored() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.rows)
}
