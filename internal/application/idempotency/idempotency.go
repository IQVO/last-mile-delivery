// Package idempotency holds the stored answer of an Idempotency-Key and the
// store port the HTTP middleware runs a request through.
package idempotency

import (
	"context"
	"time"
)

// TTL is how long a key is remembered.
const TTL = 24 * time.Hour

// Record is the first answer stored under a key.
type Record struct {
	RequestHash string
	Status      int
	ContentType string
	Body        []byte
}

// Store runs a request at most once per key. Do claims the key, calls fn and
// stores fn's answer; fn receives a context that carries the store's
// transaction (Postgres), so every unit of work inside it joins that
// transaction: the key row, the aggregate change and the outbox rows commit
// together. When the key is already stored Do does not call fn and returns the
// stored Record with replayed true (the caller compares RequestHash). Concurrent
// calls with one key are serialised; the loser replays the winner's answer. An
// answer with status 500 or above, and an error or panic from fn, are never
// stored. Keys are global, not per route.
type Store interface {
	Do(ctx context.Context, key, requestHash string, fn func(ctx context.Context) (Record, error)) (rec Record, replayed bool, err error)
}
