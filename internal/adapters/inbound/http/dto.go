package http

import (
	"time"

	"github.com/claudioed/last-mile-delivery/internal/application/views"
)

// The wire shapes of apis/openapi.yaml (camelCase JSON). No address or any
// other PII appears in any of them.

type scheduleWaveRequest struct {
	StationNodeID  string    `json:"stationNodeId"`
	ServiceDate    string    `json:"serviceDate"`
	InductCutoffAt time.Time `json:"inductCutoffAt"`
	WindowStart    time.Time `json:"windowStart"`
	WindowEnd      time.Time `json:"windowEnd"`
	StopCapacity   int       `json:"stopCapacity"`
}

type cancelWaveRequest struct {
	Reason string `json:"reason"`
}

type stopInput struct {
	StopNo          int      `json:"stopNo"`
	TrackingNumbers []string `json:"trackingNumbers"`
}

type planRouteRequest struct {
	WaveID string      `json:"waveId"`
	DSPID  string      `json:"dspId"`
	Stops  []stopInput `json:"stops"`
}

type recordAttemptRequest struct {
	TrackingNumber string    `json:"trackingNumber"`
	Outcome        string    `json:"outcome"`
	FailureReason  string    `json:"failureReason"`
	At             time.Time `json:"at"`
}

type contractPartnerRequest struct {
	StationNodeID string `json:"stationNodeId"`
	VanCapacity   int    `json:"vanCapacity"`
}

type waveBody struct {
	WaveID         string    `json:"waveId"`
	StationNodeID  string    `json:"stationNodeId"`
	ServiceDate    string    `json:"serviceDate"`
	InductCutoffAt time.Time `json:"inductCutoffAt"`
	WindowStart    time.Time `json:"windowStart"`
	WindowEnd      time.Time `json:"windowEnd"`
	StopCapacity   int       `json:"stopCapacity"`
	AssignedStops  int       `json:"assignedStops"`
	RemainingStops int       `json:"remainingStops"`
	State          string    `json:"state"`
	CancelReason   string    `json:"cancelReason,omitempty"`
}

type waveStopBody struct {
	StopKey                 string   `json:"stopKey"`
	DestinationPostalPrefix string   `json:"destinationPostalPrefix,omitempty"`
	TrackingNumbers         []string `json:"trackingNumbers"`
}

type stopBody struct {
	StopNo          int      `json:"stopNo"`
	TrackingNumbers []string `json:"trackingNumbers"`
	Outcome         string   `json:"outcome,omitempty"`
}

type attemptBody struct {
	RouteID        string    `json:"routeId"`
	TrackingNumber string    `json:"trackingNumber"`
	Outcome        string    `json:"outcome"`
	FailureReason  string    `json:"failureReason,omitempty"`
	At             time.Time `json:"at"`
}

type routeBody struct {
	RouteID       string        `json:"routeId"`
	WaveID        string        `json:"waveId"`
	DSPID         string        `json:"dspId"`
	StationNodeID string        `json:"stationNodeId"`
	State         string        `json:"state"`
	StopCount     int           `json:"stopCount"`
	Stops         []stopBody    `json:"stops"`
	Attempts      []attemptBody `json:"attempts"`
}

type partnerBody struct {
	DSPID         string `json:"dspId"`
	StationNodeID string `json:"stationNodeId"`
	VanCapacity   int    `json:"vanCapacity"`
	State         string `json:"state"`
}

func toWaveBody(w views.Wave) waveBody {
	return waveBody{
		WaveID: w.WaveID, StationNodeID: w.StationNodeID, ServiceDate: w.ServiceDate, InductCutoffAt: w.InductCutoffAt.UTC(),
		WindowStart: w.WindowStart.UTC(), WindowEnd: w.WindowEnd.UTC(), StopCapacity: w.StopCapacity, AssignedStops: w.AssignedStops,
		RemainingStops: w.RemainingStops, State: w.State, CancelReason: w.CancelReason,
	}
}

func toAttemptBody(a views.Attempt) attemptBody {
	return attemptBody{RouteID: a.RouteID, TrackingNumber: a.TrackingNumber, Outcome: a.Outcome, FailureReason: a.FailureReason, At: a.At.UTC()}
}

func toRouteBody(r views.Route) routeBody {
	b := routeBody{
		RouteID: r.RouteID, WaveID: r.WaveID, DSPID: r.DSPID, StationNodeID: r.StationNodeID, State: r.State, StopCount: r.StopCount,
		Stops: make([]stopBody, 0, len(r.Stops)), Attempts: make([]attemptBody, 0, len(r.Attempts)),
	}
	for _, s := range r.Stops {
		b.Stops = append(b.Stops, stopBody{StopNo: s.StopNo, TrackingNumbers: nonNil(s.TrackingNumbers), Outcome: s.Outcome})
	}
	for _, a := range r.Attempts {
		b.Attempts = append(b.Attempts, toAttemptBody(a))
	}
	return b
}

func toPartnerBody(p views.Partner) partnerBody {
	return partnerBody{DSPID: p.DSPID, StationNodeID: p.StationNodeID, VanCapacity: p.VanCapacity, State: p.State}
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
