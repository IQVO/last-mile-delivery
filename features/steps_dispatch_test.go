package features_test

import (
	"fmt"
	"strconv"

	"github.com/cucumber/godog"

	"github.com/claudioed/last-mile-delivery/internal/application/usecases"
	"github.com/claudioed/last-mile-delivery/internal/domain/history"
	"github.com/claudioed/last-mile-delivery/internal/domain/route"
	"github.com/claudioed/last-mile-delivery/internal/domain/shared"
	"github.com/claudioed/last-mile-delivery/internal/domain/wave"
)

// Steps of the dispatch operation and of the published events (name, subject
// and key fields), shared by the wave, route and dispatch features.
func registerDispatchSteps(sc *godog.ScenarioContext, w *world) {
	sc.Step(`^the planner dispatches wave "([^"]*)"$`, func(id string) error {
		_, w.err = usecases.NewDispatchWave(w.deps).Execute(w.ctx(), waveID(id))
		return nil
	})
	sc.Step(`^exactly (\d+) "([^"]*)" events? (?:were|was) published$`, w.assertEventCount)
	sc.Step(`^the "([^"]*)" event for (?:wave|package|route) "([^"]*)" has "([^"]*)" "([^"]*)"$`, w.assertEventField)
}

func (w *world) assertEventCount(n int, name string) error {
	if got := w.countEvents(name); got != n {
		return fmt.Errorf("%d %s events were published, want %d", got, name, n)
	}
	return nil
}

// assertEventField passes when SOME event of that name and subject carries the
// field value: per-package events of one route share the route as subject.
func (w *world) assertEventField(name, subject, field, want string) error {
	for _, ev := range w.store.Outbox().Events() {
		if ev.EventName() != name || ev.SubjectID() != subject {
			continue
		}
		if got, ok := eventField(ev, field); ok && got == want {
			return nil
		}
	}
	return fmt.Errorf("no %s event for %s carries %s %q", name, subject, field, want)
}

func (w *world) countEvents(name string) int {
	n := 0
	for _, ev := range w.store.Outbox().Events() {
		if ev.EventName() == name {
			n++
		}
	}
	return n
}

// eventField reads one key field of a concrete domain event, so a scenario can
// assert WHAT was published, not only that something was.
func eventField(ev shared.Event, field string) (string, bool) {
	switch e := ev.(type) {
	case wave.DeliveryWaveScheduled:
		return waveScheduledField(e, field)
	case wave.DeliveryWaveCancelled:
		return waveCancelledField(e, field)
	case route.RoutePlanned:
		return routePlannedField(e, field)
	case route.PackageOutForDelivery:
		return outForDeliveryField(e, field)
	case route.DeliveryAttempted:
		return attemptedField(e, field)
	case history.PackageInductionDeferred:
		return deferredField(e, field)
	}
	return "", false
}

func waveScheduledField(e wave.DeliveryWaveScheduled, field string) (string, bool) {
	switch field {
	case "stationNodeId":
		return e.StationNodeID, true
	case "stopCapacity":
		return strconv.Itoa(e.StopCapacity), true
	}
	return "", false
}

func waveCancelledField(e wave.DeliveryWaveCancelled, field string) (string, bool) {
	if field == "reason" {
		return e.Reason, true
	}
	return "", false
}

func routePlannedField(e route.RoutePlanned, field string) (string, bool) {
	switch field {
	case "dspId":
		return string(e.DSPID), true
	case "stopCount":
		return strconv.Itoa(e.StopCount), true
	}
	return "", false
}

func outForDeliveryField(e route.PackageOutForDelivery, field string) (string, bool) {
	switch field {
	case "routeId":
		return string(e.RouteID), true
	case "trackingNumber":
		return e.TrackingNumber, true
	}
	return "", false
}

func attemptedField(e route.DeliveryAttempted, field string) (string, bool) {
	switch field {
	case "outcome":
		return string(e.Outcome), true
	case "trackingNumber":
		return e.TrackingNumber, true
	}
	return "", false
}

func deferredField(e history.PackageInductionDeferred, field string) (string, bool) {
	if field == "assignmentId" {
		return e.AssignmentID, true
	}
	return "", false
}
