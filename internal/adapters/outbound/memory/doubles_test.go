package memory_test

import (
	"context"
	"testing"
	"time"

	"github.com/claudioed/last-mile-delivery/internal/adapters/outbound/memory"
	"github.com/claudioed/last-mile-delivery/internal/application/outbox"
)

var t0 = time.Date(2026, 10, 24, 6, 0, 0, 0, time.UTC)

// The Clock double must read, move and rewind deterministically: the scenarios
// and the use-case tests schedule waves relative to it.
func TestClockReadsSetAndAdvance(t *testing.T) {
	tests := []struct {
		name  string
		steps func(c *memory.Clock)
		want  time.Time
	}{
		{"a new clock reads its start time", func(*memory.Clock) {}, t0},
		{"set moves the clock", func(c *memory.Clock) { c.Set(t0.Add(3 * time.Hour)) }, t0.Add(3 * time.Hour)},
		{"advance adds to the reading", func(c *memory.Clock) { c.Advance(90 * time.Minute) }, t0.Add(90 * time.Minute)},
		{"a negative advance winds the clock back", func(c *memory.Clock) { c.Advance(-time.Hour) }, t0.Add(-time.Hour)},
		{"set then advance chain", func(c *memory.Clock) {
			c.Set(t0.Add(2 * time.Hour))
			c.Advance(-30 * time.Minute)
		}, t0.Add(90 * time.Minute)},
		{"advance on the zero clock still adds", func(c *memory.Clock) {
			c.Set(time.Time{})
			c.Advance(time.Minute)
		}, time.Time{}.Add(time.Minute)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := memory.NewClock(t0)
			tc.steps(c)
			if got := c.Now(); !got.Equal(tc.want) {
				t.Fatalf("the clock reads %s, want %s", got, tc.want)
			}
		})
	}
}

func msg(id string) outbox.Message {
	return outbox.Message{EventID: id, Subject: id, Key: []byte(id), Value: []byte(id)}
}

func ids(msgs []outbox.Message) []string {
	out := make([]string, len(msgs))
	for i, m := range msgs {
		out[i] = m.EventID
	}
	return out
}

// Messages is the test inspection helper: it must keep the insertion order of
// every row, published or not, so a test can assert the event sequence.
func TestOutboxRepoMessagesKeepsInsertionOrder(t *testing.T) {
	r := memory.NewOutboxRepo()
	if err := r.Insert(context.Background(), msg("a"), msg("b"), msg("c")); err != nil {
		t.Fatal(err)
	}
	published, err := r.Drain(context.Background(), 2, 0, func(context.Context, outbox.Message) error { return nil })
	if err != nil || published != 2 {
		t.Fatalf("drain published %d (%v), want 2", published, err)
	}
	got, want := ids(r.Messages()), []string{"a", "b", "c"}
	if len(got) != len(want) {
		t.Fatalf("the outbox holds %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("the outbox holds %v, want %v", got, want)
		}
	}
}

// Snapshot must restore the rows exactly as they were, including the published
// flags and per-row attempt state, so one test can probe Drain's side effects.
func TestOutboxRepoSnapshotRestoresTheRows(t *testing.T) {
	r := memory.NewOutboxRepo()
	if err := r.Insert(context.Background(), msg("a"), msg("b")); err != nil {
		t.Fatal(err)
	}
	restore := r.Snapshot()
	if _, err := r.Drain(context.Background(), 10, 0, func(context.Context, outbox.Message) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if err := r.Insert(context.Background(), msg("c")); err != nil {
		t.Fatal(err)
	}
	if n := r.Unpublished(); n != 1 {
		t.Fatalf("after the drain and the insert %d rows are unpublished, want 1", n)
	}
	restore()
	if n := r.Unpublished(); n != 2 {
		t.Fatalf("after the restore %d rows are unpublished, want 2", n)
	}
	got, want := ids(r.Messages()), []string{"a", "b"}
	if len(got) != len(want) {
		t.Fatalf("after the restore the outbox holds %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("after the restore the outbox holds %v, want %v", got, want)
		}
	}
	// The restored rows are the snapshot's own copies: a restore after more
	// mutations still resets the row set, not the flags changed since.
	if _, err := r.Drain(context.Background(), 10, 0, func(context.Context, outbox.Message) error { return nil }); err != nil {
		t.Fatal(err)
	}
	restore()
	if msgs := r.Messages(); len(msgs) != 2 || msgs[0].EventID != "a" || msgs[1].EventID != "b" {
		t.Fatalf("a second restore leaves the outbox at %v, want [a b]", ids(msgs))
	}
}

// Snapshot of an empty outbox restores an empty outbox.
func TestOutboxRepoSnapshotOfAnEmptyOutbox(t *testing.T) {
	r := memory.NewOutboxRepo()
	restore := r.Snapshot()
	if err := r.Insert(context.Background(), msg("a")); err != nil {
		t.Fatal(err)
	}
	restore()
	if msgs := r.Messages(); len(msgs) != 0 {
		t.Fatalf("the restored outbox holds %v, want none", ids(msgs))
	}
}
