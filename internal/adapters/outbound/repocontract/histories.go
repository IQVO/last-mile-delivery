package repocontract

import (
	"reflect"
	"testing"
	"time"

	"github.com/claudioed/last-mile-delivery/internal/application/ports"
)

var historyCases = []named{
	{"save then get round-trips the snapshot", historyRoundTrip},
	{"get of an unknown tracking number is ErrNotFound", historyGetMiss},
	{"stale, duplicate and orphan saves are ErrConcurrentUpdate", historyConcurrency},
	{"list waiting returns the AT_STATION packages of the station, oldest first, up to the limit", historyListWaiting},
}

func historyRoundTrip(t *testing.T, f Fixture) {
	h := NewHistory(t, trk1, station1, 0)
	noErr(t, f.Histories.Save(ctxb(), h))
	got, err := f.Histories.Get(ctxb(), trk1)
	noErr(t, err)
	if !reflect.DeepEqual(got.Snapshot(), h.Snapshot()) {
		t.Fatalf("snapshot = %+v, want %+v", got.Snapshot(), h.Snapshot())
	}
}

func historyGetMiss(t *testing.T, f Fixture) {
	_, err := f.Histories.Get(ctxb(), "nope")
	wantErr(t, err, ports.ErrNotFound)
}

func historyConcurrency(t *testing.T, f Fixture) {
	noErr(t, f.Histories.Save(ctxb(), NewHistory(t, trk1, station1, 0)))
	wantErr(t, f.Histories.Save(ctxb(), NewHistory(t, trk1, station1, 0)), ports.ErrConcurrentUpdate)
	winner, err := f.Histories.Get(ctxb(), trk1)
	noErr(t, err)
	loser, err := f.Histories.Get(ctxb(), trk1)
	noErr(t, err)
	noErr(t, winner.Place())
	noErr(t, loser.Place())
	noErr(t, f.Histories.Save(ctxb(), winner))
	wantErr(t, f.Histories.Save(ctxb(), loser), ports.ErrConcurrentUpdate)
	orphan := NewHistory(t, "TRK-9", station1, 0)
	noErr(t, orphan.Place()) // version 2 of a package never stored
	wantErr(t, f.Histories.Save(ctxb(), orphan), ports.ErrConcurrentUpdate)
}

func historyListWaiting(t *testing.T, f Fixture) {
	for _, h := range [](func() error){
		func() error { return f.Histories.Save(ctxb(), NewHistory(t, trk2, station1, 2*time.Minute)) },
		func() error { return f.Histories.Save(ctxb(), NewHistory(t, trk3, station1, time.Minute)) },
		func() error { return f.Histories.Save(ctxb(), NewHistory(t, trk1, station1, 2*time.Minute)) }, // same arrival as TRK-2: by number
		func() error { return f.Histories.Save(ctxb(), NewHistory(t, trk4, station2, 0)) },
	} {
		noErr(t, h())
	}
	// TRK-5 was inducted: saved at version 1 as waiting, then placed.
	first := NewHistory(t, "TRK-5", station1, 0)
	noErr(t, f.Histories.Save(ctxb(), first))
	noErr(t, first.Place())
	noErr(t, f.Histories.Save(ctxb(), first))
	cases := []struct {
		name    string
		station string
		limit   int
		want    []string
	}{
		{"all", station1, 10, []string{trk3, trk1, trk2}},
		{"limited", station1, 2, []string{trk3, trk1}},
		{"other station", station2, 10, []string{trk4}},
		{"none", "node-x", 10, []string{}},
	}
	for _, c := range cases {
		got, err := f.Histories.ListWaiting(ctxb(), c.station, c.limit)
		noErr(t, err)
		ids := make([]string, len(got))
		for i, h := range got {
			ids[i] = h.TrackingNumber()
		}
		if !reflect.DeepEqual(ids, c.want) {
			t.Fatalf("%s: ids = %v, want %v", c.name, ids, c.want)
		}
	}
}
