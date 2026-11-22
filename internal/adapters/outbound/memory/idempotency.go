package memory

import (
	"context"
	"sort"
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
// join, so a refusal or 5xx handler leaves whatever it wrote in the memory
// repositories (their own writes are atomic per call). A 5xx and a Transient
// answer (a RETRYABLE refusal, ADR 0019) are not stored: the key stays free.
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
	if rec.Status < 500 && !rec.Transient {
		stored := rec
		stored.Body = append([]byte(nil), rec.Body...)
		s.rows[key] = storedAnswer{rec: stored, created: s.now()}
	}
	return rec, false, nil
}

// PurgeExpired implements idempotency.Store: it deletes at most limit answers
// that are no longer replayed (age >= idempotency.TTL), oldest first, and
// returns how many. Do holds the same mutex while it runs fn, so a purge waits
// for a request in flight and then finds its answer live.
func (s *Idempotency) PurgeExpired(_ context.Context, limit int) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var expired []string
	for key, row := range s.rows {
		if s.now().Sub(row.created) >= IdempotencyTTL {
			expired = append(expired, key)
		}
	}
	sort.Slice(expired, func(i, j int) bool { return s.rows[expired[i]].created.Before(s.rows[expired[j]].created) })
	if limit < len(expired) {
		expired = expired[:max(limit, 0)]
	}
	for _, key := range expired {
		delete(s.rows, key)
	}
	return len(expired), nil
}

// Stored returns how many answers are stored.
func (s *Idempotency) Stored() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.rows)
}
