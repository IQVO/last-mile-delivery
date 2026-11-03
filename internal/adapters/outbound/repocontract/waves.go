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
	{unknownIDCase, waveGetMiss},
	{"a create of a stored id and a stale or orphan update are ErrConcurrentUpdate", waveConcurrency},
	{"every later save stores the new inductions and the new state", waveUpdates},
	{"list filters by station, date, state and cutoff, ordered by cutoff then id", waveList},
}

func waveRoundTrip(t *testing.T, f Fixture) {
	w := NewWave(t, wave1, station1, "2026-10-24", 2)
	noErr(t, f.Waves.Save(ctxb(), w))
	_, err := w.Induct(trk1, stopKeyA, Now, Now)
	noErr(t, err)
	noErr(t, f.Waves.Save(ctxb(), w))
	_, err = w.Induct(trk2, stopKeyA, Now, Now.Add(time.Minute))
	noErr(t, err)
	noErr(t, f.Waves.Save(ctxb(), w))
	got, err := f.Waves.Get(ctxb(), wave1)
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
	w := NewWave(t, wave1, station1, "2026-10-24", 2)
	noErr(t, f.Waves.Save(ctxb(), w))
	wantErr(t, f.Waves.Save(ctxb(), NewWave(t, wave1, station1, "2026-10-24", 3)), ports.ErrConcurrentUpdate)
	winner, err := f.Waves.Get(ctxb(), wave1)
	noErr(t, err)
	loser, err := f.Waves.Get(ctxb(), wave1)
	noErr(t, err)
	noErr(t, winner.CloseInduction())
	noErr(t, loser.CloseInduction())
	noErr(t, f.Waves.Save(ctxb(), winner))
	wantErr(t, f.Waves.Save(ctxb(), loser), ports.ErrConcurrentUpdate)
	orphan := NewWave(t, "wave-9", station1, "2026-10-24", 2)
	noErr(t, orphan.CloseInduction()) // version 2 of a wave never stored
	wantErr(t, f.Waves.Save(ctxb(), orphan), ports.ErrConcurrentUpdate)
}

func waveUpdates(t *testing.T, f Fixture) {
	w := NewWave(t, wave1, station1, "2026-10-24", 2)
	noErr(t, f.Waves.Save(ctxb(), w))
	for i, tn := range []string{trk1, trk2, trk3} {
		got, err := f.Waves.Get(ctxb(), wave1)
		noErr(t, err)
		_, err = got.Induct(tn, []string{stopKeyA, stopKeyA, stopKeyB}[i], Now, Now)
		noErr(t, err)
		noErr(t, f.Waves.Save(ctxb(), got))
	}
	got, err := f.Waves.Get(ctxb(), wave1)
	noErr(t, err)
	if len(got.Inductions()) != 3 || got.AssignedStops() != 2 || got.Version() != 4 {
		t.Fatalf("wave = %+v", got.Snapshot())
	}
	_, err = got.Cancel("closed", Now)
	noErr(t, err)
	noErr(t, f.Waves.Save(ctxb(), got))
	again, err := f.Waves.Get(ctxb(), wave1)
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
	closed := NewWave(t, waveD, station1, "2026-10-24", 4)
	noErr(t, f.Waves.Save(ctxb(), closed))
	noErr(t, closed.CloseInduction())
	noErr(t, f.Waves.Save(ctxb(), closed))
	for _, w := range []*wave.DeliveryWave{
		NewWave(t, waveB, station1, "2026-10-24", 3),
		NewWave(t, waveA, station1, "2026-10-24", 3), // same cutoff as wave-b: ordered by id
		NewWave(t, waveC, station1, "2026-10-25", 2),
		NewWave(t, waveE, station2, "2026-10-24", 1),
	} {
		noErr(t, f.Waves.Save(ctxb(), w))
	}
	cases := []struct {
		name   string
		filter repository.WaveFilter
		want   []wave.ID
	}{
		{"no filter", repository.WaveFilter{}, []wave.ID{waveE, waveC, waveA, waveB, waveD}},
		{"station", repository.WaveFilter{StationNodeID: station1}, []wave.ID{waveC, waveA, waveB, waveD}},
		{"station and date", repository.WaveFilter{StationNodeID: station1, ServiceDate: "2026-10-24"}, []wave.ID{waveA, waveB, waveD}},
		{"state", repository.WaveFilter{State: wave.StateInductionClosed}, []wave.ID{waveD}},
		{"open after a cutoff (strictly)", repository.WaveFilter{StationNodeID: station1, State: wave.StateOpen, InductCutoffAfter: Now.Add(2 * time.Hour)}, []wave.ID{waveA, waveB}},
		{"after the last cutoff", repository.WaveFilter{InductCutoffAfter: Now.Add(4 * time.Hour)}, []wave.ID{}},
		{"no match", repository.WaveFilter{StationNodeID: "node-x"}, []wave.ID{}},
		{"limit", repository.WaveFilter{Limit: 2}, []wave.ID{waveE, waveC}},
		{"after a position with a tied cutoff", repository.WaveFilter{After: &repository.WaveAfter{InductCutoffAt: Now.Add(3 * time.Hour), WaveID: waveA}}, []wave.ID{waveB, waveD}},
		{"after and limit", repository.WaveFilter{After: &repository.WaveAfter{InductCutoffAt: Now.Add(2 * time.Hour), WaveID: waveC}, Limit: 1}, []wave.ID{waveA}},
	}
	for _, c := range cases {
		if got := waveIDs(t, f, c.filter); !reflect.DeepEqual(got, c.want) {
			t.Fatalf("%s: ids = %v, want %v", c.name, got, c.want)
		}
	}
}
