package report_test

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/claudioed/last-mile-delivery/internal/analytics/report"
)

func TestAttemptOutcomesAreTheAsBuiltVocabulary(t *testing.T) {
	want := []string{"DELIVERED", "FAILED", "COLLECTED"}
	if got := report.AttemptOutcomes(); !reflect.DeepEqual(got, want) {
		t.Fatalf("AttemptOutcomes = %v, want %v", got, want)
	}
	for _, o := range want {
		if !report.IsAttemptOutcome(o) {
			t.Errorf("IsAttemptOutcome(%s) = false, want true", o)
		}
	}
	for _, o := range []string{"", "delivered", "RETURNED", "DEFERRED"} {
		if report.IsAttemptOutcome(o) {
			t.Errorf("IsAttemptOutcome(%q) = true, want false", o)
		}
	}
}

func TestUnknownStation(t *testing.T) {
	if report.UnknownStation() != "UNKNOWN" {
		t.Fatalf("UnknownStation = %q", report.UnknownStation())
	}
}

func TestIsCompletedRoute(t *testing.T) {
	cases := []struct {
		out, attempted int
		want           bool
	}{{3, 3, true}, {1, 1, true}, {3, 2, false}, {0, 0, false}, {2, 0, false}, {2, 3, false}}
	for _, c := range cases {
		if got := report.IsCompletedRoute(c.out, c.attempted); got != c.want {
			t.Errorf("IsCompletedRoute(%d, %d) = %v, want %v", c.out, c.attempted, got, c.want)
		}
	}
}

var at = time.Date(2026, 11, 3, 10, 0, 0, 0, time.UTC)

// valid returns one complete fact of every projected kind.
func valid() []report.Event {
	base := report.Event{EventID: "e1", At: at, OccurredAt: at}
	with := func(f func(e *report.Event)) report.Event {
		e := base
		f(&e)
		return e
	}
	return []report.Event{
		with(func(e *report.Event) {
			e.Kind, e.WaveID, e.StationNodeID, e.ServiceDate = report.KindWaveScheduled, "w1", "st1", at.Truncate(24*time.Hour)
		}),
		with(func(e *report.Event) { e.Kind, e.WaveID, e.AssignedStops = report.KindWaveCapacityChanged, "w1", 1 }),
		with(func(e *report.Event) { e.Kind, e.WaveID = report.KindWaveCancelled, "w1" }),
		with(func(e *report.Event) { e.Kind, e.WaveID = report.KindWaveCostDeclared, "w1" }),
		with(func(e *report.Event) {
			e.Kind, e.RouteID, e.WaveID, e.StopCount = report.KindRoutePlanned, "r1", "w1", 1
		}),
		with(func(e *report.Event) {
			e.Kind, e.RouteID, e.TrackingNumber, e.StationNodeID = report.KindOutForDelivery, "r1", "T1", "st1"
		}),
		with(func(e *report.Event) {
			e.Kind, e.RouteID, e.TrackingNumber, e.Outcome = report.KindDeliveryAttempted, "r1", "T1", report.OutcomeFailed
		}),
		with(func(e *report.Event) { e.Kind, e.TrackingNumber = report.KindReturnInitiated, "T1" }),
		with(func(e *report.Event) {
			e.Kind, e.RouteID, e.StopNo, e.TrackingNumbers = report.KindStopEtaWindowAssigned, "r1", 1, []string{"T1", "T2"}
			e.EtaStart, e.EtaEnd = at, at.Add(2*time.Hour)
		}),
	}
}

func TestValidateAcceptsCompleteFacts(t *testing.T) {
	for _, e := range valid() {
		if err := e.Validate(); err != nil {
			t.Errorf("%s: %v", e.Kind, err)
		}
	}
}

// missingCase breaks one field of the valid fact at index base.
type missingCase struct {
	name   string
	base   int
	mutate func(*report.Event)
}

var missingCases = []missingCase{
	{"id", 0, func(e *report.Event) { e.EventID = "" }},
	{"time", 0, func(e *report.Event) { e.At = time.Time{} }},
	{"the business instant", 0, func(e *report.Event) { e.OccurredAt = time.Time{} }},
	{"waveId", 0, func(e *report.Event) { e.WaveID = "" }},
	{"stationNodeId", 0, func(e *report.Event) { e.StationNodeID = "" }},
	{"serviceDate", 0, func(e *report.Event) { e.ServiceDate = time.Time{} }},
	{"a non-negative stopCapacity", 0, func(e *report.Event) { e.StopCapacity = -1 }},
	{"a positive assignedStops", 1, func(e *report.Event) { e.AssignedStops = 0 }},
	{"waveId", 2, func(e *report.Event) { e.WaveID = "" }},
	{"a non-negative costPerStopCents", 3, func(e *report.Event) { e.CostPerStopCents = -1 }},
	{"routeId", 4, func(e *report.Event) { e.RouteID = "" }},
	{"waveId", 4, func(e *report.Event) { e.WaveID = "" }},
	{"a positive stopCount", 4, func(e *report.Event) { e.StopCount = 0 }},
	{"trackingNumber", 5, func(e *report.Event) { e.TrackingNumber = "" }},
	{"stationNodeId", 5, func(e *report.Event) { e.StationNodeID = "" }},
	{"routeId", 6, func(e *report.Event) { e.RouteID = "" }},
	{"an outcome of the vocabulary", 6, func(e *report.Event) { e.Outcome = "RETURNED" }},
	{"trackingNumber", 7, func(e *report.Event) { e.TrackingNumber = "" }},
	{"routeId", 8, func(e *report.Event) { e.RouteID = "" }},
	{"a positive stopNo", 8, func(e *report.Event) { e.StopNo = 0 }},
	{"at least one trackingNumber, none blank", 8, func(e *report.Event) { e.TrackingNumbers = nil }},
	{"at least one trackingNumber, none blank", 8, func(e *report.Event) { e.TrackingNumbers = []string{"T1", ""} }},
	{"etaStart before etaEnd", 8, func(e *report.Event) { e.EtaEnd = e.EtaStart }},
	{"etaStart before etaEnd", 8, func(e *report.Event) { e.EtaStart = time.Time{}; e.EtaEnd = time.Time{} }},
	{"etaStart before etaEnd", 8, func(e *report.Event) { e.EtaEnd = e.EtaStart.Add(-time.Minute) }},
	{"a known kind", 0, func(e *report.Event) { e.Kind = "PackageInductionDeferred" }},
}

func TestValidateNamesTheFirstMissingField(t *testing.T) {
	for _, c := range missingCases {
		e := valid()[c.base]
		c.mutate(&e)
		err := e.Validate()
		if !errors.Is(err, report.ErrInvalidEvent) {
			t.Errorf("%s (%s): err = %v, want ErrInvalidEvent", c.name, e.Kind, err)
			continue
		}
		if !strings.HasSuffix(err.Error(), ": "+c.name+" is required") {
			t.Errorf("%s: err = %q does not name the field", c.name, err)
		}
	}
}

func TestValidateBoundaries(t *testing.T) {
	zeroCapacity, zeroCost := valid()[0], valid()[3]
	zeroCapacity.StopCapacity, zeroCost.CostPerStopCents = 0, 0
	for _, e := range []report.Event{zeroCapacity, zeroCost} {
		if err := e.Validate(); err != nil {
			t.Errorf("%s: zero is a legal value: %v", e.Kind, err)
		}
	}
}
