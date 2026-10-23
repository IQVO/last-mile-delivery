package repocontract

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/claudioed/last-mile-delivery/internal/domain/wave"
)

var unitOfWorkCases = []named{
	{"a returned nil commits every save", uowCommit},
	{"a returned error rolls every save back and is returned unchanged", uowRollback},
	{"a panic rolls back and propagates", uowPanic},
	{"a nested Do joins the outer unit", uowNested},
	{"the outbox commits and rolls back with the aggregates", uowOutbox},
}

var errBoom = errors.New("boom")

func twoWaves(t *testing.T, f Fixture, ctx context.Context) error {
	t.Helper()
	if err := f.Waves.Save(ctx, NewWave(t, "wave-1", "node-ds-01", "2026-10-24", 2)); err != nil {
		return err
	}
	return f.Waves.Save(ctx, NewWave(t, "wave-2", "node-ds-01", "2026-10-24", 3))
}

func waveStored(f Fixture, id wave.ID) bool {
	_, err := f.Waves.Get(context.Background(), id)
	return err == nil
}

func uowCommit(t *testing.T, f Fixture) {
	noErr(t, f.UoW.Do(ctxb(), func(ctx context.Context) error { return twoWaves(t, f, ctx) }))
	if !waveStored(f, "wave-1") || !waveStored(f, "wave-2") {
		t.Fatal("a committed unit lost a save")
	}
}

func uowRollback(t *testing.T, f Fixture) {
	err := f.UoW.Do(ctxb(), func(ctx context.Context) error {
		noErr(t, twoWaves(t, f, ctx))
		return errBoom
	})
	wantErr(t, err, errBoom)
	if waveStored(f, "wave-1") || waveStored(f, "wave-2") {
		t.Fatal("a failed unit left a save behind")
	}
}

func uowPanic(t *testing.T, f Fixture) {
	defer func() {
		if recover() == nil {
			t.Fatal("the panic was swallowed")
		}
		if waveStored(f, "wave-1") {
			t.Fatal("a panicking unit left a save behind")
		}
	}()
	_ = f.UoW.Do(ctxb(), func(ctx context.Context) error {
		noErr(t, f.Waves.Save(ctx, NewWave(t, "wave-1", "node-ds-01", "2026-10-24", 2)))
		panic("kaboom")
	})
}

func uowNested(t *testing.T, f Fixture) {
	err := f.UoW.Do(ctxb(), func(ctx context.Context) error {
		noErr(t, f.UoW.Do(ctx, func(ctx context.Context) error {
			return f.Waves.Save(ctx, NewWave(t, "wave-1", "node-ds-01", "2026-10-24", 2))
		}))
		return errBoom
	})
	wantErr(t, err, errBoom)
	if waveStored(f, "wave-1") {
		t.Fatal("the inner unit committed on its own")
	}
}

func uowOutbox(t *testing.T, f Fixture) {
	if f.OutboxCount == nil {
		t.Skip("the fixture cannot count its outbox")
	}
	w, events, err := wave.Schedule(wave.NewWave{
		ID: "wave-1", StationNodeID: "node-ds-01", ServiceDate: "2026-10-24", InductCutoffAt: Now.Add(time.Hour),
		WindowStart: Now.Add(2 * time.Hour), WindowEnd: Now.Add(3 * time.Hour), StopCapacity: 1,
	}, Now)
	noErr(t, err)
	failed := f.UoW.Do(ctxb(), func(ctx context.Context) error {
		noErr(t, f.Waves.Save(ctx, w))
		noErr(t, f.Outbox.Append(ctx, events...))
		return errBoom
	})
	wantErr(t, failed, errBoom)
	assertOutbox(t, f, 0)
	noErr(t, f.UoW.Do(ctxb(), func(ctx context.Context) error {
		noErr(t, f.Waves.Save(ctx, w))
		return f.Outbox.Append(ctx, events...)
	}))
	assertOutbox(t, f, len(events))
}

func assertOutbox(t *testing.T, f Fixture, want int) {
	t.Helper()
	got, err := f.OutboxCount(context.Background())
	noErr(t, err)
	if got != want {
		t.Fatalf("outbox holds %d events, want %d", got, want)
	}
}
