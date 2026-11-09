package kafka

import (
	"fmt"
	"time"

	ce "github.com/cloudevents/sdk-go/v2/event"

	"github.com/claudioed/last-mile-delivery/internal/adapters/kafka/cloudevents"
	"github.com/claudioed/last-mile-delivery/internal/analytics/report"
)

// The CloudEvents entity segments of this context's three event-raising
// aggregates.
const (
	entityWave    = "wave"
	entityRoute   = "route"
	entityHistory = "packageattempthistory"
)

// serviceDateLayout is the wire form of a wave's serviceDate.
const serviceDateLayout = "2006-01-02"

// projectedKinds maps the FULL CloudEvents type of each projected event
// (ADR 0012) to its report.Kind.
func projectedKinds() map[string]report.Kind {
	return map[string]report.Kind{
		cloudevents.Type(entityWave, string(report.KindWaveScheduled)):       report.KindWaveScheduled,
		cloudevents.Type(entityWave, string(report.KindWaveCapacityChanged)): report.KindWaveCapacityChanged,
		cloudevents.Type(entityWave, string(report.KindWaveCancelled)):       report.KindWaveCancelled,
		cloudevents.Type(entityWave, string(report.KindWaveCostDeclared)):    report.KindWaveCostDeclared,
		cloudevents.Type(entityRoute, string(report.KindRoutePlanned)):       report.KindRoutePlanned,
		cloudevents.Type(entityRoute, string(report.KindOutForDelivery)):     report.KindOutForDelivery,
		cloudevents.Type(entityRoute, string(report.KindDeliveryAttempted)):  report.KindDeliveryAttempted,
		cloudevents.Type(entityHistory, string(report.KindReturnInitiated)):  report.KindReturnInitiated,
	}
}

// acknowledgedTypes are this context's other published types: committed past
// without a warning, because no report needs them (ADR 0012).
func acknowledgedTypes() map[string]bool {
	return map[string]bool{
		cloudevents.Type(entityHistory, "PackageInductionDeferred"): true,
	}
}

// analyticsData is the union of the payload fields the projection reads,
// hand-mirrored from apis/asyncapi.yaml (never imported from the encoder).
// Unknown fields are ignored; an absent timestamp stays zero and an absent
// count stays nil (so a legal 0 is told apart from a missing field).
type analyticsData struct {
	WaveID           string    `json:"waveId"`
	StationNodeID    string    `json:"stationNodeId"`
	ServiceDate      string    `json:"serviceDate"`
	StopCapacity     *int      `json:"stopCapacity"`
	AssignedStops    *int      `json:"assignedStops"`
	CostPerStopCents *int64    `json:"costPerStopCents"`
	DeclaredAt       time.Time `json:"declaredAt"`
	RouteID          string    `json:"routeId"`
	DSPID            string    `json:"dspId"`
	StopCount        *int      `json:"stopCount"`
	TrackingNumber   string    `json:"trackingNumber"`
	Outcome          string    `json:"outcome"`
	At               time.Time `json:"at"`
}

// toEvent decodes and validates a CloudEvent of a projected kind. Every error
// is deterministic: the same bytes can never pass.
func toEvent(kind report.Kind, evt ce.Event) (report.Event, error) {
	var d analyticsData
	if err := evt.DataAs(&d); err != nil {
		return report.Event{}, fmt.Errorf("decode %s %s data: %w", kind, evt.ID(), err)
	}
	if aggregate := d.aggregateID(kind); aggregate == "" || aggregate != evt.Subject() {
		return report.Event{}, fmt.Errorf("%s %s: the aggregate id %q does not match the subject %q", kind, evt.ID(), aggregate, evt.Subject())
	}
	if absent := d.absentCount(kind); absent != "" {
		return report.Event{}, fmt.Errorf("%w: %s %s: %s is required", report.ErrInvalidEvent, kind, evt.ID(), absent)
	}
	serviceDate, err := d.serviceDate(kind)
	if err != nil {
		return report.Event{}, fmt.Errorf("%s %s: %w", kind, evt.ID(), err)
	}
	e := report.Event{
		Kind: kind, EventID: evt.ID(), At: evt.Time().UTC(), OccurredAt: d.instant(kind, evt.Time()).UTC(),
		WaveID: d.WaveID, StationNodeID: d.StationNodeID, ServiceDate: serviceDate,
		StopCapacity: deref(d.StopCapacity), AssignedStops: deref(d.AssignedStops), CostPerStopCents: deref(d.CostPerStopCents),
		RouteID: d.RouteID, DSPID: d.DSPID, StopCount: deref(d.StopCount), Outcome: d.Outcome, TrackingNumber: d.TrackingNumber,
	}
	if err := e.Validate(); err != nil {
		return report.Event{}, err
	}
	return e, nil
}

// aggregateID is the payload's aggregate id, which must equal the subject:
// waveId for the wave events, routeId for the route events, trackingNumber for
// the history event.
func (d analyticsData) aggregateID(kind report.Kind) string {
	switch kind {
	case report.KindWaveScheduled, report.KindWaveCapacityChanged, report.KindWaveCancelled, report.KindWaveCostDeclared:
		return d.WaveID
	case report.KindReturnInitiated:
		return d.TrackingNumber
	default:
		return d.RouteID
	}
}

// serviceDate parses the wave's serviceDate (only DeliveryWaveScheduled has
// one); an absent date stays zero for Validate to name.
func (d analyticsData) serviceDate(kind report.Kind) (time.Time, error) {
	if kind != report.KindWaveScheduled || d.ServiceDate == "" {
		return time.Time{}, nil
	}
	t, err := time.Parse(serviceDateLayout, d.ServiceDate)
	if err != nil {
		return time.Time{}, fmt.Errorf("serviceDate %q is not a YYYY-MM-DD date", d.ServiceDate)
	}
	return t, nil
}

// instant is the business instant of a fact: the attempt's and the
// out-for-delivery's `at`, the cost's `declaredAt`, else the CloudEvents time
// (the other events carry no instant of their own).
func (d analyticsData) instant(kind report.Kind, eventTime time.Time) time.Time {
	switch kind {
	case report.KindOutForDelivery, report.KindDeliveryAttempted:
		return d.At
	case report.KindWaveCostDeclared:
		return d.DeclaredAt
	default:
		return eventTime
	}
}

// absentCount names the count field the Kind requires when the payload omits
// it: a legal 0 (stopCapacity, costPerStopCents) must not stand in for a
// missing field.
func (d analyticsData) absentCount(kind report.Kind) string {
	required := map[report.Kind]struct {
		name    string
		present bool
	}{
		report.KindWaveScheduled:       {"stopCapacity", d.StopCapacity != nil},
		report.KindWaveCapacityChanged: {"assignedStops", d.AssignedStops != nil},
		report.KindWaveCostDeclared:    {"costPerStopCents", d.CostPerStopCents != nil},
		report.KindRoutePlanned:        {"stopCount", d.StopCount != nil},
	}
	if r, ok := required[kind]; ok && !r.present {
		return r.name
	}
	return ""
}

// deref is the decoded value, or zero when the field was absent.
func deref[T int | int64](v *T) T {
	if v == nil {
		return 0
	}
	return *v
}
