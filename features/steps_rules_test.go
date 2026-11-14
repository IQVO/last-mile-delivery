package features_test

import (
	"fmt"
	"strconv"
	"time"

	"github.com/cucumber/godog"

	"github.com/claudioed/last-mile-delivery/internal/adapters/outbound/memory"
	"github.com/claudioed/last-mile-delivery/internal/application/usecases"
	"github.com/claudioed/last-mile-delivery/internal/application/views"
	"github.com/claudioed/last-mile-delivery/internal/domain/route"
)

// The steps of the documentation-derived rule features (waves_rules, route_rules,
// delivery_point_rules, carrier_area_rules, partner_rules and rest_rules). They
// add to the vocabulary of steps_test.go: the clock, arrivals at an instant, the
// node types of a load, service areas and labels, grouped routes, attempts with
// their detail and day, and the assertions on events and profiles.
func registerRuleSteps(sc *godog.ScenarioContext, w *world, h *httpWorld) {
	registerClockAndArrivalSteps(sc, w)
	registerAreaSteps(sc, w)
	registerRuleRouteSteps(sc, w)
	registerRuleAttemptSteps(sc, w)
	registerRuleAssertionSteps(sc, w)
	registerRuleEventSteps(sc, w)
	registerRuleHTTPSteps(sc, h)
}

const stationID = "node-ds-01"

// clock returns the scenario's settable clock.
func (w *world) clock() *memory.Clock { return w.deps.Clock.(*memory.Clock) }

// clockTime reads "HH:MM" or "HH:MM:SS" as an instant of the scenario day.
func clockTime(s string) (time.Time, error) {
	layout := "15:04"
	if len(s) == len("15:04:05") {
		layout = "15:04:05"
	}
	t, err := time.Parse(layout, s)
	if err != nil {
		return time.Time{}, err
	}
	return time.Date(t0.Year(), t0.Month(), t0.Day(), t.Hour(), t.Minute(), t.Second(), 0, time.UTC), nil
}

// arriveAt delivers one LoadArrived event for the given node and node type.
func (w *world) arriveAt(eventID, nodeType, node string, at time.Time, tns ...string) error {
	return usecases.NewInductOnArrival(w.deps).Execute(w.ctx(), usecases.LoadArrivedCommand{
		EventID: eventID, EventType: "LoadArrived", LoadID: "load-" + eventID, DepartureID: "dep-1", DestinationNodeID: node,
		NodeType: nodeType, TrackingNumbers: tns, ArrivedAt: at,
	})
}

func registerClockAndArrivalSteps(sc *godog.ScenarioContext, w *world) {
	sc.Step(`^the time is (\d\d:\d\d(?::\d\d)?)$`, func(s string) error {
		t, err := clockTime(s)
		if err != nil {
			return err
		}
		w.clock().Set(t)
		return nil
	})
	sc.Step(`^the package "([^"]*)" arrives at the station at (\d\d:\d\d(?::\d\d)?)$`, func(tn, s string) error {
		t, err := clockTime(s)
		if err != nil {
			return err
		}
		return w.arriveAt("ev-"+tn+"-"+s, views.DeliveryStation, stationID, t, tn)
	})
	sc.Step(`^the package "([^"]*)" arrives again in a later load$`, func(tn string) error {
		return w.arriveAt("ev-again-"+tn, views.DeliveryStation, stationID, t0, tn)
	})
	sc.Step(`^a load of the node type "([^"]*)" brings the package "([^"]*)" to the node "([^"]*)"$`, func(nodeType, tn, node string) error {
		return w.arriveAt("ev-"+tn, nodeType, node, t0, tn)
	})
	sc.Step(`^a load without a node type brings the package "([^"]*)" to the node "([^"]*)"$`, func(tn, node string) error {
		return w.arriveAt("ev-"+tn, "", node, t0, tn)
	})
	sc.Step(`^node-topology registers the node "([^"]*)" as a "([^"]*)"$`, func(node, nodeType string) error {
		return usecases.NewApplyNodeRegistered(w.deps).Execute(w.ctx(), usecases.NodeRegisteredCommand{
			EventID: "node-" + node, EventType: "NodeRegistered", NodeID: node, NodeType: nodeType, RegionID: "region-se", Timezone: "America/Sao_Paulo",
		})
	})
}

func registerAreaSteps(sc *godog.ScenarioContext, w *world) {
	area := func(prefix, mode string, version int64) error {
		return usecases.NewApplyServiceAreaAssigned(w.deps).Execute(w.ctx(), usecases.ServiceAreaAssignedCommand{
			EventID: fmt.Sprintf("area-%s-%s-%d", prefix, mode, version), EventType: "ServiceAreaAssigned", PostalPrefix: prefix,
			StationNodeID: stationID, RegionID: "region-se", AssignmentVersion: version, DeliveryMode: mode,
		})
	}
	sc.Step(`^node-topology assigns the postal prefix "([^"]*)" to the station in the delivery mode "([^"]*)" as version (\d+)$`, area)
	sc.Step(`^node-topology assigns the postal prefix "([^"]*)" to the station without a delivery mode as version (\d+)$`, func(prefix string, version int64) error {
		return area(prefix, "", version)
	})
	sc.Step(`^the label of "([^"]*)" says stop "([^"]*)" for the postal prefix "([^"]*)"$`, func(tn, key, prefix string) error {
		return usecases.NewApplyLabelIssued(w.deps).Execute(w.ctx(), usecases.LabelIssuedCommand{
			EventID: "label-" + tn, EventType: "LabelIssued", TrackingNumber: tn, StopKey: key, AssignmentID: "asg-" + tn, DestinationPostalPrefix: prefix,
		})
	})
}

// label tells the PackageStopView the stop of a package, as LabelIssued does.
func (w *world) label(tn, key string) error {
	return usecases.NewApplyLabelIssued(w.deps).Execute(w.ctx(), usecases.LabelIssuedCommand{
		EventID: "label-" + tn, EventType: "LabelIssued", TrackingNumber: tn, StopKey: key, AssignmentID: "asg-" + tn,
	})
}

func (w *world) dispatchAndStart(routeIDs ...string) error {
	if _, err := usecases.NewDispatchWave(w.deps).Execute(w.ctx(), "wave-1"); err != nil {
		return err
	}
	for _, id := range routeIDs {
		if _, err := usecases.NewStartRoute(w.deps).Execute(w.ctx(), route.ID(id)); err != nil {
			return err
		}
	}
	return nil
}

func registerRuleRouteSteps(sc *godog.ScenarioContext, w *world) {
	sc.Step(`^a partner "([^"]*)" for the station "([^"]*)" with a van of (\d+)$`, w.contractPartnerAt)
	sc.Step(`^the planner plans a route for the packages (.+)$`, w.planOneStopEach)
	sc.Step(`^the package "([^"]*)" already failed once$`, func(tn string) error { w.failures[tn] = 1; return nil })
	sc.Step(`^a started route with one stop of the packages (.+)$`, func(list string) error {
		return w.planStarted(quoted(list))
	})
	sc.Step(`^the packages (.+) of the stop "([^"]*)" each ride their own started route$`, func(list, key string) error {
		var groups [][]string
		for _, tn := range quoted(list) {
			if err := w.label(tn, key); err != nil {
				return err
			}
			groups = append(groups, []string{tn})
		}
		return w.planStarted(groups...)
	})
	sc.Step(`^the packages (.+) of the stop "([^"]*)" ride one started route and the package "([^"]*)" of the same stop rides another$`, func(list, key, other string) error {
		first := quoted(list)
		for _, tn := range append(append([]string{}, first...), other) {
			if err := w.label(tn, key); err != nil {
				return err
			}
		}
		return w.planStarted(first, []string{other})
	})
}

func (w *world) contractPartnerAt(id, station string, capacity int) error {
	p, err := usecases.NewContractPartner(w.deps).Execute(w.ctx(), usecases.ContractPartnerCommand{StationNodeID: station, VanCapacity: capacity})
	if err == nil && p.DSPID != id {
		return fmt.Errorf("the partner got the id %s, the scenario calls it %s", p.DSPID, id)
	}
	return err
}

// planOneStopEach plans one route with one stop per package, and keeps the error.
func (w *world) planOneStopEach(list string) error {
	var stops []route.Stop
	for i, tn := range quoted(list) {
		stops = append(stops, route.Stop{No: i + 1, TrackingNumbers: []string{tn}})
	}
	_, w.err = w.plan("wave-1", "dsp-1", stops)
	return nil
}

// planStarted inducts the packages, closes induction, plans one route per group
// of packages (route-1, route-2, ...) with the group as its one stop, dispatches
// wave-1 and starts every route.
func (w *world) planStarted(groups ...[]string) error {
	var all []string
	for _, g := range groups {
		all = append(all, g...)
	}
	if err := w.arrive("ev-route", all...); err != nil {
		return err
	}
	if err := w.closeInduction("wave-1"); err != nil {
		return err
	}
	var ids []string
	for i, g := range groups {
		if _, err := w.plan("wave-1", "dsp-1", []route.Stop{{No: 1, TrackingNumbers: g}}); err != nil {
			return err
		}
		ids = append(ids, "route-"+strconv.Itoa(i+1))
	}
	return w.dispatchAndStart(ids...)
}

// record runs RecordAttempt for route-N at the scenario's attempt instant
// (08:00 after t0, plus the given number of days) and keeps the error.
func (w *world) record(routeID, tn string, o route.Outcome, d route.Detail, reason, day string) error {
	days := 0
	if day != "" {
		days, _ = strconv.Atoi(day)
	}
	_, w.err = usecases.NewRecordAttempt(w.deps).Execute(w.ctx(), usecases.RecordAttemptCommand{
		RouteID: route.ID(routeID), TrackingNumber: tn, Outcome: o, Detail: d, FailureReason: reason,
		At: t0.Add(8*time.Hour + time.Duration(days)*24*time.Hour),
	})
	return nil
}

func registerRuleAttemptSteps(sc *godog.ScenarioContext, w *world) {
	sc.Step(`^on route "([^"]*)" the driver records "([^"]*)" as delivered(?: on day (\d+))?$`, func(id, tn, day string) error {
		return w.record(id, tn, route.OutcomeDelivered, "", "", day)
	})
	sc.Step(`^on route "([^"]*)" the driver records "([^"]*)" as delivered in a safe place(?: on day (\d+))?$`, func(id, tn, day string) error {
		return w.record(id, tn, route.OutcomeDelivered, route.DetailSafePlaceUsed, "", day)
	})
	sc.Step(`^on route "([^"]*)" the driver records "([^"]*)" as failed because "([^"]*)"(?: on day (\d+))?$`, func(id, tn, reason, day string) error {
		return w.record(id, tn, route.OutcomeFailed, "", reason, day)
	})
	sc.Step(`^on route "([^"]*)" the driver records "([^"]*)" as failed with the detail "([^"]*)" because "([^"]*)"(?: on day (\d+))?$`, func(id, tn, detail, reason, day string) error {
		return w.record(id, tn, route.OutcomeFailed, route.Detail(detail), reason, day)
	})
}
