package mcp

import (
	"context"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/claudioed/last-mile-delivery/internal/application/usecases"
	"github.com/claudioed/last-mile-delivery/internal/application/views"
	"github.com/claudioed/last-mile-delivery/internal/domain/route"
	"github.com/claudioed/last-mile-delivery/internal/domain/wave"
)

// Deps is everything the MCP tools need, injected by the composition root.
// It carries the SAME read use cases the REST adapter uses (GET /waves,
// GET /waves/{waveId}/stops, GET /routes/{routeId}); the adapter never
// constructs an outbound adapter itself, and it holds no write use case at
// all (docs/adr/0006).
type Deps struct {
	ListWaves    *usecases.ListWaves
	GetWaveStops *usecases.GetWaveStops
	GetRoute     *usecases.GetRoute
}

// --- inputs -------------------------------------------------------------------

type listWavesInput struct {
	StationNodeID string `json:"stationNodeId" jsonschema:"the delivery station whose waves to list, e.g. node-42"`
	ServiceDate   string `json:"serviceDate" jsonschema:"the station's local service date, YYYY-MM-DD, e.g. 2026-10-24"`
}

type waveIDInput struct {
	WaveID string `json:"waveId" jsonschema:"the wave id, e.g. wave-1b4e28ba-2fa1-4d3c-8f6e-0a1b2c3d4e5f"`
}

type routeIDInput struct {
	RouteID string `json:"routeId" jsonschema:"the route id, e.g. route-9f2c-..."`
}

// --- outputs (the REST bodies, camelCase like the REST contract) --------------

type waveView struct {
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

type wavePageView struct {
	Items []waveView `json:"items"`
}

type waveStopView struct {
	StopKey                 string   `json:"stopKey"`
	DestinationPostalPrefix string   `json:"destinationPostalPrefix,omitempty"`
	TrackingNumbers         []string `json:"trackingNumbers"`
}

type waveStopsView struct {
	Items []waveStopView `json:"items"`
}

type stopView struct {
	StopNo          int      `json:"stopNo"`
	TrackingNumbers []string `json:"trackingNumbers"`
	Outcome         string   `json:"outcome,omitempty"`
}

type attemptView struct {
	RouteID        string `json:"routeId"`
	TrackingNumber string `json:"trackingNumber"`
	Outcome        string `json:"outcome"`
	FailureReason  string `json:"failureReason,omitempty"`
	At             string `json:"at"`
}

type routeView struct {
	RouteID       string        `json:"routeId"`
	WaveID        string        `json:"waveId"`
	DSPID         string        `json:"dspId"`
	StationNodeID string        `json:"stationNodeId"`
	State         string        `json:"state"`
	StopCount     int           `json:"stopCount"`
	Stops         []stopView    `json:"stops"`
	Attempts      []attemptView `json:"attempts"`
}

// --- mapping ------------------------------------------------------------------

// instant formats an instant as RFC 3339 UTC; the zero time is "" (omitted).
func instant(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339Nano)
}

func toWave(w views.Wave) waveView {
	return waveView{
		WaveID: w.WaveID, StationNodeID: w.StationNodeID, ServiceDate: w.ServiceDate,
		InductCutoffAt: instant(w.InductCutoffAt), WindowStart: instant(w.WindowStart), WindowEnd: instant(w.WindowEnd),
		StopCapacity: w.StopCapacity, AssignedStops: w.AssignedStops, RemainingStops: w.RemainingStops,
		State: w.State, CancelReason: w.CancelReason,
	}
}

func toWavePage(waves []views.Wave) wavePageView {
	items := make([]waveView, 0, len(waves))
	for _, w := range waves {
		items = append(items, toWave(w))
	}
	return wavePageView{Items: items}
}

func toWaveStops(stops []views.WaveStop) waveStopsView {
	out := make([]waveStopView, 0, len(stops))
	for _, s := range stops {
		out = append(out, waveStopView{
			StopKey: s.StopKey, DestinationPostalPrefix: s.DestinationPostalPrefix,
			TrackingNumbers: nonNil(s.TrackingNumbers),
		})
	}
	return waveStopsView{Items: out}
}

func toRoute(r views.Route) routeView {
	out := routeView{
		RouteID: r.RouteID, WaveID: r.WaveID, DSPID: r.DSPID, StationNodeID: r.StationNodeID,
		State: r.State, StopCount: r.StopCount, Stops: make([]stopView, 0, len(r.Stops)), Attempts: make([]attemptView, 0, len(r.Attempts)),
	}
	for _, s := range r.Stops {
		out.Stops = append(out.Stops, stopView{StopNo: s.StopNo, TrackingNumbers: nonNil(s.TrackingNumbers), Outcome: s.Outcome})
	}
	for _, a := range r.Attempts {
		out.Attempts = append(out.Attempts, attemptView{
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

// --- handlers -----------------------------------------------------------------

// listWaves mirrors GET /waves: the waves of one station on one service date,
// ordered by induct cutoff. A station and date with no wave is an empty list.
func (d Deps) listWaves(ctx context.Context, in listWavesInput) (wavePageView, error) {
	waves, err := d.ListWaves.Execute(ctx, in.StationNodeID, in.ServiceDate)
	if err != nil {
		return wavePageView{}, mapError(err)
	}
	return toWavePage(waves), nil
}

// getWaveStops mirrors GET /waves/{waveId}/stops: the packages inducted in the
// wave grouped into stops by the opaque stopKey, in first-induction order.
func (d Deps) getWaveStops(ctx context.Context, in waveIDInput) (waveStopsView, error) {
	stops, err := d.GetWaveStops.Execute(ctx, wave.ID(in.WaveID))
	if err != nil {
		return waveStopsView{}, mapError(err)
	}
	return toWaveStops(stops), nil
}

// getRoute mirrors GET /routes/{routeId}: the route with its stops (outcome
// derived) and every recorded attempt.
func (d Deps) getRoute(ctx context.Context, in routeIDInput) (routeView, error) {
	r, err := d.GetRoute.Execute(ctx, route.ID(in.RouteID))
	if err != nil {
		return routeView{}, mapError(err)
	}
	return toRoute(r), nil
}

// --- registry -----------------------------------------------------------------

// registerTools adds the three read-only tools. Every tool is annotated
// read-only, idempotent and closed-world; TestToolSurface pins the set and
// fails the build on any write-verb name (docs/adr/0006).
func (d Deps) registerTools(server *mcp.Server) {
	closedWorld := false
	readOnly := &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true, OpenWorldHint: &closedWorld}

	addTool(server, &mcp.Tool{
		Name: "list_waves",
		Description: "List the delivery waves of one station for one service date, ordered by inductCutoffAt: state " +
			"(OPEN, INDUCTION_CLOSED, DISPATCHED, COMPLETED or CANCELLED), the induct cutoff and delivery window, the stop " +
			"capacity with assignedStops and remainingStops, and cancelReason on a cancelled wave. A station and date with " +
			"no wave is an empty list. Read-only; a malformed station or a serviceDate that is not YYYY-MM-DD fails with " +
			"validation-failed.",
		Annotations: readOnly,
	}, d.listWaves)

	addTool(server, &mcp.Tool{
		Name: "get_wave_stops",
		Description: "Read the stops of one wave: the packages inducted in it grouped by the opaque stopKey (the keyed " +
			"hash of the normalized address; pkg:<trackingNumber> when no label data was received), in the order of each " +
			"stop's first induction. Each stop carries its trackingNumbers and destinationPostalPrefix when the label data " +
			"had it; no address is ever returned. Read-only; an unknown wave fails with not-found.",
		Annotations: readOnly,
	}, d.getWaveStops)

	addTool(server, &mcp.Tool{
		Name: "get_route",
		Description: "Read one route of a delivery service partner: its state (PLANNED, OUT_FOR_DELIVERY or COMPLETED), " +
			"its numbered stops with the trackingNumbers of each and the outcome derived from the per-package attempts " +
			"(omitted until every package of the stop has one), and every recorded attempt (trackingNumber, DELIVERED or " +
			"FAILED, failureReason, at). Read-only; an unknown route fails with not-found.",
		Annotations: readOnly,
	}, d.getRoute)
}

// addTool registers one tool. A handler error is returned to the SDK as the
// handler's error, which the SDK turns into an isError tool result (never a
// transport/protocol failure), carrying the error text.
func addTool[In, Out any](
	server *mcp.Server,
	tool *mcp.Tool,
	handle func(context.Context, In) (Out, error),
) {
	mcp.AddTool(server, tool, func(ctx context.Context, _ *mcp.CallToolRequest, in In) (*mcp.CallToolResult, Out, error) {
		out, err := handle(ctx, in)
		if err != nil {
			var zero Out
			return nil, zero, err
		}
		return nil, out, nil
	})
}
