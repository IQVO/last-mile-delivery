package repocontract

import (
	"reflect"
	"testing"
	"time"

	"github.com/claudioed/last-mile-delivery/internal/application/ports"
	"github.com/claudioed/last-mile-delivery/internal/domain/partner"
	"github.com/claudioed/last-mile-delivery/internal/domain/route"
	"github.com/claudioed/last-mile-delivery/internal/domain/wave"
)

var routeCases = []named{
	{"save then get round-trips stops and attempts", routeRoundTrip},
	{"get of an unknown id is ErrNotFound", routeGetMiss},
	{"stale, duplicate and orphan saves are ErrConcurrentUpdate", routeConcurrency},
	{"packages in a wave and uncompleted routes count every route of the wave only", routeWaveQueries},
}

var partnerCases = []named{
	{"save then get round-trips the snapshot", partnerRoundTrip},
	{"get of an unknown id is ErrNotFound", partnerGetMiss},
	{"stale saves are ErrConcurrentUpdate", partnerConcurrency},
}

func routeRoundTrip(t *testing.T, f Fixture) {
	r := savedStartedRoute(t, f, "route-1", "wave-1")
	got, err := f.Routes.Get(ctxb(), "route-1")
	noErr(t, err)
	if !reflect.DeepEqual(got.Snapshot(), r.Snapshot()) {
		t.Fatalf("snapshot = %+v, want %+v", got.Snapshot(), r.Snapshot())
	}
	_, err = got.RecordAttempt("TRK-1", route.OutcomeFailed, "absent", Now.Add(time.Hour), Now)
	noErr(t, err)
	noErr(t, f.Routes.Save(ctxb(), got))
	_, err = got.RecordAttempt("TRK-2", route.OutcomeDelivered, "", Now.Add(time.Hour), Now)
	noErr(t, err)
	noErr(t, f.Routes.Save(ctxb(), got))
	again, err := f.Routes.Get(ctxb(), "route-1")
	noErr(t, err)
	if !reflect.DeepEqual(again.Snapshot(), got.Snapshot()) || len(again.Attempts()) != 2 {
		t.Fatalf("snapshot = %+v, want %+v", again.Snapshot(), got.Snapshot())
	}
}

func routeGetMiss(t *testing.T, f Fixture) {
	_, err := f.Routes.Get(ctxb(), "nope")
	wantErr(t, err, ports.ErrNotFound)
}

func routeConcurrency(t *testing.T, f Fixture) {
	r := savedStartedRoute(t, f, "route-1", "wave-1")
	wantErr(t, f.Routes.Save(ctxb(), r), ports.ErrConcurrentUpdate)
	winner, err := f.Routes.Get(ctxb(), "route-1")
	noErr(t, err)
	loser, err := f.Routes.Get(ctxb(), "route-1")
	noErr(t, err)
	_, err = winner.RecordAttempt("TRK-1", route.OutcomeDelivered, "", Now, Now)
	noErr(t, err)
	_, err = loser.RecordAttempt("TRK-2", route.OutcomeDelivered, "", Now, Now)
	noErr(t, err)
	noErr(t, f.Routes.Save(ctxb(), winner))
	wantErr(t, f.Routes.Save(ctxb(), loser), ports.ErrConcurrentUpdate)
	orphan, _ := StartedRoute(t, "route-9", "wave-9")
	_, err = orphan.RecordAttempt("TRK-1", route.OutcomeDelivered, "", Now, Now)
	noErr(t, err)
	wantErr(t, f.Routes.Save(ctxb(), orphan), ports.ErrConcurrentUpdate)
}

func routeWaveQueries(t *testing.T, f Fixture) {
	got, err := f.Routes.PackagesInWave(ctxb(), "wave-1")
	noErr(t, err)
	if got == nil || len(got) != 0 {
		t.Fatalf("no route: want an empty non-nil list, got %#v", got)
	}
	n, err := f.Routes.CountUncompleted(ctxb(), "wave-1")
	noErr(t, err)
	if n != 0 {
		t.Fatalf("uncompleted = %d, want 0", n)
	}
	savedStartedRoute(t, f, "route-1", "wave-1")
	savedStartedRoute(t, f, "route-2", "wave-2")
	got, err = f.Routes.PackagesInWave(ctxb(), "wave-1")
	noErr(t, err)
	if !reflect.DeepEqual(got, []string{"TRK-1", "TRK-2", "TRK-3"}) {
		t.Fatalf("packages = %v", got)
	}
	if n, err = f.Routes.CountUncompleted(ctxb(), "wave-1"); err != nil || n != 1 {
		t.Fatalf("uncompleted = %d, %v, want 1", n, err)
	}
	loaded, err := f.Routes.Get(ctxb(), "route-1")
	noErr(t, err)
	for _, tn := range loaded.Packages() {
		_, err = loaded.RecordAttempt(tn, route.OutcomeDelivered, "", Now, Now)
		noErr(t, err)
		noErr(t, f.Routes.Save(ctxb(), loaded))
	}
	noErr(t, loaded.Complete())
	noErr(t, f.Routes.Save(ctxb(), loaded))
	if n, err = f.Routes.CountUncompleted(ctxb(), "wave-1"); err != nil || n != 0 {
		t.Fatalf("uncompleted after completion = %d, %v, want 0", n, err)
	}
	if n, err = f.Routes.CountUncompleted(ctxb(), "wave-2"); err != nil || n != 1 {
		t.Fatalf("the other wave's route is still open: %d, %v", n, err)
	}
}

func partnerRoundTrip(t *testing.T, f Fixture) {
	p := NewPartner(t, "dsp-1", "node-ds-01")
	noErr(t, f.Partners.Save(ctxb(), p))
	got, err := f.Partners.Get(ctxb(), "dsp-1")
	noErr(t, err)
	if got.Snapshot() != p.Snapshot() {
		t.Fatalf("snapshot = %+v, want %+v", got.Snapshot(), p.Snapshot())
	}
}

func partnerGetMiss(t *testing.T, f Fixture) {
	_, err := f.Partners.Get(ctxb(), "nope")
	wantErr(t, err, ports.ErrNotFound)
}

func partnerConcurrency(t *testing.T, f Fixture) {
	noErr(t, f.Partners.Save(ctxb(), NewPartner(t, "dsp-1", "node-ds-01")))
	wantErr(t, f.Partners.Save(ctxb(), NewPartner(t, "dsp-1", "node-ds-01")), ports.ErrConcurrentUpdate)
	winner, err := f.Partners.Get(ctxb(), "dsp-1")
	noErr(t, err)
	loser, err := f.Partners.Get(ctxb(), "dsp-1")
	noErr(t, err)
	noErr(t, winner.Deactivate())
	noErr(t, loser.Deactivate())
	noErr(t, f.Partners.Save(ctxb(), winner))
	wantErr(t, f.Partners.Save(ctxb(), loser), ports.ErrConcurrentUpdate)
	got, err := f.Partners.Get(ctxb(), "dsp-1")
	noErr(t, err)
	if got.State() != partner.StateInactive {
		t.Fatalf("state = %s", got.State())
	}
}

// savedStartedRoute stores a started route the way the service does: one saved
// version per command (planned, then started).
func savedStartedRoute(t *testing.T, f Fixture, id route.ID, waveID wave.ID) *route.Route {
	t.Helper()
	w := NewWave(t, waveID, "node-ds-01", "2026-10-24", 2)
	for _, in := range [][2]string{{"TRK-1", "k-a"}, {"TRK-2", "k-a"}, {"TRK-3", "k-b"}} {
		_, err := w.Induct(in[0], in[1], Now, Now)
		noErr(t, err)
	}
	noErr(t, w.CloseInduction())
	r, _, err := route.Plan(id, []route.Stop{{No: 1, TrackingNumbers: []string{"TRK-1", "TRK-2"}}, {No: 2, TrackingNumbers: []string{"TRK-3"}}},
		route.Planning{Wave: w, Partner: NewPartner(t, "dsp-1", "node-ds-01")}, Now)
	noErr(t, err)
	noErr(t, f.Routes.Save(ctxb(), r))
	_, err = r.Start(Now)
	noErr(t, err)
	noErr(t, f.Routes.Save(ctxb(), r))
	return r
}
