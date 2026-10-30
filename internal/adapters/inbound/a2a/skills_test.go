package a2a_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2aclient"
	"github.com/a2aproject/a2a-go/v2/a2asrv"

	inbounda2a "github.com/claudioed/last-mile-delivery/internal/adapters/inbound/a2a"
	"github.com/claudioed/last-mile-delivery/internal/adapters/outbound/memory"
	"github.com/claudioed/last-mile-delivery/internal/application/ports"
	"github.com/claudioed/last-mile-delivery/internal/application/repository"
	"github.com/claudioed/last-mile-delivery/internal/application/usecases"
	"github.com/claudioed/last-mile-delivery/internal/application/views"
	"github.com/claudioed/last-mile-delivery/internal/domain/partner"
	"github.com/claudioed/last-mile-delivery/internal/domain/route"
	"github.com/claudioed/last-mile-delivery/internal/domain/wave"
)

// t0 is the fixture's fixed "now" (the service date of every seeded wave).
var t0 = time.Date(2026, 10, 24, 6, 0, 0, 0, time.UTC)

// fixture is the real use cases over the in-memory repositories. The write use
// cases exist ONLY here, to seed data the way the REST adapter and the
// consumers would: the A2A surface itself holds none.
type fixture struct {
	t      *testing.T
	deps   usecases.Deps
	outbox *memory.Outbox
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	store := memory.NewStore()
	deps := usecases.Deps{
		Waves: store.Waves(), Routes: store.Routes(), Partners: store.Partners(), Histories: store.Histories(), Stations: store.Stations(),
		ServiceAreas: store.ServiceAreas(), PackageStops: store.PackageStops(), Inbound: store.Inbound(), Inbox: store.Inbox(),
		Outbox: store.Outbox(), UoW: store.UnitOfWork(), Clock: memory.NewClock(t0), IDs: &memory.SequentialIDs{},
	}
	if err := deps.Stations.Save(context.Background(), views.Station{NodeID: "node-ds-01", NodeType: views.DeliveryStation, RegionID: "region-se"}); err != nil {
		t.Fatal(err)
	}
	return &fixture{t: t, deps: deps, outbox: store.Outbox()}
}

func (f *fixture) must(err error) {
	f.t.Helper()
	if err != nil {
		f.t.Fatal(err)
	}
}

// withRoute seeds one delivery day: two labelled packages arrive, wave-1 is
// scheduled, dsp-1 is contracted, route-1 is planned, the wave dispatched, the
// route started and TRK-1 delivered.
func (f *fixture) withRoute() *fixture {
	f.t.Helper()
	ctx := f.t.Context()
	for _, p := range []struct{ tn, stopKey, prefix string }{{"TRK-1", "stop-se-1", "12345"}, {"TRK-2", "stop-se-2", "67890"}} {
		f.must(usecases.NewApplyLabelIssued(f.deps).Execute(ctx, usecases.LabelIssuedCommand{
			EventID: "label-" + p.tn, EventType: "LabelIssued", TrackingNumber: p.tn, StopKey: p.stopKey,
			DestinationPostalPrefix: p.prefix, AssignmentID: "asg-" + p.tn,
		}))
	}
	f.must(usecases.NewInductOnArrival(f.deps).Execute(ctx, usecases.LoadArrivedCommand{
		EventID: "e1", EventType: "LoadArrived", LoadID: "load-e1", DepartureID: "dep-1",
		DestinationNodeID: "node-ds-01", NodeType: views.DeliveryStation, TrackingNumbers: []string{"TRK-1", "TRK-2"}, ArrivedAt: t0,
	}))
	_, err := usecases.NewScheduleWave(f.deps).Execute(ctx, usecases.ScheduleWaveCommand{
		StationNodeID: "node-ds-01", ServiceDate: "2026-10-24",
		InductCutoffAt: t0.Add(2 * time.Hour), WindowStart: t0.Add(6 * time.Hour), WindowEnd: t0.Add(14 * time.Hour), StopCapacity: 5,
	})
	f.must(err)
	_, err = usecases.NewContractPartner(f.deps).Execute(ctx, usecases.ContractPartnerCommand{StationNodeID: "node-ds-01", VanCapacity: 5})
	f.must(err)
	_, err = usecases.NewCloseInduction(f.deps).Execute(ctx, wave.ID("wave-1"))
	f.must(err)
	_, err = usecases.NewPlanRoute(f.deps).Execute(ctx, usecases.PlanRouteCommand{
		WaveID: wave.ID("wave-1"), DSPID: partner.ID("dsp-1"),
		Stops: []route.Stop{{No: 1, TrackingNumbers: []string{"TRK-1"}}, {No: 2, TrackingNumbers: []string{"TRK-2"}}},
	})
	f.must(err)
	_, err = usecases.NewDispatchWave(f.deps).Execute(ctx, wave.ID("wave-1"))
	f.must(err)
	_, err = usecases.NewStartRoute(f.deps).Execute(ctx, route.ID("route-1"))
	f.must(err)
	_, err = usecases.NewRecordAttempt(f.deps).Execute(ctx, usecases.RecordAttemptCommand{
		RouteID: route.ID("route-1"), TrackingNumber: "TRK-1", Outcome: route.OutcomeDelivered, At: t0.Add(7 * time.Hour),
	})
	f.must(err)
	return f
}

// agentDeps is what cmd/a2a wires: the two read use cases, nothing else.
func (f *fixture) agentDeps() inbounda2a.Deps {
	return inbounda2a.Deps{ListWaves: usecases.NewListWaves(f.deps), GetRoute: usecases.NewGetRoute(f.deps)}
}

// ---- the wire: JSON-RPC over HTTP, driven by the SDK's own client ----------

// wireEnvelope is a TEST-LOCAL copy of the answer contract. If the adapter's
// envelope drifts, this suite fails instead of silently following it.
type wireEnvelope struct {
	Skill string `json:"skill"`
	Data  struct {
		Waves *struct {
			StationNodeID string     `json:"stationNodeId"`
			ServiceDate   string     `json:"serviceDate"`
			Items         []wireWave `json:"items"`
		} `json:"waves"`
		Route *wireRoute `json:"route"`
	} `json:"data"`
	Problem *struct {
		Type   string `json:"type"`
		Title  string `json:"title"`
		Detail string `json:"detail"`
		Status int    `json:"status"`
	} `json:"problem"`
}

type wireWave struct {
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
}

type wireRoute struct {
	RouteID       string `json:"routeId"`
	WaveID        string `json:"waveId"`
	DSPID         string `json:"dspId"`
	StationNodeID string `json:"stationNodeId"`
	State         string `json:"state"`
	StopCount     int    `json:"stopCount"`
	Stops         []struct {
		StopNo          int      `json:"stopNo"`
		TrackingNumbers []string `json:"trackingNumbers"`
		Outcome         string   `json:"outcome"`
	} `json:"stops"`
	Attempts []struct {
		RouteID        string `json:"routeId"`
		TrackingNumber string `json:"trackingNumber"`
		Outcome        string `json:"outcome"`
		At             string `json:"at"`
	} `json:"attempts"`
}

type wire struct {
	client *a2aclient.Client
}

func newWire(t *testing.T, deps inbounda2a.Deps) *wire {
	t.Helper()
	srv := httptest.NewServer(inbounda2a.Handler(inbounda2a.NewExecutor(deps)))
	t.Cleanup(srv.Close)
	card := inbounda2a.Card(inbounda2a.Config{AgentURL: srv.URL + "/", Version: "test"})
	client, err := a2aclient.NewFromCard(context.Background(), card)
	if err != nil {
		t.Fatalf("NewFromCard: %v", err)
	}
	return &wire{client: client}
}

// call sends one message part and returns the decoded envelope and the
// one-line text summary of the agent's reply.
func (w *wire) call(t *testing.T, part *a2a.Part) (wireEnvelope, string) {
	t.Helper()
	resp, err := w.client.SendMessage(context.Background(), &a2a.SendMessageRequest{Message: a2a.NewMessage(a2a.MessageRoleUser, part)})
	if err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	msg, ok := resp.(*a2a.Message)
	if !ok || len(msg.Parts) < 2 {
		t.Fatalf("reply = %T, want a message with a data and a text part", resp)
	}
	data, ok := msg.Parts[0].Content.(a2a.Data)
	if !ok {
		t.Fatalf("first reply part is %T, want data", msg.Parts[0].Content)
	}
	raw, _ := json.Marshal(data.Value)
	var env wireEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatalf("reply is not the skill envelope: %v (%s)", err, raw)
	}
	text, _ := msg.Parts[1].Content.(a2a.Text)
	return env, string(text)
}

func (w *wire) skill(t *testing.T, skill string, args map[string]any) (wireEnvelope, string) {
	t.Helper()
	return w.call(t, a2a.NewDataPart(map[string]any{"skill": skill, "arguments": args}))
}

// wantProblem requires an in-band problem with the REST slug as its type.
func wantProblem(t *testing.T, env wireEnvelope, status int, slug string) {
	t.Helper()
	if env.Problem == nil {
		t.Fatalf("expected a %d problem, got data: %+v", status, env.Data)
	}
	if env.Problem.Status != status || env.Problem.Type != slug {
		t.Fatalf("problem = %+v, want %d %s", env.Problem, status, slug)
	}
}

func TestWireListWavesReturnsTheStationsDay(t *testing.T) {
	w := newWire(t, newFixture(t).withRoute().agentDeps())

	env, text := w.skill(t, "list_waves", map[string]any{"stationNodeId": "node-ds-01", "serviceDate": "2026-10-24"})
	l := env.Data.Waves
	if env.Problem != nil || l == nil || len(l.Items) != 1 || l.StationNodeID != "node-ds-01" || l.ServiceDate != "2026-10-24" {
		t.Fatalf("envelope = %+v", env)
	}
	want := wireWave{
		WaveID: "wave-1", StationNodeID: "node-ds-01", ServiceDate: "2026-10-24",
		InductCutoffAt: "2026-10-24T08:00:00Z", WindowStart: "2026-10-24T12:00:00Z", WindowEnd: "2026-10-24T20:00:00Z",
		StopCapacity: 5, AssignedStops: 2, RemainingStops: 3, State: "DISPATCHED",
	}
	if l.Items[0] != want {
		t.Fatalf("wave = %+v, want %+v", l.Items[0], want)
	}
	if text != "1 waves at node-ds-01 on 2026-10-24: wave-1 DISPATCHED (2/5 stops assigned)" {
		t.Fatalf("summary = %q", text)
	}
}

func TestWireListWavesWithNoWaveIsAnEmptyListNotAProblem(t *testing.T) {
	w := newWire(t, newFixture(t).agentDeps())

	env, text := w.skill(t, "list_waves", map[string]any{"stationNodeId": "node-ds-01", "serviceDate": "2026-10-25"})
	if env.Problem != nil || env.Data.Waves == nil || env.Data.Waves.Items == nil || len(env.Data.Waves.Items) != 0 {
		t.Fatalf("envelope = %+v, want an empty items array", env)
	}
	if text != "0 waves at node-ds-01 on 2026-10-25" {
		t.Fatalf("summary = %q", text)
	}
}

func TestWireGetRouteReturnsStopsAndAttempts(t *testing.T) {
	w := newWire(t, newFixture(t).withRoute().agentDeps())

	env, text := w.skill(t, "get_route", map[string]any{"routeId": "route-1"})
	r := env.Data.Route
	if env.Problem != nil || r == nil {
		t.Fatalf("envelope = %+v", env)
	}
	assertRouteHeader(t, r)
	assertRouteStopsAndAttempts(t, r)
	if text != "route-1: out_for_delivery, partner dsp-1, wave wave-1, 2 stops, 1 attempts recorded" {
		t.Fatalf("summary = %q", text)
	}
}

func assertRouteHeader(t *testing.T, r *wireRoute) {
	t.Helper()
	got := []string{r.RouteID, r.WaveID, r.DSPID, r.StationNodeID, r.State}
	want := []string{"route-1", "wave-1", "dsp-1", "node-ds-01", "OUT_FOR_DELIVERY"}
	if !slices.Equal(got, want) || r.StopCount != 2 || len(r.Stops) != 2 || len(r.Attempts) != 1 {
		t.Fatalf("route = %+v", r)
	}
}

func assertRouteStopsAndAttempts(t *testing.T, r *wireRoute) {
	t.Helper()
	// An undecided stop carries no outcome.
	if r.Stops[1].StopNo != 2 || r.Stops[1].Outcome != "" || r.Stops[0].TrackingNumbers[0] != "TRK-1" {
		t.Fatalf("stops = %+v", r.Stops)
	}
	a := r.Attempts[0]
	if got := []string{a.TrackingNumber, a.Outcome, a.At}; !slices.Equal(got, []string{"TRK-1", "DELIVERED", "2026-10-24T13:00:00Z"}) {
		t.Fatalf("attempt = %+v", a)
	}
}

// An unknown route is a 404 not-found answered in-band, with the same type and
// status the REST adapter gives GET /routes/{routeId}; a malformed list_waves
// argument is the REST 422 validation-failed.
func TestWireDomainRejectionsAreAnsweredWithTheRESTClassification(t *testing.T) {
	w := newWire(t, newFixture(t).withRoute().agentDeps())

	env, text := w.skill(t, "get_route", map[string]any{"routeId": "route-nope"})
	wantProblem(t, env, 404, "not-found")
	if env.Problem.Detail != `no route exists with routeId "route-nope"` || env.Problem.Title != "Resource not found" {
		t.Fatalf("problem = %+v", env.Problem)
	}
	if text != `Resource not found (404): no route exists with routeId "route-nope"` {
		t.Fatalf("summary = %q", text)
	}

	for name, args := range map[string]map[string]any{
		"date not YYYY-MM-DD": {"stationNodeId": "node-ds-01", "serviceDate": "24-10-2026"},
		"impossible date":     {"stationNodeId": "node-ds-01", "serviceDate": "2026-02-30"},
		"station with space":  {"stationNodeId": "node ds 01", "serviceDate": "2026-10-24"},
	} {
		t.Run(name, func(t *testing.T) {
			env, text := w.skill(t, "list_waves", args)
			wantProblem(t, env, 422, "validation-failed")
			// The shared domain error carries the "validation:" sentinel text; the
			// adapter owns the answer text.
			for _, s := range []string{env.Problem.Detail, env.Problem.Title, text} {
				if strings.Contains(strings.ToLower(s), "validation:") || strings.Contains(s, "usecases") {
					t.Fatalf("an answer leaks the internal error text: %q", s)
				}
			}
		})
	}
}

// failingRoutes makes every read fail, standing in for a database outage.
type failingRoutes struct{ ports.RouteRepository }

var errBoom = errors.New("pgx: connection refused to 10.0.0.7:5432 password=hunter2")

func (failingRoutes) Get(context.Context, route.ID) (*route.Route, error) { return nil, errBoom }

// failingWaves makes the list read fail.
type failingWaves struct{ ports.WaveRepository }

func (failingWaves) List(context.Context, repository.WaveFilter) ([]*wave.DeliveryWave, error) {
	return nil, errBoom
}

// An unexpected failure is answered as a 500 problem, never a JSON-RPC error,
// and its raw text (which may carry infrastructure detail) is not echoed.
func TestWireUnexpectedFailureIs500AndDoesNotLeakTheCause(t *testing.T) {
	f := newFixture(t)
	f.deps.Routes = failingRoutes{f.deps.Routes}
	f.deps.Waves = failingWaves{f.deps.Waves}
	w := newWire(t, f.agentDeps())

	cases := map[string]struct {
		skill string
		args  map[string]any
	}{
		"get_route":  {"get_route", map[string]any{"routeId": "route-1"}},
		"list_waves": {"list_waves", map[string]any{"stationNodeId": "node-ds-01", "serviceDate": "2026-10-24"}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			env, text := w.skill(t, tc.skill, tc.args)
			wantProblem(t, env, 500, "internal-error")
			for _, s := range []string{env.Problem.Detail, env.Problem.Title, text} {
				if strings.Contains(s, "hunter2") || strings.Contains(s, "10.0.0.7") {
					t.Fatalf("a 500 answer leaks the underlying error: %q", s)
				}
			}
		})
	}
}

func TestWireArgumentValidation(t *testing.T) {
	w := newWire(t, newFixture(t).withRoute().agentDeps())
	cases := map[string]struct {
		skill string
		args  map[string]any
	}{
		"list: missing stationNodeId":  {"list_waves", map[string]any{"serviceDate": "2026-10-24"}},
		"list: missing serviceDate":    {"list_waves", map[string]any{"stationNodeId": "node-ds-01"}},
		"list: blank stationNodeId":    {"list_waves", map[string]any{"stationNodeId": "   ", "serviceDate": "2026-10-24"}},
		"list: no arguments":           {"list_waves", map[string]any{}},
		"list: serviceDate wrong type": {"list_waves", map[string]any{"stationNodeId": "node-ds-01", "serviceDate": 20261024}},
		"list: unknown argument":       {"list_waves", map[string]any{"stationNodeId": "node-ds-01", "serviceDate": "2026-10-24", "limit": 5}},
		"list: typo'd argument":        {"list_waves", map[string]any{"stationNodeId": "node-ds-01", "serviceDat": "2026-10-24", "serviceDate": "2026-10-24"}},
		"list: wrong-skill argument":   {"list_waves", map[string]any{"routeId": "route-1"}},
		"get: missing routeId":         {"get_route", map[string]any{}},
		"get: blank routeId":           {"get_route", map[string]any{"routeId": ""}},
		"get: routeId wrong type":      {"get_route", map[string]any{"routeId": 7}},
		"get: unknown argument":        {"get_route", map[string]any{"routeId": "route-1", "includeAttempts": false}},
		"get: wrong-skill argument":    {"get_route", map[string]any{"waveId": "wave-1"}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			env, _ := w.skill(t, tc.skill, tc.args)
			wantProblem(t, env, 400, "invalid-argument")
		})
	}
}

func TestWireRequestShapeProblems(t *testing.T) {
	w := newWire(t, newFixture(t).withRoute().agentDeps())

	env, _ := w.call(t, a2a.NewTextPart("not json at all"))
	wantProblem(t, env, 400, "malformed-skill-request")

	env, _ = w.call(t, a2a.NewDataPart(map[string]any{"arguments": map[string]any{}}))
	wantProblem(t, env, 400, "malformed-skill-request")

	env, _ = w.call(t, a2a.NewDataPart(map[string]any{"skill": "get_route", "arguments": map[string]any{"routeId": "route-1"}, "extra": 1}))
	wantProblem(t, env, 400, "malformed-skill-request")

	// Trailing data after the JSON document is refused too.
	env, _ = w.call(t, a2a.NewTextPart(`{"skill":"get_route","arguments":{"routeId":"route-1"}} {"skill":"list_waves"}`))
	wantProblem(t, env, 400, "malformed-skill-request")

	env, _ = w.skill(t, "launch_rocket", map[string]any{})
	wantProblem(t, env, 400, "unknown-skill")

	// A text part holding the same JSON is accepted (documented contract).
	env, _ = w.call(t, a2a.NewTextPart(`{"skill":"get_route","arguments":{"routeId":"route-1"}}`))
	if env.Problem != nil || env.Data.Route == nil || env.Data.Route.RouteID != "route-1" {
		t.Fatalf("a text part holding the skill JSON must be accepted, got %+v", env)
	}
}

// The writes of the REST surface (and the MCP tool this agent did not curate)
// are not offered here: asking for one is an unknown skill, answered in-band,
// and nothing is written.
func TestWireOffersNoWriteSkill(t *testing.T) {
	f := newFixture(t).withRoute()
	w := newWire(t, f.agentDeps())
	before := len(f.outbox.Events())

	for _, skill := range []string{"schedule_wave", "plan_route", "start_route", "record_attempt", "close_induction", "dispatch_wave", "cancel_wave", "contract_partner", "get_wave_stops"} {
		env, _ := w.skill(t, skill, map[string]any{"waveId": "wave-1", "routeId": "route-1", "stationNodeId": "node-ds-01"})
		wantProblem(t, env, 400, "unknown-skill")
	}
	if after := len(f.outbox.Events()); after != before {
		t.Fatalf("outbox rows %d -> %d: an unknown skill wrote an event", before, after)
	}
}

// The two read skills emit nothing: the A2A surface never touches the outbox.
func TestWireReadsEmitNothing(t *testing.T) {
	f := newFixture(t).withRoute()
	w := newWire(t, f.agentDeps())
	before := len(f.outbox.Events())

	w.skill(t, "list_waves", map[string]any{"stationNodeId": "node-ds-01", "serviceDate": "2026-10-24"})
	w.skill(t, "get_route", map[string]any{"routeId": "route-1"})
	if after := len(f.outbox.Events()); after != before {
		t.Fatalf("outbox rows %d -> %d: a read skill appended an event", before, after)
	}
}

// TestCardIsServedAndDialable proves discovery end to end: the card handler
// serves the card at the well-known path and a client built from the FETCHED
// card reaches the agent.
func TestCardIsServedAndDialable(t *testing.T) {
	mux := http.NewServeMux()
	mux.Handle("/", inbounda2a.Handler(inbounda2a.NewExecutor(newFixture(t).withRoute().agentDeps())))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	mux.Handle(a2asrv.WellKnownAgentCardPath, a2asrv.NewStaticAgentCardHandler(inbounda2a.Card(inbounda2a.Config{AgentURL: srv.URL + "/", Version: "test"})))

	res, err := http.Get(srv.URL + a2asrv.WellKnownAgentCardPath)
	if err != nil || res.StatusCode != http.StatusOK {
		t.Fatalf("card fetch = %v / %v", res, err)
	}
	defer func() { _ = res.Body.Close() }()
	var card a2a.AgentCard
	if err := json.NewDecoder(res.Body).Decode(&card); err != nil {
		t.Fatal(err)
	}
	client, err := a2aclient.NewFromCard(context.Background(), &card)
	if err != nil {
		t.Fatal(err)
	}
	env, _ := (&wire{client: client}).skill(t, "get_route", map[string]any{"routeId": "route-1"})
	if env.Data.Route == nil {
		t.Fatalf("a client built from the served card could not call the agent: %+v", env)
	}
}
