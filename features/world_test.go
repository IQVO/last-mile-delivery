package features_test

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/claudioed/last-mile-delivery/internal/adapters/outbound/memory"
	"github.com/claudioed/last-mile-delivery/internal/application/usecases"
	"github.com/claudioed/last-mile-delivery/internal/application/views"
	"github.com/claudioed/last-mile-delivery/internal/domain/partner"
	"github.com/claudioed/last-mile-delivery/internal/domain/route"
	"github.com/claudioed/last-mile-delivery/internal/domain/wave"
)

var t0 = time.Date(2026, 10, 24, 6, 0, 0, 0, time.UTC)

// world is the state of one scenario: one in-memory service.
type world struct {
	store    *memory.Store
	deps     usecases.Deps
	err      error
	failures map[string]int
}

func newWorld() *world {
	store := memory.NewStore()
	clock := memory.NewClock(t0)
	return &world{
		store: store, failures: map[string]int{},
		deps: usecases.Deps{
			Waves: store.Waves(), Routes: store.Routes(), Partners: store.Partners(), Histories: store.Histories(), Stations: store.Stations(),
			ServiceAreas: store.ServiceAreas(), PackageStops: store.PackageStops(), Inbound: store.Inbound(), Inbox: store.Inbox(),
			Outbox: store.Outbox(), UoW: store.UnitOfWork(), Clock: clock, IDs: &memory.SequentialIDs{},
		},
	}
}

func (w *world) ctx() context.Context { return context.Background() }

func (w *world) schedule(name string, cutoffHours, capacity int) error {
	cutoff := t0.Add(time.Duration(cutoffHours) * time.Hour)
	v, err := usecases.NewScheduleWave(w.deps).Execute(w.ctx(), usecases.ScheduleWaveCommand{
		StationNodeID: "node-ds-01", ServiceDate: "2026-10-24", InductCutoffAt: cutoff,
		WindowStart: cutoff.Add(4 * time.Hour), WindowEnd: cutoff.Add(12 * time.Hour), StopCapacity: capacity,
	})
	w.err = err
	if err == nil && v.WaveID != name {
		return fmt.Errorf("the wave got the id %s, the scenario calls it %s", v.WaveID, name)
	}
	return nil
}

func (w *world) arrive(eventID string, tns ...string) error {
	return usecases.NewInductOnArrival(w.deps).Execute(w.ctx(), usecases.LoadArrivedCommand{
		EventID: eventID, EventType: "LoadArrived", LoadID: "load-" + eventID, DepartureID: "dep-1", DestinationNodeID: "node-ds-01",
		NodeType: views.DeliveryStation, TrackingNumbers: tns, ArrivedAt: t0,
	})
}

func (w *world) loadWave(id string) (*wave.DeliveryWave, error) {
	return w.deps.Waves.Get(w.ctx(), wave.ID(id))
}

// stopsOf returns n one-package stops over the first n inducted packages of the wave.
func (w *world) stopsOf(waveID string, n int) ([]route.Stop, error) {
	wv, err := w.loadWave(waveID)
	if err != nil {
		return nil, err
	}
	var stops []route.Stop
	for i, in := range wv.Inductions() {
		if i == n {
			break
		}
		stops = append(stops, route.Stop{No: i + 1, TrackingNumbers: []string{in.TrackingNumber}})
	}
	return stops, nil
}

func (w *world) plan(waveID, dsp string, stops []route.Stop) (views.Route, error) {
	return usecases.NewPlanRoute(w.deps).Execute(w.ctx(), usecases.PlanRouteCommand{WaveID: wave.ID(waveID), DSPID: partner.ID(dsp), Stops: stops})
}

func (w *world) closeInduction(waveID string) error {
	_, err := usecases.NewCloseInduction(w.deps).Execute(w.ctx(), wave.ID(waveID))
	return err
}

// quoted returns every "double quoted" item of s in order.
func quoted(s string) []string {
	var out []string
	for {
		i := strings.IndexByte(s, '"')
		if i < 0 {
			return out
		}
		j := strings.IndexByte(s[i+1:], '"')
		if j < 0 {
			return out
		}
		out = append(out, s[i+1:i+1+j])
		s = s[i+j+2:]
	}
}

func waveID(s string) wave.ID { return wave.ID(s) }

func dspID(s string) partner.ID { return partner.ID(s) }
