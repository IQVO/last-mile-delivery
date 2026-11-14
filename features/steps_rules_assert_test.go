package features_test

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/cucumber/godog"

	"github.com/claudioed/last-mile-delivery/internal/application/ports"
	"github.com/claudioed/last-mile-delivery/internal/domain/deliverypoint"
	"github.com/claudioed/last-mile-delivery/internal/domain/route"
	"github.com/claudioed/last-mile-delivery/internal/domain/shared"
	"github.com/claudioed/last-mile-delivery/internal/domain/wave"
)

// events returns the published events of one name, in publication order.
func (w *world) events(name string) []shared.Event {
	var out []shared.Event
	for _, ev := range w.store.Outbox().Events() {
		if ev.EventName() == name {
			out = append(out, ev)
		}
	}
	return out
}

func registerRuleAssertionSteps(sc *godog.ScenarioContext, w *world) {
	sc.Step(`^the request is accepted$`, w.assertAccepted)
	sc.Step(`^no event was published$`, w.assertNoEvent)
	sc.Step(`^the package "([^"]*)" is unknown to the station$`, w.assertUnknownPackage)
	sc.Step(`^the station view holds no node "([^"]*)"$`, w.assertNoStationNode)
	sc.Step(`^the package "([^"]*)" failed (\d+) times?$`, w.assertFailedAttempts)
	sc.Step(`^the package "([^"]*)" is not inducted in wave "([^"]*)"$`, w.assertNotInducted)
	sc.Step(`^the arrival of "([^"]*)" is (recorded|not recorded) in the load "([^"]*)"$`, w.assertArrival)
	registerPointAssertionSteps(sc, w)
}

func (w *world) assertAccepted() error {
	if w.err != nil {
		return fmt.Errorf("the request was refused: %w", w.err)
	}
	return nil
}

func (w *world) assertNoEvent() error {
	if n := len(w.store.Outbox().Events()); n != 0 {
		return fmt.Errorf("%d events were published, want none", n)
	}
	return nil
}

func (w *world) assertUnknownPackage(tn string) error {
	if _, err := w.deps.Histories.Get(w.ctx(), tn); !errors.Is(err, ports.ErrNotFound) {
		return fmt.Errorf("the package %s has a history (lookup: %w), want none", tn, err)
	}
	return nil
}

func (w *world) assertNoStationNode(node string) error {
	if st, err := w.deps.Stations.Get(w.ctx(), node); !errors.Is(err, ports.ErrNotFound) {
		return fmt.Errorf("the station view holds %s as a %s (lookup: %w), want no such node", node, st.NodeType, err)
	}
	return nil
}

func (w *world) assertFailedAttempts(tn string, n int) error {
	h, err := w.deps.Histories.Get(w.ctx(), tn)
	if err != nil {
		return err
	}
	if h.FailedAttempts() != n {
		return fmt.Errorf("the package %s failed %d times, want %d", tn, h.FailedAttempts(), n)
	}
	return nil
}

func (w *world) assertNotInducted(tn, id string) error {
	if err := w.assertInducted(tn, id); err == nil {
		return fmt.Errorf("the package %s is inducted in wave %s, want it out", tn, id)
	}
	return nil
}

func (w *world) assertArrival(tn, which, load string) error {
	_, err := w.deps.Inbound.Get(w.ctx(), load, tn)
	if recorded := err == nil; recorded != (which == "recorded") {
		return fmt.Errorf("the arrival of %s in %s is %v, want it %s", tn, load, recorded, which)
	}
	return nil
}

func registerPointAssertionSteps(sc *godog.ScenarioContext, w *world) {
	sc.Step(`^the delivery point "([^"]*)" has (\d+) open strikes?$`, w.assertOpenStrikes)
	sc.Step(`^the delivery point "([^"]*)" is (flagged|not flagged)$`, w.assertFlagged)
	sc.Step(`^the delivery point "([^"]*)" was flagged on day (\d+)$`, w.assertFlaggedOn)
	sc.Step(`^the delivery point "([^"]*)" tallied (\d+) attempts: (\d+) delivered \((\d+) in a safe place\), (\d+) not home, (\d+) access problem, (\d+) other$`, w.assertTally)
	sc.Step(`^no delivery point profile exists$`, func() error {
		if n := w.store.Counts().DeliveryPoints; n != 0 {
			return fmt.Errorf("%d delivery point profiles exist, want none", n)
		}
		return nil
	})
}

func (w *world) point(key string) (*deliverypoint.Profile, error) {
	return w.deps.DeliveryPoints.Get(w.ctx(), key)
}

func (w *world) assertOpenStrikes(key string, n int) error {
	p, err := w.point(key)
	if err != nil {
		return err
	}
	if p.OpenStrikes() != n {
		return fmt.Errorf("the point %s has %d open strikes, want %d", key, p.OpenStrikes(), n)
	}
	return nil
}

func (w *world) assertFlagged(key, which string) error {
	p, err := w.point(key)
	if err != nil {
		return err
	}
	if p.Flagged() != (which == "flagged") {
		return fmt.Errorf("the point %s flagged = %v, want %s", key, p.Flagged(), which)
	}
	return nil
}

func (w *world) assertFlaggedOn(key string, day int) error {
	p, err := w.point(key)
	if err != nil {
		return err
	}
	if want := t0.Add(8*time.Hour + time.Duration(day)*24*time.Hour); !p.FlaggedAt().Equal(want) {
		return fmt.Errorf("the point %s was flagged at %s, want %s", key, p.FlaggedAt(), want)
	}
	return nil
}

func (w *world) assertTally(key string, attempts, delivered, safe, notHome, access, other int) error {
	p, err := w.point(key)
	if err != nil {
		return err
	}
	got := []int{p.Attempts(), p.Delivered(), p.SafePlaceUsed(), p.FailedNotHome(), p.FailedAccess(), p.FailedOther()}
	want := []int{attempts, delivered, safe, notHome, access, other}
	if !reflect.DeepEqual(got, want) {
		return fmt.Errorf("the point %s tallied %v (attempts, delivered, safe, not home, access, other), want %v", key, got, want)
	}
	return nil
}

func registerRuleEventSteps(sc *godog.ScenarioContext, w *world) {
	sc.Step(`^the latest "DeliveryWaveCapacityChanged" event for wave "([^"]*)" reports (\d+) assigned and (\d+) remaining stops$`, w.assertLatestCapacity)
	sc.Step(`^the "DeliveryAttempted" event for route "([^"]*)" and package "([^"]*)" carries the outcome "([^"]*)", the detail "([^"]*)" and the reason "([^"]*)"$`, w.assertAttemptEvent)
	sc.Step(`^the events of route "([^"]*)" after RoutePlanned are, in order:$`, w.assertRouteEventOrder)
	sc.Step(`^the latest "DeliveryPointFlagged" event for the stop "([^"]*)" flags (\d+) failures for the packages "([^"]*)" because (NOT_HOME|ACCESS_PROBLEM)$`, w.assertLatestFlag)
	sc.Step(`^route "([^"]*)" gives its stop (\d+) the ETA window (\d\d:\d\d) to (\d\d:\d\d)$`, w.assertEtaWindow)
	sc.Step(`^the ETA window of stop (\d+) of route "([^"]*)" covers the packages "([^"]*)" of the stop "([^"]*)"$`, w.assertEtaCovers)
}

func (w *world) assertLatestCapacity(id string, assigned, remaining int) error {
	var last *wave.DeliveryWaveCapacityChanged
	for _, ev := range w.events("DeliveryWaveCapacityChanged") {
		if e := ev.(wave.DeliveryWaveCapacityChanged); string(e.WaveID) == id {
			last = &e
		}
	}
	if last == nil {
		return fmt.Errorf("no DeliveryWaveCapacityChanged event for %s was published", id)
	}
	if last.AssignedStops != assigned || last.RemainingStops != remaining {
		return fmt.Errorf("the latest event reports %d assigned and %d remaining, want %d and %d", last.AssignedStops, last.RemainingStops, assigned, remaining)
	}
	return nil
}

func (w *world) assertAttemptEvent(id, tn, outcome, detail, reason string) error {
	for _, ev := range w.events("DeliveryAttempted") {
		e := ev.(route.DeliveryAttempted)
		if string(e.RouteID) != id || e.TrackingNumber != tn {
			continue
		}
		if string(e.Outcome) != outcome || string(e.Detail) != detail || e.FailureReason != reason {
			return fmt.Errorf("the event carries %s/%q/%q, want %s/%q/%q", e.Outcome, e.Detail, e.FailureReason, outcome, detail, reason)
		}
		return nil
	}
	return fmt.Errorf("no DeliveryAttempted event for %s on %s", tn, id)
}

func (w *world) assertRouteEventOrder(id string, table *godog.Table) error {
	var got, want []string
	for _, ev := range w.store.Outbox().Events() {
		if ev.SubjectID() == id && ev.EventName() != "RoutePlanned" {
			got = append(got, ev.EventName())
		}
	}
	for _, row := range table.Rows {
		want = append(want, row.Cells[0].Value)
	}
	if !reflect.DeepEqual(got, want) {
		return fmt.Errorf("route %s raised %v after RoutePlanned, want %v", id, got, want)
	}
	return nil
}

func (w *world) assertLatestFlag(key string, failures int, tns, reason string) error {
	var last *deliverypoint.DeliveryPointFlagged
	for _, ev := range w.events("DeliveryPointFlagged") {
		if e := ev.(deliverypoint.DeliveryPointFlagged); e.StopKey == key {
			last = &e
		}
	}
	if last == nil {
		return fmt.Errorf("no DeliveryPointFlagged event for the stop %s was published", key)
	}
	if got := strings.Join(last.TrackingNumbers, ","); last.Failures != failures || got != tns || string(last.Reason) != reason {
		return fmt.Errorf("the latest flag says %d failures, packages %q, reason %s; want %d, %q, %s", last.Failures, got, last.Reason, failures, tns, reason)
	}
	return nil
}

// etaEvent finds the StopEtaWindowAssigned event of one stop of one route.
func (w *world) etaEvent(id string, stopNo int) (route.StopEtaWindowAssigned, error) {
	for _, ev := range w.events("StopEtaWindowAssigned") {
		if e := ev.(route.StopEtaWindowAssigned); string(e.RouteID) == id && e.StopNo == stopNo {
			return e, nil
		}
	}
	return route.StopEtaWindowAssigned{}, fmt.Errorf("no StopEtaWindowAssigned event for stop %d of route %s", stopNo, id)
}

func (w *world) assertEtaWindow(id string, stopNo int, from, to string) error {
	start, err := clockTime(from)
	if err != nil {
		return err
	}
	end, err := clockTime(to)
	if err != nil {
		return err
	}
	e, err := w.etaEvent(id, stopNo)
	if err != nil {
		return err
	}
	if !e.EtaStart.Equal(start) || !e.EtaEnd.Equal(end) {
		return fmt.Errorf("stop %d has the window %s to %s, want %s to %s", stopNo, e.EtaStart.Format(time.TimeOnly), e.EtaEnd.Format(time.TimeOnly), from, to)
	}
	return nil
}

func (w *world) assertEtaCovers(stopNo int, id, tns, key string) error {
	e, err := w.etaEvent(id, stopNo)
	if err != nil {
		return err
	}
	if got := strings.Join(e.TrackingNumbers, ","); got != tns || e.StopKey != key {
		return fmt.Errorf("the window covers %q of the stop %q, want %q of %q", got, e.StopKey, tns, key)
	}
	return nil
}
