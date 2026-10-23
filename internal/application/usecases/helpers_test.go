package usecases_test

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/claudioed/last-mile-delivery/internal/adapters/outbound/memory"
	"github.com/claudioed/last-mile-delivery/internal/application/usecases"
	"github.com/claudioed/last-mile-delivery/internal/application/views"
	"github.com/claudioed/last-mile-delivery/internal/domain/partner"
	"github.com/claudioed/last-mile-delivery/internal/domain/route"
	"github.com/claudioed/last-mile-delivery/internal/domain/wave"
)

var t0 = time.Date(2026, 10, 24, 6, 0, 0, 0, time.UTC)

// env is one in-memory service: every double over one store.
type env struct {
	store *memory.Store
	clock *memory.Clock
	deps  usecases.Deps
}

func newEnv(t *testing.T) *env {
	t.Helper()
	store := memory.NewStore()
	clock := memory.NewClock(t0)
	return &env{store: store, clock: clock, deps: usecases.Deps{
		Waves: store.Waves(), Routes: store.Routes(), Partners: store.Partners(), Histories: store.Histories(),
		Stations: store.Stations(), ServiceAreas: store.ServiceAreas(), PackageStops: store.PackageStops(), Inbound: store.Inbound(),
		Inbox: store.Inbox(), Outbox: store.Outbox(), UoW: store.UnitOfWork(), Clock: clock, IDs: &memory.SequentialIDs{},
	}}
}

func noErr(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func wantIs(t *testing.T, err, target error) {
	t.Helper()
	if !errors.Is(err, target) {
		t.Fatalf("error = %v, want %v", err, target)
	}
}

// eventNames returns "Name:subject" of every outbox event in order.
func (e *env) eventNames() []string {
	var out []string
	for _, ev := range e.store.Outbox().Events() {
		out = append(out, ev.EventName()+":"+ev.SubjectID())
	}
	return out
}

func (e *env) wantEvents(t *testing.T, want ...string) {
	t.Helper()
	got := e.eventNames()
	if len(got) == 0 && len(want) == 0 {
		return
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("outbox events = %v, want %v", got, want)
	}
}

// wantUnchanged runs fn and fails unless the store (tables, versions, outbox)
// is exactly as before.
func (e *env) wantUnchanged(t *testing.T, fn func()) {
	t.Helper()
	before := e.store.Counts()
	fn()
	if after := e.store.Counts(); after != before {
		t.Fatalf("a failed use case changed the store: %+v -> %+v", before, after)
	}
}

func (e *env) station(t *testing.T, id string) {
	t.Helper()
	noErr(t, e.deps.Stations.Save(context.Background(), views.Station{NodeID: id, NodeType: views.DeliveryStation, RegionID: "region-se", Timezone: "America/Sao_Paulo"}))
}

// wave schedules a wave of node-ds-01 whose cutoff is cutoffHours after t0.
func (e *env) wave(t *testing.T, cutoffHours int, capacity int) views.Wave {
	t.Helper()
	cutoff := t0.Add(time.Duration(cutoffHours) * time.Hour)
	v, err := usecases.NewScheduleWave(e.deps).Execute(context.Background(), usecases.ScheduleWaveCommand{
		StationNodeID: "node-ds-01", ServiceDate: "2026-10-24", InductCutoffAt: cutoff,
		WindowStart: cutoff.Add(4 * time.Hour), WindowEnd: cutoff.Add(12 * time.Hour), StopCapacity: capacity,
	})
	noErr(t, err)
	return v
}

func (e *env) label(t *testing.T, id, tn, stopKey string) {
	t.Helper()
	noErr(t, usecases.NewApplyLabelIssued(e.deps).Execute(context.Background(), usecases.LabelIssuedCommand{
		EventID: id, EventType: "LabelIssued", TrackingNumber: tn, AssignmentID: "asg-" + tn, StopKey: stopKey, DestinationPostalPrefix: "01310",
	}))
}

func (e *env) arrive(t *testing.T, id string, tns ...string) {
	t.Helper()
	noErr(t, usecases.NewInductOnArrival(e.deps).Execute(context.Background(), e.arrival(id, tns...)))
}

func (e *env) arrival(id string, tns ...string) usecases.LoadArrivedCommand {
	return usecases.LoadArrivedCommand{
		EventID: id, EventType: "LoadArrived", LoadID: "load-" + id, DepartureID: "dep-1", DestinationNodeID: "node-ds-01",
		NodeType: views.DeliveryStation, TrackingNumbers: tns, ArrivedAt: t0,
	}
}

func (e *env) partner(t *testing.T, capacity int) views.Partner {
	t.Helper()
	p, err := usecases.NewContractPartner(e.deps).Execute(context.Background(), usecases.ContractPartnerCommand{StationNodeID: "node-ds-01", VanCapacity: capacity})
	noErr(t, err)
	return p
}

func (e *env) loadWave(t *testing.T, id string) *wave.DeliveryWave {
	t.Helper()
	w, err := e.deps.Waves.Get(context.Background(), wave.ID(id))
	noErr(t, err)
	return w
}

func (e *env) loadRoute(t *testing.T, id string) *route.Route {
	t.Helper()
	r, err := e.deps.Routes.Get(context.Background(), route.ID(id))
	noErr(t, err)
	return r
}

func (e *env) closeAndPlan(t *testing.T, waveID string, dsp string, stops ...route.Stop) views.Route {
	t.Helper()
	ctx := context.Background()
	if e.loadWave(t, waveID).State() == wave.StateOpen {
		_, err := usecases.NewCloseInduction(e.deps).Execute(ctx, wave.ID(waveID))
		noErr(t, err)
	}
	r, err := usecases.NewPlanRoute(e.deps).Execute(ctx, usecases.PlanRouteCommand{WaveID: wave.ID(waveID), DSPID: partner.ID(dsp), Stops: stops})
	noErr(t, err)
	return r
}

func stop(no int, tns ...string) route.Stop { return route.Stop{No: no, TrackingNumbers: tns} }
