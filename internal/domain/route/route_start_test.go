package route

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/claudioed/last-mile-delivery/internal/domain/shared"
)

func TestStartRaisesEveryOutForDeliveryBeforeTheEtaWindows(t *testing.T) {
	r := planned(t)
	events, err := r.Start(t1, closedWave(t))
	wantNoErr(t, err)
	if len(events) != 5 {
		t.Fatalf("events = %d, want 5", len(events))
	}
	for i, ev := range events[:3] {
		if ev.EventName() != "PackageOutForDelivery" {
			t.Fatalf("event %d = %s, want PackageOutForDelivery first", i, ev.EventName())
		}
	}
	h := shared.Header{Version: 2, At: t1}
	want := []shared.Event{
		StopEtaWindowAssigned{Header: h, RouteID: "route-1", StopNo: 1, StopKey: "k-a", TrackingNumbers: []string{"TRK-1", "TRK-2"}, EtaStart: t1.Add(time.Hour), EtaEnd: t1.Add(3 * time.Hour)},
		StopEtaWindowAssigned{Header: h, RouteID: "route-1", StopNo: 2, StopKey: "k-b", TrackingNumbers: []string{"TRK-3"}, EtaStart: t1.Add(5 * time.Hour), EtaEnd: t1.Add(7 * time.Hour)},
	}
	for i, w := range want {
		got := events[3+i]
		if !reflect.DeepEqual(got, w) {
			t.Fatalf("eta event %d = %#v, want %#v", i, got, w)
		}
		if got.EventName() != "StopEtaWindowAssigned" || got.Entity() != "route" || got.SubjectID() != "route-1" {
			t.Fatal("eta event shape wrong")
		}
	}
}

func TestStartRaisesNoEtaWhenTheWindowIsNearlyOver(t *testing.T) {
	r := planned(t)
	late := t1.Add(8*time.Hour - MinEtaSpan + time.Minute)
	events, err := r.Start(late, closedWave(t))
	wantNoErr(t, err)
	assertState(t, r, StateOutForDelivery, 2)
	if len(events) != 3 {
		t.Fatalf("events = %d, want only the 3 PackageOutForDelivery", len(events))
	}
	for _, ev := range events {
		if ev.EventName() != "PackageOutForDelivery" {
			t.Fatalf("unexpected %s", ev.EventName())
		}
	}
}

func TestStartBeforeTheWindowOpensUsesTheWindowStart(t *testing.T) {
	r := planned(t)
	events, err := r.Start(t0, closedWave(t))
	wantNoErr(t, err)
	first, ok := events[3].(StopEtaWindowAssigned)
	if !ok || first.EtaStart.Before(t1) || !first.At.Equal(t0) {
		t.Fatalf("first eta = %#v, want a window inside the wave window stamped with the start instant", events[3])
	}
}

func TestStartNeedsTheWaveAndLeavesTheRouteUntouchedOnError(t *testing.T) {
	r := planned(t)
	if _, err := r.Start(t1, nil); !isValidation(err) {
		t.Fatalf("nil wave: err = %v, want a validation error", err)
	}
	assertState(t, r, StatePlanned, 1)
	again := started(t)
	_, err := again.Start(t1, closedWave(t))
	wantIs(t, err, shared.ErrIllegalTransition)
	assertState(t, again, StateOutForDelivery, 2)
}

func isValidation(err error) bool { return errors.Is(err, shared.ErrValidation) }
