package features_test

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/cucumber/godog"

	"github.com/claudioed/last-mile-delivery/internal/application/usecases"
	"github.com/claudioed/last-mile-delivery/internal/application/views"
	"github.com/claudioed/last-mile-delivery/internal/domain/route"
)

func initializeScenario(sc *godog.ScenarioContext) {
	w := &world{}
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		*w = *newWorld()
		return ctx, nil
	})
	registerWaveSteps(sc, w)
	registerInductionSteps(sc, w)
	registerRouteSteps(sc, w)
	registerAssertionSteps(sc, w)
}

func registerWaveSteps(sc *godog.ScenarioContext, w *world) {
	sc.Step(`^a delivery station "([^"]*)"$`, func(id string) error {
		return w.deps.Stations.Save(w.ctx(), views.Station{NodeID: id, NodeType: views.DeliveryStation, RegionID: "region-se"})
	})
	sc.Step(`^the planner schedules a wave whose cutoff is after its window start$`, func() error {
		_, w.err = usecases.NewScheduleWave(w.deps).Execute(w.ctx(), usecases.ScheduleWaveCommand{
			StationNodeID: "node-ds-01", ServiceDate: "2026-10-24", InductCutoffAt: t0.Add(5 * time.Hour),
			WindowStart: t0.Add(2 * time.Hour), WindowEnd: t0.Add(6 * time.Hour), StopCapacity: 1,
		})
		return nil
	})
	wave := func(name string, hours, capacity int) error { return w.schedule(name, hours, capacity) }
	sc.Step(`^a wave "([^"]*)" with its cutoff in (\d+) hours and capacity (\d+)$`, wave)
	sc.Step(`^the planner schedules a wave "([^"]*)" with its cutoff in (\d+) hours and capacity (\d+)$`, wave)
	sc.Step(`^induction of wave "([^"]*)" is closed$`, w.closeInduction)
	sc.Step(`^the planner cancels wave "([^"]*)" because "([^"]*)"$`, func(id, reason string) error {
		_, w.err = usecases.NewCancelWave(w.deps).Execute(w.ctx(), usecases.CancelWaveCommand{WaveID: waveID(id), Reason: reason})
		return nil
	})
	sc.Step(`^the planner contracts a partner with a van of (-?\d+)$`, func(capacity int) error {
		_, w.err = usecases.NewContractPartner(w.deps).Execute(w.ctx(), usecases.ContractPartnerCommand{StationNodeID: "node-ds-01", VanCapacity: capacity})
		return nil
	})
	sc.Step(`^the planner contracts a partner for the station "([^"]*)"$`, func(station string) error {
		_, w.err = usecases.NewContractPartner(w.deps).Execute(w.ctx(), usecases.ContractPartnerCommand{StationNodeID: station, VanCapacity: 3})
		return nil
	})
	sc.Step(`^a partner "([^"]*)" with a van of (\d+)$`, func(id string, capacity int) error {
		p, err := usecases.NewContractPartner(w.deps).Execute(w.ctx(), usecases.ContractPartnerCommand{StationNodeID: "node-ds-01", VanCapacity: capacity})
		if err == nil && p.DSPID != id {
			return fmt.Errorf("the partner got the id %s, the scenario calls it %s", p.DSPID, id)
		}
		return err
	})
	sc.Step(`^the partner "([^"]*)" is deactivated$`, func(id string) error {
		_, err := usecases.NewDeactivatePartner(w.deps).Execute(w.ctx(), dspID(id))
		return err
	})
}

func registerInductionSteps(sc *godog.ScenarioContext, w *world) {
	sc.Step(`^the label of "([^"]*)" says stop "([^"]*)"$`, func(tn, key string) error {
		return usecases.NewApplyLabelIssued(w.deps).Execute(w.ctx(), usecases.LabelIssuedCommand{
			EventID: "label-" + tn, EventType: "LabelIssued", TrackingNumber: tn, StopKey: key, AssignmentID: "asg-" + tn,
		})
	})
	sc.Step(`^the packages "([^"]*)" and "([^"]*)" arrive at the station$`, func(a, b string) error { return w.arrive("ev-"+a, a, b) })
	sc.Step(`^the package "([^"]*)" arrives at the station$`, func(a string) error { return w.arrive("ev-"+a, a) })
	sc.Step(`^the package "([^"]*)" arrived and waits at the station$`, func(a string) error { return w.arrive("ev-"+a, a) })
	sc.Step(`^the package "([^"]*)" arrived and is inducted in wave "([^"]*)"$`, func(a, _ string) error { return w.arrive("ev-"+a, a) })
	sc.Step(`^the load event "([^"]*)" with the package "([^"]*)" is delivered twice$`, func(ev, tn string) error {
		if err := w.arrive(ev, tn); err != nil {
			return err
		}
		return w.arrive(ev, tn)
	})
}

func registerRouteSteps(sc *godog.ScenarioContext, w *world) {
	sc.Step(`^the packages (.+) are inducted in wave "([^"]*)" and induction is closed$`, func(list, id string) error {
		if err := w.arrive("ev-list", quoted(list)...); err != nil {
			return err
		}
		return w.closeInduction(id)
	})
	sc.Step(`^the planner plans a route with (\d+) stops$`, func(n int) error {
		stops, err := w.stopsOf("wave-1", n)
		if err != nil {
			return err
		}
		_, w.err = w.plan("wave-1", "dsp-1", stops)
		return nil
	})
	sc.Step(`^a planned route "([^"]*)" with the packages (.+)$`, func(_, list string) error { return w.buildRoute(quoted(list), false) })
	sc.Step(`^a started route "([^"]*)" with the packages (.+)$`, func(_, list string) error { return w.buildRoute(quoted(list), true) })
	sc.Step(`^the package "([^"]*)" already failed twice$`, func(tn string) error { w.failures[tn] = 2; return nil })
	sc.Step(`^the driver starts route "([^"]*)"$`, func(id string) error {
		_, w.err = usecases.NewStartRoute(w.deps).Execute(w.ctx(), route.ID(id))
		return nil
	})
	sc.Step(`^the driver records the package "([^"]*)" as delivered$`, func(tn string) error { return w.attempt(tn, route.OutcomeDelivered, "") })
	sc.Step(`^the driver records the package "([^"]*)" as failed because "([^"]*)"$`, func(tn, reason string) error {
		return w.attempt(tn, route.OutcomeFailed, reason)
	})
}

func (w *world) attempt(tn string, o route.Outcome, reason string) error {
	_, w.err = usecases.NewRecordAttempt(w.deps).Execute(w.ctx(), usecases.RecordAttemptCommand{
		RouteID: "route-1", TrackingNumber: tn, Outcome: o, FailureReason: reason, At: t0.Add(8 * time.Hour),
	})
	return nil
}

// buildRoute inducts the packages into wave-1, applies the failures a scenario
// asked for, closes induction, plans one stop per package and, when started is
// true, dispatches the wave and starts the route.
func (w *world) buildRoute(tns []string, started bool) error {
	if err := w.arrive("ev-route", tns...); err != nil {
		return err
	}
	for tn, n := range w.failures {
		if err := w.failTimes(tn, n); err != nil {
			return err
		}
	}
	if err := w.closeInduction("wave-1"); err != nil {
		return err
	}
	stops, err := w.stopsOf("wave-1", len(tns))
	if err != nil {
		return err
	}
	if _, err := w.plan("wave-1", "dsp-1", stops); err != nil {
		return err
	}
	if !started {
		return nil
	}
	if _, err := usecases.NewDispatchWave(w.deps).Execute(w.ctx(), "wave-1"); err != nil {
		return err
	}
	_, err = usecases.NewStartRoute(w.deps).Execute(w.ctx(), "route-1")
	return err
}

func (w *world) failTimes(tn string, n int) error {
	for i := 0; i < n; i++ {
		h, err := w.deps.Histories.Get(w.ctx(), tn)
		if err != nil {
			return err
		}
		if _, err := h.RecordFailure(true, t0); err != nil {
			return err
		}
		if err := w.deps.Histories.Save(w.ctx(), h); err != nil {
			return err
		}
	}
	return nil
}

func registerAssertionSteps(sc *godog.ScenarioContext, w *world) {
	sc.Step(`^the request is refused with "([^"]*)"$`, w.assertRefused)
	sc.Step(`^no wave exists$`, w.assertNoWave)
	sc.Step(`^the package "([^"]*)" is inducted in wave "([^"]*)"$`, w.assertInducted)
	sc.Step(`^both packages "([^"]*)" and "([^"]*)" are inducted in wave "([^"]*)"$`, func(a, b, id string) error {
		if err := w.assertInducted(a, id); err != nil {
			return err
		}
		return w.assertInducted(b, id)
	})
	sc.Step(`^wave "([^"]*)" has (\d+) assigned stops? and (\d+) remaining$`, w.assertStops)
	sc.Step(`^wave "([^"]*)" holds (\d+) packages?$`, w.assertHolds)
	sc.Step(`^the package "([^"]*)" waits at the station$`, func(tn string) error { return w.assertPackage(tn, "AT_STATION") })
	sc.Step(`^the package "([^"]*)" is "([^"]*)"$`, w.assertPackage)
	sc.Step(`^route "([^"]*)" is "([^"]*)"$`, w.assertRoute)
	sc.Step(`^wave "([^"]*)" is "([^"]*)"$`, w.assertWave)
	sc.Step(`^the event "([^"]*)" was published for (?:wave|package) "([^"]*)"$`, w.assertEvent)
}

func (w *world) assertRefused(want string) error {
	if w.err == nil {
		return fmt.Errorf("the request succeeded, want it refused with %q", want)
	}
	if !strings.Contains(strings.ToLower(w.err.Error()), want) {
		return fmt.Errorf("the request was refused with %q, want %q", w.err, want)
	}
	return nil
}

func (w *world) assertNoWave() error {
	if n := w.store.Counts().Waves; n != 0 {
		return fmt.Errorf("%d waves exist", n)
	}
	return nil
}

func (w *world) assertStops(id string, assigned, remaining int) error {
	wv, err := w.loadWave(id)
	if err != nil {
		return err
	}
	if wv.AssignedStops() != assigned || wv.RemainingStops() != remaining {
		return fmt.Errorf("wave %s has %d assigned and %d remaining, want %d and %d", id, wv.AssignedStops(), wv.RemainingStops(), assigned, remaining)
	}
	return nil
}

func (w *world) assertHolds(id string, n int) error {
	wv, err := w.loadWave(id)
	if err != nil {
		return err
	}
	if len(wv.Inductions()) != n {
		return fmt.Errorf("wave %s holds %d packages, want %d", id, len(wv.Inductions()), n)
	}
	return nil
}

func (w *world) assertRoute(id, state string) error {
	r, err := w.deps.Routes.Get(w.ctx(), route.ID(id))
	if err != nil {
		return err
	}
	if string(r.State()) != state {
		return fmt.Errorf("route %s is %s, want %s", id, r.State(), state)
	}
	return nil
}

func (w *world) assertWave(id, state string) error {
	wv, err := w.loadWave(id)
	if err != nil {
		return err
	}
	if string(wv.State()) != state {
		return fmt.Errorf("wave %s is %s, want %s", id, wv.State(), state)
	}
	return nil
}

func (w *world) assertEvent(name, subject string) error {
	for _, ev := range w.store.Outbox().Events() {
		if ev.EventName() == name && ev.SubjectID() == subject {
			return nil
		}
	}
	return fmt.Errorf("no %s event for %s was published", name, subject)
}

func (w *world) assertInducted(tn, waveID string) error {
	wv, err := w.loadWave(waveID)
	if err != nil {
		return err
	}
	if _, ok := wv.Induction(tn); !ok {
		return fmt.Errorf("the package %s is not inducted in wave %s", tn, waveID)
	}
	return nil
}

func (w *world) assertPackage(tn, status string) error {
	h, err := w.deps.Histories.Get(w.ctx(), tn)
	if err != nil {
		return err
	}
	if string(h.Status()) != status {
		return fmt.Errorf("the package %s is %s, want %s", tn, h.Status(), status)
	}
	return nil
}
