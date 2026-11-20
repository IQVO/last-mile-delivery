package memory_test

import (
	"context"
	"testing"

	"github.com/claudioed/last-mile-delivery/internal/adapters/outbound/memory"
	"github.com/claudioed/last-mile-delivery/internal/application/idempotency"
)

func respond(status int, transient bool) func(context.Context) (idempotency.Record, error) {
	return func(context.Context) (idempotency.Record, error) {
		return idempotency.Record{Status: status, Body: []byte("x"), Transient: transient}, nil
	}
}

// A Transient answer (a RETRYABLE refusal, ADR 0019) and a 5xx are returned but
// not stored: the key stays free, a retry executes, another request under it is
// a new request. A stored answer is replayed.
func TestIdempotencyStoreDoesNotStoreATransientAnswer(t *testing.T) {
	s := memory.NewIdempotency(nil)
	ctx := context.Background()
	if rec, replayed, err := s.Do(ctx, "k", "h1", respond(409, true)); err != nil || replayed || rec.Status != 409 {
		t.Fatalf("transient = %+v %v %v", rec, replayed, err)
	}
	if s.Stored() != 0 {
		t.Fatalf("stored = %d after a Transient answer, want 0", s.Stored())
	}
	if rec, replayed, err := s.Do(ctx, "k", "h2", respond(503, false)); err != nil || replayed || rec.Status != 503 || s.Stored() != 0 {
		t.Fatalf("5xx = %+v %v %v stored=%d", rec, replayed, err, s.Stored())
	}
	if rec, replayed, err := s.Do(ctx, "k", "h3", respond(409, false)); err != nil || replayed || rec.Status != 409 || s.Stored() != 1 {
		t.Fatalf("a refusal not marked Transient is stored: %+v %v %v stored=%d", rec, replayed, err, s.Stored())
	}
	if rec, replayed, _ := s.Do(ctx, "k", "h3", respond(200, false)); !replayed || rec.Status != 409 {
		t.Fatalf("replay = %+v %v, want the stored 409", rec, replayed)
	}
}
