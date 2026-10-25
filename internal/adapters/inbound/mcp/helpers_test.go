package mcp_test

import (
	"context"
	"strings"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	inboundmcp "github.com/claudioed/last-mile-delivery/internal/adapters/inbound/mcp"
	"github.com/claudioed/last-mile-delivery/internal/adapters/outbound/memory"
	"github.com/claudioed/last-mile-delivery/internal/application/usecases"
	"github.com/claudioed/last-mile-delivery/internal/application/views"
	"github.com/claudioed/last-mile-delivery/internal/domain/partner"
	"github.com/claudioed/last-mile-delivery/internal/domain/route"
	"github.com/claudioed/last-mile-delivery/internal/domain/wave"
)

// t0 is the fixture's fixed "now" (the service date of every seeded wave).
var t0 = time.Date(2026, 10, 24, 6, 0, 0, 0, time.UTC)

// harness is a connected in-memory MCP client over the real server wired to
// the in-memory repositories, exactly as cmd/mcp wires them (minus Postgres).
// The write use cases exist ONLY here, to seed data the way the REST adapter
// and the consumers would: the MCP surface itself has none.
type harness struct {
	t       *testing.T
	session *sdk.ClientSession
	deps    usecases.Deps
	outbox  *memory.Outbox
}

func newHarness(t *testing.T) *harness {
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
	return &harness{
		t:       t,
		session: connectSession(t, inboundmcp.Deps{ListWaves: usecases.NewListWaves(deps), GetWaveStops: usecases.NewGetWaveStops(deps), GetRoute: usecases.NewGetRoute(deps)}),
		deps:    deps,
		outbox:  store.Outbox(),
	}
}

func connectSession(t *testing.T, deps inboundmcp.Deps) *sdk.ClientSession {
	t.Helper()
	ctx := context.Background()
	clientTransport, serverTransport := sdk.NewInMemoryTransports()
	serverSession, err := inboundmcp.NewServer(deps, "test").Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatalf("server connect: %v", err)
	}
	t.Cleanup(func() { _ = serverSession.Close() })
	client := sdk.NewClient(&sdk.Implementation{Name: "test-client", Version: "0"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// scheduleWave schedules node-ds-01's wave for 2026-10-24 (id wave-1 under the
// sequential id generator).
func (h *harness) scheduleWave(t *testing.T) {
	t.Helper()
	_, err := usecases.NewScheduleWave(h.deps).Execute(t.Context(), usecases.ScheduleWaveCommand{
		StationNodeID: "node-ds-01", ServiceDate: "2026-10-24",
		InductCutoffAt: t0.Add(2 * time.Hour), WindowStart: t0.Add(6 * time.Hour), WindowEnd: t0.Add(14 * time.Hour),
		StopCapacity: 5,
	})
	must(t, err)
}

// arrive records a load that reached the station with the given packages
// (the linehaul consumer's use case), the way the event would.
func (h *harness) arrive(t *testing.T, eventID string, tns ...string) {
	t.Helper()
	err := usecases.NewInductOnArrival(h.deps).Execute(t.Context(), usecases.LoadArrivedCommand{
		EventID: eventID, EventType: "LoadArrived", LoadID: "load-" + eventID, DepartureID: "dep-1",
		DestinationNodeID: "node-ds-01", NodeType: views.DeliveryStation, TrackingNumbers: tns, ArrivedAt: t0,
	})
	must(t, err)
}

// label feeds one package's label data into the PackageStopView (the shipping
// consumer's use case), so its stop key and postal prefix are known.
func (h *harness) label(t *testing.T, tn, stopKey, prefix string) {
	t.Helper()
	err := usecases.NewApplyLabelIssued(h.deps).Execute(t.Context(), usecases.LabelIssuedCommand{
		EventID: "label-" + tn, EventType: "LabelIssued", TrackingNumber: tn, StopKey: stopKey,
		DestinationPostalPrefix: prefix, AssignmentID: "asg-" + tn,
	})
	must(t, err)
}

// withWave seeds one wave for node-ds-01 on 2026-10-24 holding TRK-1 and
// TRK-2 (labels known: stop-se-1 with prefix 12345, stop-se-2 with 67890).
func withWave(t *testing.T) *harness {
	t.Helper()
	h := newHarness(t)
	h.label(t, "TRK-1", "stop-se-1", "12345")
	h.label(t, "TRK-2", "stop-se-2", "67890")
	h.arrive(t, "e1", "TRK-1", "TRK-2")
	h.scheduleWave(t)
	return h
}

// withRoute plans and starts route-1 (dsp-1, both packages) on the seeded
// wave and records one delivered attempt for TRK-1.
func withRoute(t *testing.T) *harness {
	t.Helper()
	h := withWave(t)
	_, err := usecases.NewContractPartner(h.deps).Execute(t.Context(), usecases.ContractPartnerCommand{StationNodeID: "node-ds-01", VanCapacity: 5})
	must(t, err)
	_, err = usecases.NewCloseInduction(h.deps).Execute(t.Context(), wave.ID("wave-1"))
	must(t, err)
	_, err = usecases.NewPlanRoute(h.deps).Execute(t.Context(), usecases.PlanRouteCommand{
		WaveID: wave.ID("wave-1"), DSPID: partner.ID("dsp-1"),
		Stops: []route.Stop{{No: 1, TrackingNumbers: []string{"TRK-1"}}, {No: 2, TrackingNumbers: []string{"TRK-2"}}},
	})
	must(t, err)
	_, err = usecases.NewDispatchWave(h.deps).Execute(t.Context(), wave.ID("wave-1"))
	must(t, err)
	_, err = usecases.NewStartRoute(h.deps).Execute(t.Context(), route.ID("route-1"))
	must(t, err)
	_, err = usecases.NewRecordAttempt(h.deps).Execute(t.Context(), usecases.RecordAttemptCommand{
		RouteID: route.ID("route-1"), TrackingNumber: "TRK-1", Outcome: route.OutcomeDelivered, At: t0.Add(7 * time.Hour),
	})
	must(t, err)
	return h
}

// call invokes a tool and fails the test on a TRANSPORT/protocol error;
// tool-level failures come back as res.IsError.
func call(t *testing.T, session *sdk.ClientSession, name string, args map[string]any) *sdk.CallToolResult {
	t.Helper()
	res, err := session.CallTool(context.Background(), &sdk.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: transport/protocol error (want a tool result): %v", name, err)
	}
	return res
}

// ok calls a tool, requires success, and returns its structured content.
func (h *harness) ok(t *testing.T, name string, args map[string]any) map[string]any {
	t.Helper()
	res := call(t, h.session, name, args)
	if res.IsError {
		t.Fatalf("%s returned a tool error: %s", name, text(res))
	}
	sc, isMap := res.StructuredContent.(map[string]any)
	if !isMap {
		t.Fatalf("%s: structured content = %#v, want an object", name, res.StructuredContent)
	}
	return sc
}

// failWith calls a tool and requires an isError result whose text starts
// with the slug want.
func failWith(t *testing.T, session *sdk.ClientSession, name string, args map[string]any, want string) {
	t.Helper()
	res := call(t, session, name, args)
	if !res.IsError {
		t.Fatalf("%s: expected an isError tool result, got %#v", name, res.StructuredContent)
	}
	if got := text(res); !strings.HasPrefix(got, want+": ") {
		t.Fatalf("%s: error text %q does not start with %q", name, got, want+": ")
	}
}

func text(res *sdk.CallToolResult) string {
	var b strings.Builder
	for _, c := range res.Content {
		if tc, isText := c.(*sdk.TextContent); isText {
			b.WriteString(tc.Text)
		}
	}
	return b.String()
}
