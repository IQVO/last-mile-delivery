package repocontract

import (
	"reflect"
	"testing"
	"time"

	"github.com/claudioed/last-mile-delivery/internal/application/ports"
	"github.com/claudioed/last-mile-delivery/internal/domain/deliverypoint"
	"github.com/claudioed/last-mile-delivery/internal/domain/route"
)

// concurrencyCase names the shared optimistic-concurrency case of every aggregate.
const concurrencyCase = "stale, duplicate and orphan saves are ErrConcurrentUpdate"

var deliveryPointCases = []named{
	{"save then get round-trips every field", deliveryPointRoundTrip},
	{unknownIDCase, deliveryPointGetMiss},
	{concurrencyCase, deliveryPointConcurrency},
	{"get many returns the profiles that exist and omits the misses", deliveryPointGetMany},
}

// observed folds one attempt into the profile, failing the test on an error.
func observed(t testing.TB, p *deliverypoint.Profile, routeID, tn string, o route.Outcome, d route.Detail, after time.Duration) {
	t.Helper()
	at := Now.Add(after)
	_, err := p.Observe(deliverypoint.Observation{RouteID: routeID, TrackingNumber: tn, Outcome: o, Detail: d, At: at}, at)
	noErr(t, err)
}

// NewDeliveryPoint builds a profile at version 1 that holds one safe-place
// delivery.
func NewDeliveryPoint(t testing.TB, key string) *deliverypoint.Profile {
	t.Helper()
	p, err := deliverypoint.New(key)
	noErr(t, err)
	observed(t, p, "route-0", trk3, route.OutcomeDelivered, route.DetailSafePlaceUsed, 0)
	return p
}

// step observes one attempt and saves the profile (a create at version 1, then
// updates), so a case walks the whole persistence path.
func step(t testing.TB, f Fixture, p *deliverypoint.Profile, routeID, tn string, o route.Outcome, d route.Detail, after time.Duration) {
	t.Helper()
	observed(t, p, routeID, tn, o, d, after)
	noErr(t, f.DeliveryPoints.Save(ctxb(), p))
}

func deliveryPointRoundTrip(t *testing.T, f Fixture) {
	p := NewDeliveryPoint(t, pointKeyA) // a safe-place delivery, version 1
	noErr(t, f.DeliveryPoints.Save(ctxb(), p))
	step(t, f, p, "route-1", trk4, route.OutcomeFailed, "", time.Minute)
	step(t, f, p, "route-2", trk1, route.OutcomeFailed, route.DetailNotHome, time.Hour)
	step(t, f, p, "route-3", trk2, route.OutcomeFailed, route.DetailAccessProblem, 2*time.Hour)
	want := p.Snapshot()
	if want.FlaggedAt.IsZero() || len(want.Strikes) != 2 || !want.Strikes[0].Notified || want.SafePlaceUsed != 1 || want.FailedOther != 1 || want.Version != 4 {
		t.Fatalf("the fixture must carry every kind of fact: %+v", want)
	}
	got, err := f.DeliveryPoints.Get(ctxb(), pointKeyA)
	noErr(t, err)
	if !reflect.DeepEqual(got.Snapshot(), want) {
		t.Fatalf("snapshot = %+v, want %+v", got.Snapshot(), want)
	}
	// A delivery on a route with no strike ends the episode: strikes and flag go.
	step(t, f, got, "route-4", trk3, route.OutcomeDelivered, "", 3*time.Hour)
	again, err := f.DeliveryPoints.Get(ctxb(), pointKeyA)
	noErr(t, err)
	if !reflect.DeepEqual(again.Snapshot(), got.Snapshot()) || again.Flagged() || again.OpenStrikes() != 0 || again.Version() != 5 {
		t.Fatalf("snapshot = %+v, want %+v", again.Snapshot(), got.Snapshot())
	}
}

func deliveryPointGetMiss(t *testing.T, f Fixture) {
	_, err := f.DeliveryPoints.Get(ctxb(), "nope")
	wantErr(t, err, ports.ErrNotFound)
}

func deliveryPointConcurrency(t *testing.T, f Fixture) {
	noErr(t, f.DeliveryPoints.Save(ctxb(), NewDeliveryPoint(t, pointKeyA)))
	wantErr(t, f.DeliveryPoints.Save(ctxb(), NewDeliveryPoint(t, pointKeyA)), ports.ErrConcurrentUpdate)
	winner, err := f.DeliveryPoints.Get(ctxb(), pointKeyA)
	noErr(t, err)
	loser, err := f.DeliveryPoints.Get(ctxb(), pointKeyA)
	noErr(t, err)
	observed(t, winner, "route-1", trk1, route.OutcomeFailed, route.DetailNotHome, time.Hour)
	observed(t, loser, "route-2", trk2, route.OutcomeFailed, route.DetailNotHome, time.Hour)
	noErr(t, f.DeliveryPoints.Save(ctxb(), winner))
	wantErr(t, f.DeliveryPoints.Save(ctxb(), loser), ports.ErrConcurrentUpdate)
	orphan := NewDeliveryPoint(t, pointKeyB)
	observed(t, orphan, "route-1", trk1, route.OutcomeFailed, route.DetailNotHome, time.Hour) // version 2 of a key never stored
	wantErr(t, f.DeliveryPoints.Save(ctxb(), orphan), ports.ErrConcurrentUpdate)
}

func deliveryPointGetMany(t *testing.T, f Fixture) {
	none, err := f.DeliveryPoints.GetMany(ctxb(), nil)
	noErr(t, err)
	if none == nil || len(none) != 0 {
		t.Fatalf("no keys: want an empty non-nil map, got %#v", none)
	}
	noErr(t, f.DeliveryPoints.Save(ctxb(), NewDeliveryPoint(t, pointKeyA)))
	noErr(t, f.DeliveryPoints.Save(ctxb(), NewDeliveryPoint(t, pointKeyB)))
	got, err := f.DeliveryPoints.GetMany(ctxb(), []string{pointKeyA, pointKeyC, pointKeyB})
	noErr(t, err)
	if len(got) != 2 || got[pointKeyA] == nil || got[pointKeyB] == nil || got[pointKeyC] != nil {
		t.Fatalf("GetMany = %v, want the two stored keys only", got)
	}
	if got[pointKeyA].StopKey() != pointKeyA || got[pointKeyB].StopKey() != pointKeyB {
		t.Fatal("the map is keyed by stop key")
	}
	miss, err := f.DeliveryPoints.GetMany(ctxb(), []string{pointKeyC})
	noErr(t, err)
	if len(miss) != 0 {
		t.Fatalf("a missing key is absent, not an error: %v", miss)
	}
}
