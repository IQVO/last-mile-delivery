package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/claudioed/last-mile-delivery/internal/application/idempotency"
)

// fakePurger answers PurgeExpired from a script and records the limit it got.
type fakePurger struct {
	idempotency.Store
	script []int
	err    error
	limits []int
	calls  chan struct{}
}

func (f *fakePurger) PurgeExpired(_ context.Context, limit int) (int, error) {
	f.limits = append(f.limits, limit)
	if f.calls != nil {
		f.calls <- struct{}{}
	}
	if f.err != nil {
		return 0, f.err
	}
	if len(f.script) == 0 {
		return 0, nil
	}
	n := f.script[0]
	f.script = f.script[1:]
	return n, nil
}

func TestPurgeRunsBatchesUntilOneComesBackShort(t *testing.T) {
	f := &fakePurger{script: []int{3, 3, 1}}
	n, err := purgeIdempotencyKeys(t.Context(), f, 3)
	if err != nil || n != 7 || len(f.limits) != 3 || f.limits[0] != 3 {
		t.Fatalf("purge = %d %v limits %v, want 7 rows in 3 batches of 3", n, err, f.limits)
	}
}

func TestPurgeStopsOnAnErrorAndReportsIt(t *testing.T) {
	boom := errors.New("boom")
	f := &fakePurger{err: boom}
	if n, err := purgeIdempotencyKeys(t.Context(), f, 3); !errors.Is(err, boom) || n != 0 || len(f.limits) != 1 {
		t.Fatalf("purge = %d %v after %d calls, want boom after one", n, err, len(f.limits))
	}
}

func TestPurgeStopsWhenTheContextEnds(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	f := &fakePurger{script: []int{3}}
	if n, err := purgeIdempotencyKeys(ctx, f, 3); err != nil || n != 0 || len(f.limits) != 0 {
		t.Fatalf("purge on a cancelled ctx = %d %v calls %d, want none", n, err, len(f.limits))
	}
}

// The loop purges at once, again every tick, survives a failing pass and
// returns when the context ends.
func TestPurgeLoopRunsAtOnceThenEveryTickAndStopsOnCancel(t *testing.T) {
	f := &fakePurger{err: errors.New("down"), calls: make(chan struct{}, 16)}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		defer close(done)
		purgeLoop(ctx, slog.New(slog.NewTextHandler(io.Discard, nil)), f, 5*time.Millisecond, 10)
	}()
	for i := 0; i < 3; i++ {
		select {
		case <-f.calls:
		case <-time.After(5 * time.Second):
			t.Fatalf("pass %d never ran", i+1)
		}
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the loop did not stop with its context")
	}
}
