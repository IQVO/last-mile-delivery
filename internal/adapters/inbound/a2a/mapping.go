package a2a

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/claudioed/last-mile-delivery/internal/application/views"
)

// problem is the RFC 7807-shaped rejection an agent client receives instead of
// a result: domain rejections and request-shape failures are business
// answers, not transport errors, so they travel in the message envelope. Type
// is the problem SLUG, exactly like the REST adapter's RFC 9457 "type"
// (internal/adapters/inbound/http/errors.go), so a delegating agent sees the
// identifiers a REST caller would for the outcomes both surfaces share.
type problem struct {
	Type   string `json:"type"`
	Title  string `json:"title"`
	Detail string `json:"detail"`
	Status int    `json:"status"`
}

// summary renders the one-line form used for the reply's text part.
func (p *problem) summary() string {
	return fmt.Sprintf("%s (%d): %s", p.Title, p.Status, p.Detail)
}

// The request-shape problems exist on this surface only (REST has no skill
// envelope): a malformed delegation is 400, like the reference A2A agents.
func problemMalformedRequest(detail string) *problem {
	return &problem{Type: "malformed-skill-request", Title: "Malformed skill request", Detail: detail, Status: http.StatusBadRequest}
}

func problemUnknownSkill(skill string) *problem {
	return &problem{Type: "unknown-skill", Title: "Unknown skill", Detail: fmt.Sprintf("skill %q is not offered by this agent; the Agent Card lists the supported skills", skill), Status: http.StatusBadRequest}
}

func problemInvalidArgument(err error) *problem {
	return &problem{Type: "invalid-argument", Title: "Invalid skill argument", Detail: err.Error(), Status: http.StatusBadRequest}
}

// envelope is the structured payload of every skill answer: exactly one of
// Data (a skill DTO) or Problem (why there is no result), plus the skill id
// the answer belongs to.
type envelope struct {
	Skill   string   `json:"skill"`
	Data    skillDTO `json:"data,omitempty"`
	Problem *problem `json:"problem,omitempty"`
}

// skillDTO is the union of the skill result DTOs. A2A parts are
// machine-readable JSON; each skill fills its own field, keeping the envelope
// flat and versioned as one contract.
type skillDTO struct {
	Waves   *wavesDTO `json:"waves,omitempty"`
	Route   *routeDTO `json:"route,omitempty"`
	summary string
}

// Summary is the human-readable one-liner for the reply's text part.
func (s skillDTO) Summary() string { return s.summary }

// wavesDTO is the list_waves result: the station and date that were asked
// for, and the waves in it (ordered by induct cutoff, an empty list when the
// station runs none).
type wavesDTO struct {
	StationNodeID string    `json:"stationNodeId"`
	ServiceDate   string    `json:"serviceDate"`
	Items         []waveDTO `json:"items"`
}

// waveDTO is the message-boundary representation of a delivery wave: the same
// fields the REST body and the MCP tool carry.
type waveDTO struct {
	WaveID         string `json:"waveId"`
	StationNodeID  string `json:"stationNodeId"`
	ServiceDate    string `json:"serviceDate"`
	InductCutoffAt string `json:"inductCutoffAt"`
	WindowStart    string `json:"windowStart"`
	WindowEnd      string `json:"windowEnd"`
	StopCapacity   int    `json:"stopCapacity"`
	AssignedStops  int    `json:"assignedStops"`
	RemainingStops int    `json:"remainingStops"`
	State          string `json:"state"`
	CancelReason   string `json:"cancelReason,omitempty"`
}

type stopDTO struct {
	StopNo          int      `json:"stopNo"`
	TrackingNumbers []string `json:"trackingNumbers"`
	Outcome         string   `json:"outcome,omitempty"`
}

type attemptDTO struct {
	RouteID        string `json:"routeId"`
	TrackingNumber string `json:"trackingNumber"`
	Outcome        string `json:"outcome"`
	FailureReason  string `json:"failureReason,omitempty"`
	At             string `json:"at"`
}

// routeDTO is the message-boundary representation of a route with its stops
// and attempts: the same fields the REST body and the MCP tool carry.
type routeDTO struct {
	RouteID       string       `json:"routeId"`
	WaveID        string       `json:"waveId"`
	DSPID         string       `json:"dspId"`
	StationNodeID string       `json:"stationNodeId"`
	State         string       `json:"state"`
	StopCount     int          `json:"stopCount"`
	Stops         []stopDTO    `json:"stops"`
	Attempts      []attemptDTO `json:"attempts"`
}

// instant formats an instant as RFC 3339 UTC; the zero time is "" (omitted).
func instant(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339Nano)
}

func toWaveDTO(w views.Wave) waveDTO {
	return waveDTO{
		WaveID: w.WaveID, StationNodeID: w.StationNodeID, ServiceDate: w.ServiceDate,
		InductCutoffAt: instant(w.InductCutoffAt), WindowStart: instant(w.WindowStart), WindowEnd: instant(w.WindowEnd),
		StopCapacity: w.StopCapacity, AssignedStops: w.AssignedStops, RemainingStops: w.RemainingStops,
		State: w.State, CancelReason: w.CancelReason,
	}
}

func toWavesDTO(station, date string, waves []views.Wave) wavesDTO {
	items := make([]waveDTO, 0, len(waves))
	for _, w := range waves {
		items = append(items, toWaveDTO(w))
	}
	return wavesDTO{StationNodeID: station, ServiceDate: date, Items: items}
}

func toRouteDTO(r views.Route) routeDTO {
	out := routeDTO{
		RouteID: r.RouteID, WaveID: r.WaveID, DSPID: r.DSPID, StationNodeID: r.StationNodeID,
		State: r.State, StopCount: r.StopCount, Stops: make([]stopDTO, 0, len(r.Stops)), Attempts: make([]attemptDTO, 0, len(r.Attempts)),
	}
	for _, s := range r.Stops {
		out.Stops = append(out.Stops, stopDTO{StopNo: s.StopNo, TrackingNumbers: nonNil(s.TrackingNumbers), Outcome: s.Outcome})
	}
	for _, a := range r.Attempts {
		out.Attempts = append(out.Attempts, attemptDTO{
			RouteID: a.RouteID, TrackingNumber: a.TrackingNumber, Outcome: a.Outcome,
			FailureReason: a.FailureReason, At: instant(a.At),
		})
	}
	return out
}

// nonNil keeps every JSON list an array, never null.
func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// summarizeWaves is the one-line text form of the day's waves.
func summarizeWaves(list wavesDTO) string {
	parts := make([]string, 0, len(list.Items))
	for _, w := range list.Items {
		parts = append(parts, fmt.Sprintf("%s %s (%d/%d stops assigned)", w.WaveID, w.State, w.AssignedStops, w.StopCapacity))
	}
	if len(parts) == 0 {
		return fmt.Sprintf("0 waves at %s on %s", list.StationNodeID, list.ServiceDate)
	}
	return fmt.Sprintf("%d waves at %s on %s: %s", len(parts), list.StationNodeID, list.ServiceDate, strings.Join(parts, ", "))
}

// summarizeRoute is the one-line text form of one route.
func summarizeRoute(r routeDTO) string {
	return fmt.Sprintf("%s: %s, partner %s, wave %s, %d stops, %d attempts recorded",
		r.RouteID, strings.ToLower(r.State), r.DSPID, r.WaveID, r.StopCount, len(r.Attempts))
}
