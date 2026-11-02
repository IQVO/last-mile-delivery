package repocontract

import (
	"reflect"
	"testing"
	"time"

	"github.com/claudioed/last-mile-delivery/internal/application/ports"
	"github.com/claudioed/last-mile-delivery/internal/application/repository"
	"github.com/claudioed/last-mile-delivery/internal/domain/wave"
)

var waveCases = []named{
	{"save then get round-trips the snapshot with its inductions", waveRoundTrip},
	{"get of an unknown id is ErrNotFound", waveGetMiss},
	{"a create of a stored id and a stale or orphan update are ErrConcurrentUpdate", waveConcurrency},
	{"every later save stores the new inductions and the new state", waveUpdates},
	{"list filters by station, date, state and cutoff, ordered by cutoff then id", waveList},
}

func waveRoundTrip(t *testing.T, f Fixture) {
	w := NewWave(t, "wave-1", "node-ds-01", "2026-10-24", 2)
	noErr(t, f.Waves.Save(ctxb(), w))
	_, err := w.Induct("TRK-1", "k-a", Now, Now)
	noErr(t, err)
	noErr(t, f.Waves.Save(ctxb(), w))
	_, err = w.Induct("TRK-2", "k-a", Now, Now.Add(time.Minute))
	noErr(t, err)
	noErr(t, f.Waves.Save(ctxb(), w))
	got, err := f.Waves.Get(ctxb(), "wave-1")
	noErr(t, err)
	if !reflect.DeepEqual(got.Snapshot(), w.Snapshot()) {
		t.Fatalf("snapshot = %+v, want %+v", got.Snapshot(), w.Snapshot())
	}
}

func waveGetMiss(t *testing.T, f Fixture) {
	_, err := f.Waves.Get(ctxb(), "nope")
	wantErr(t, err, ports.ErrNotFound)
}

func waveConcurrency(t *testing.T, f Fixture) {
	w := NewWave(t, "wave-1", "node-ds-01", "2026-10-24", 2)
	noErr(t, f.Waves.Save(ctxb(), w))
	wantErr(t, f.Waves.Save(ctxb(), NewWave(t, "wave-1", "node-ds-01", "2026-10-24", 3)), ports.ErrConcurrentUpdate)
	winner, err := f.Waves.Get(ctxb(), "wave-1")
	noErr(t, err)
	loser, err := f.Waves.Get(ctxb(), "wave-1")
	noErr(t, err)
	noErr(t, winner.CloseInduction())
	noErr(t, loser.CloseInduction())
	noErr(t, f.Waves.Save(ctxb(), winner))
	wantErr(t, f.Waves.Save(ctxb(), loser), ports.ErrConcurrentUpdate)
	orphan := NewWave(t, "wave-9", "node-ds-01", "2026-10-24", 2)
	noErr(t, orphan.CloseInduction()) // version 2 of a wave never stored
	wantErr(t, f.Waves.Save(ctxb(), orphan), ports.ErrConcurrentUpdate)
}

func waveUpdates(t *testing.T, f Fixture) {
	w := NewWave(t, "wave-1", "node-ds-01", "2026-10-24", 2)
	noErr(t, f.Waves.Save(ctxb(), w))
	for i, tn := range []string{"TRK-1", "TRK-2", "TRK-3"} {
		got, err := f.Waves.Get(ctxb(), "wave-1")
		noErr(t, err)
		_, err = got.Induct(tn, []string{"k-a", "k-a", "k-b"}[i], Now, Now)
		noErr(t, err)
		noErr(t, f.Waves.Save(ctxb(), got))
	}
	got, err := f.Waves.Get(ctxb(), "wave-1")
	noErr(t, err)
	if len(got.Inductions()) != 3 || got.AssignedStops() != 2 || got.Version() != 4 {
		t.Fatalf("wave = %+v", got.Snapshot())
	}
	_, err = got.Cancel("closed", Now)
	noErr(t, err)
	noErr(t, f.Waves.Save(ctxb(), got))
	again, err := f.Waves.Get(ctxb(), "wave-1")
	noErr(t, err)
	if again.State() != wave.StateCancelled || again.CancelReason() != "closed" || len(again.Inductions()) != 3 {
		t.Fatalf("wave = %+v", again.Snapshot())
	}
}

func waveIDs(t *testing.T, f Fixture, filter repository.WaveFilter) []wave.ID {
	t.Helper()
	got, err := f.Waves.List(ctxb(), filter)
	noErr(t, err)
	ids := make([]wave.ID, len(got))
	for i, w := range got {
		ids[i] = w.ID()
	}
	return ids
}

func waveList(t *testing.T, f Fixture) {
	closed := NewWave(t, "wave-d", "node-ds-01", "2026-10-24", 4)
	noErr(t, f.Waves.Save(ctxb(), closed))
	noErr(t, closed.CloseInduction())
	noErr(t, f.Waves.Save(ctxb(), closed))
	for _, w := range []*wave.DeliveryWave{
		NewWave(t, "wave-b", "node-ds-01", "2026-10-24", 3),
		NewWave(t, "wave-a", "node-ds-01", "2026-10-24", 3), // same cutoff as wave-b: ordered by id
		NewWave(t, "wave-c", "node-ds-01", "2026-10-25", 2),
		NewWave(t, "wave-e", "node-ds-02", "2026-10-24", 1),
	} {
		noErr(t, f.Waves.Save(ctxb(), w))
	}
	cases := []struct {
		name   string
		filter repository.WaveFilter
		want   []wave.ID
	}{
		{"no filter", repository.WaveFilter{}, []wave.ID{"wave-e", "wave-c", "wave-a", "wave-b", "wave-d"}},
		{"station", repository.WaveFilter{StationNodeID: "node-ds-01"}, []wave.ID{"wave-c", "wave-a", "wave-b", "wave-d"}},
		{"station and date", repository.WaveFilter{StationNodeID: "node-ds-01", ServiceDate: "2026-10-24"}, []wave.ID{"wave-a", "wave-b", "wave-d"}},
		{"state", repository.WaveFilter{State: wave.StateInductionClosed}, []wave.ID{"wave-d"}},
		{"open after a cutoff (strictly)", repository.WaveFilter{StationNodeID: "node-ds-01", State: wave.StateOpen, InductCutoffAfter: Now.Add(2 * time.Hour)}, []wave.ID{"wave-a", "wave-b"}},
		{"after the last cutoff", repository.WaveFilter{InductCutoffAfter: Now.Add(4 * time.Hour)}, []wave.ID{}},
		{"no match", repository.WaveFilter{StationNodeID: "node-x"}, []wave.ID{}},
		{"limit", repository.WaveFilter{Limit: 2}, []wave.ID{"wave-e", "wave-c"}},
		{"after a position with a tied cutoff", repository.WaveFilter{After: &repository.WaveAfter{InductCutoffAt: Now.Add(3 * time.Hour), WaveID: "wave-a"}}, []wave.ID{"wave-b", "wave-d"}},
		{"after and limit", repository.WaveFilter{After: &repository.WaveAfter{InductCutoffAt: Now.Add(2 * time.Hour), WaveID: "wave-c"}, Limit: 1}, []wave.ID{"wave-a"}},
	}
	for _, c := range cases {
		if got := waveIDs(t, f, c.filter); !reflect.DeepEqual(got, c.want) {
			t.Fatalf("%s: ids = %v, want %v", c.name, got, c.want)
		}
	}
}
