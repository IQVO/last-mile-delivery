package usecases_test

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/claudioed/last-mile-delivery/internal/application/ports"
	"github.com/claudioed/last-mile-delivery/internal/application/usecases"
	"github.com/claudioed/last-mile-delivery/internal/domain/route"
	"github.com/claudioed/last-mile-delivery/internal/domain/shared"
)

// recordingEta lists the ports.EtaMetrics calls of StartRoute.
type recordingEta struct{ calls []string }

var _ ports.EtaMetrics = (*recordingEta)(nil)

func (m *recordingEta) EtaAssigned(_ context.Context, n int) {
	m.calls = append(m.calls, "assigned/"+string(rune('0'+n)))
}
func (m *recordingEta) EtaSkipped(_ context.Context, reason string) {
	m.calls = append(m.calls, "skipped/"+reason)
}

func etaEvents(e *env) []route.StopEtaWindowAssigned {
	var out []route.StopEtaWindowAssigned
	for _, ev := range e.store.Outbox().Events() {
		if x, ok := ev.(route.StopEtaWindowAssigned); ok {
			out = append(out, x)
		}
	}
	return out
}

// The wave of dispatchedRoute has its delivery window at 12:00 to 20:00.
func TestStartRouteRaisesOneEtaPerStopAfterEveryOutForDelivery(t *testing.T) {
	e := dispatchedRoute(t, false)
	m := &recordingEta{}
	e.deps.EtaMetrics = m
	before := len(e.eventNames())
	e.start(t)
	got := e.eventNames()[before:]
	want := []string{
		"PackageOutForDelivery:route-1", "PackageOutForDelivery:route-1", "PackageOutForDelivery:route-1",
		"StopEtaWindowAssigned:route-1", "StopEtaWindowAssigned:route-1",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("events = %v, want %v", got, want)
	}
	etas := etaEvents(e)
	at := func(h int) time.Time { return time.Date(2026, 10, 24, h, 0, 0, 0, time.UTC) }
	wantEtas := []route.StopEtaWindowAssigned{
		{Header: shared.Header{Version: 2, At: t0}, RouteID: "route-1", StopNo: 1, StopKey: "k-a", TrackingNumbers: []string{"TRK-1", "TRK-2"}, EtaStart: at(13), EtaEnd: at(15)},
		{Header: shared.Header{Version: 2, At: t0}, RouteID: "route-1", StopNo: 2, StopKey: "pkg:TRK-3", TrackingNumbers: []string{"TRK-3"}, EtaStart: at(17), EtaEnd: at(19)},
	}
	if !reflect.DeepEqual(etas, wantEtas) {
		t.Fatalf("eta events = %#v, want %#v", etas, wantEtas)
	}
	if !reflect.DeepEqual(m.calls, []string{"assigned/2"}) {
		t.Fatalf("eta metrics = %v", m.calls)
	}
}

func TestStartRouteLateInTheWindowRaisesNoEtaAndStillStarts(t *testing.T) {
	e := dispatchedRoute(t, false)
	m := &recordingEta{}
	e.deps.EtaMetrics = m
	e.clock.Set(time.Date(2026, 10, 24, 19, 45, 0, 0, time.UTC))
	v, err := usecases.NewStartRoute(e.deps).Execute(context.Background(), "route-1")
	noErr(t, err)
	if v.State != "OUT_FOR_DELIVERY" || len(etaEvents(e)) != 0 {
		t.Fatalf("state = %s, eta events = %d; want a started route and no ETA", v.State, len(etaEvents(e)))
	}
	if !reflect.DeepEqual(m.calls, []string{"skipped/window_elapsed"}) {
		t.Fatalf("eta metrics = %v", m.calls)
	}
}

func TestStartRouteRefusalRecordsNoEtaMetrics(t *testing.T) {
	e := dispatchedRoute(t, false)
	e.start(t)
	m := &recordingEta{}
	e.deps.EtaMetrics = m
	e.wantUnchanged(t, func() {
		_, err := usecases.NewStartRoute(e.deps).Execute(context.Background(), "route-1")
		wantIs(t, err, shared.ErrIllegalTransition)
	})
	if len(m.calls) != 0 {
		t.Fatalf("a refused start recorded %v", m.calls)
	}
}
