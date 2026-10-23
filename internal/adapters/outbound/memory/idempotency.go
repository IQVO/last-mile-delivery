package memory

import (
	"context"
	"errors"
	"sync"
	"time"
)

// IdempotencyTTL is how long a stored response is replayed.
const IdempotencyTTL = 24 * time.Hour

// storedResponse is one stored 2xx response.
type storedResponse struct {
	hash    string
	status  int
	body    []byte
	created time.Time
}

// Idempotency is the in-memory double of the HTTP adapter's IdempotencyStore:
// the same contract as the Postgres store (see the http package), serialised by
// one mutex.
type Idempotency struct {
	mu     sync.Mutex
	rows   map[[2]string]storedResponse
	now    func() time.Time
	reused error
}

// NewIdempotency returns an empty store. reused is the error returned for a key
// stored with another request hash (the http package's ErrKeyReused); now is the
// clock (time.Now when nil).
func NewIdempotency(reused error, now func() time.Time) *Idempotency {
	if now == nil {
		now = time.Now
	}
	if reused == nil {
		reused = errors.New("idempotency key reused")
	}
	return &Idempotency{rows: map[[2]string]storedResponse{}, now: now, reused: reused}
}

// Run implements the IdempotencyStore contract.
func (s *Idempotency) Run(ctx context.Context, scope, key, hash string, fn func(context.Context) (int, []byte)) (int, []byte, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id := [2]string{scope, key}
	if row, ok := s.rows[id]; ok {
		if s.now().Sub(row.created) < IdempotencyTTL {
			if row.hash != hash {
				return 0, nil, false, s.reused
			}
			return row.status, append([]byte(nil), row.body...), true, nil
		}
		delete(s.rows, id)
	}
	status, body := fn(ctx)
	if status >= 200 && status < 300 {
		s.rows[id] = storedResponse{hash: hash, status: status, body: append([]byte(nil), body...), created: s.now()}
	}
	return status, body, false, nil
}

// Stored returns how many responses are stored.
func (s *Idempotency) Stored() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.rows)
}
