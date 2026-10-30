//go:build integration

package main

import (
	"context"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2aclient"

	outboundkafka "github.com/claudioed/last-mile-delivery/internal/adapters/outbound/kafka"
	"github.com/claudioed/last-mile-delivery/internal/adapters/outbound/postgres"
	"github.com/claudioed/last-mile-delivery/internal/adapters/outbound/system"
	"github.com/claudioed/last-mile-delivery/internal/application/usecases"
	"github.com/claudioed/last-mile-delivery/internal/application/views"
	"github.com/claudioed/last-mile-delivery/internal/domain/partner"
	"github.com/claudioed/last-mile-delivery/internal/domain/route"
	"github.com/claudioed/last-mile-delivery/internal/domain/wave"
	"github.com/claudioed/last-mile-delivery/internal/testing/pgtest"
)

func TestMain(m *testing.M) {
	code := m.Run()
	pgtest.Shutdown()
	os.Exit(code)
}

// seed is what seedThroughWriteUseCases left behind for the reads to find.
type seed struct {
	stationNodeID string
	serviceDate   string
	waveID        string
	routeID       string
}

// countOutbox returns the number of outbox_events rows (the pool type is the
// pgtest re-export, so this file needs no pgx import of its own).
func countOutbox(ctx context.Context, pool pgtest.Pool) int {
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM outbox_events`).Scan(&n); err != nil {
		panic(err)
	}
	return n
}

// The binary migrates a database itself (idempotently: pgtest's databases
// are already migrated, a second run must be a no-op), then reads through the
// A2A skills exactly what the write use cases persisted, over the real
// router and the SDK client, and its reads add no outbox row.
func TestA2AAgainstPostgres_ReadsWhatTheAPIWroteAndNeverWrites(t *testing.T) {
	url := pgtest.URL(t) // a fresh, already migrated database
	ctx := context.Background()

	pool, err := postgres.NewPool(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)

	sd := seedThroughWriteUseCases(t, url)
	before := countOutbox(ctx, pool)
	if before < 2 {
		t.Fatalf("outbox rows after seeding = %d, want the wave, the out-for-delivery and the attempt events", before)
	}

	deps, closeFn, err := buildDeps(ctx, quietLogger(), url, url)
	if err != nil {
		t.Fatalf("buildDeps: %v", err)
	}
	t.Cleanup(closeFn)

	srv := httptest.NewServer(newAgent(deps, testAgentURL, "test", quietLogger(), "last-mile-delivery-a2a"))
	t.Cleanup(srv.Close)
	card := a2a.AgentCard{SupportedInterfaces: []*a2a.AgentInterface{a2a.NewAgentInterface(srv.URL+"/a2a", a2a.TransportProtocolJSONRPC)}}
	client, err := a2aclient.NewFromCard(ctx, &card)
	if err != nil {
		t.Fatal(err)
	}

	assertSeededReads(t, client, sd)

	if after := countOutbox(ctx, pool); after != before {
		t.Fatalf("outbox rows %d -> %d: the A2A binary wrote an event", before, after)
	}
}

// seedThroughWriteUseCases plays one delivery day the way cmd/api does: the
// station is registered, two labelled packages arrive, a wave is scheduled
// (which sweeps them in), a partner is contracted, the route is planned,
// the wave dispatched, the route started and one attempt delivered. It
// returns the planned route id.
func seedThroughWriteUseCases(t *testing.T, url string) seed {
	t.Helper()
	ctx := context.Background()
	// All times are relative to the real clock (system Clock), so the wave
	// stays induct-eligible whenever the test runs.
	now := time.Now().UTC()
	station := views.Station{NodeID: "node-ds-91", NodeType: views.DeliveryStation, RegionID: "region-se"}
	serviceDate := now.Format("2006-01-02")
	pool, err := postgres.NewPool(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	d := usecases.Deps{
		Waves: postgres.NewWaveRepository(pool), Routes: postgres.NewRouteRepository(pool), Partners: postgres.NewPartnerRepository(pool),
		Histories: postgres.NewHistoryRepository(pool), Stations: postgres.NewStationRepository(pool),
		ServiceAreas: postgres.NewServiceAreaRepository(pool), PackageStops: postgres.NewPackageStopRepository(pool),
		Inbound: postgres.NewInboundPackageRepository(pool), Inbox: postgres.NewInbox(pool),
		Outbox: postgres.NewOutbox(pool, outboundkafka.NewEncoder()), UoW: postgres.NewUnitOfWork(pool),
		Clock: system.Clock{}, IDs: system.IDs{},
	}

	run := func(err error, what string) {
		t.Helper()
		if err != nil {
			t.Fatalf("%s: %v", what, err)
		}
	}

	run(usecases.NewApplyNodeRegistered(d).Execute(ctx, usecases.NodeRegisteredCommand{
		EventID: "evt-nr", EventType: "NodeRegistered", NodeID: station.NodeID, NodeType: station.NodeType, RegionID: station.RegionID, Timezone: "UTC",
	}), "register station")
	for _, pkg := range []struct{ tn, stopKey, prefix string }{
		{"TRK-91", "stop-se-91", "12345"}, {"TRK-92", "stop-se-92", "67890"},
	} {
		run(usecases.NewApplyLabelIssued(d).Execute(ctx, usecases.LabelIssuedCommand{
			EventID: "evt-li-" + pkg.tn, EventType: "LabelIssued", TrackingNumber: pkg.tn,
			StopKey: pkg.stopKey, DestinationPostalPrefix: pkg.prefix, AssignmentID: "asg",
		}), "label "+pkg.tn)
	}
	run(usecases.NewInductOnArrival(d).Execute(ctx, usecases.LoadArrivedCommand{
		EventID: "evt-la", EventType: "LoadArrived", LoadID: "load-1", DepartureID: "dep-1",
		DestinationNodeID: station.NodeID, NodeType: views.DeliveryStation,
		TrackingNumbers: []string{"TRK-91", "TRK-92"}, ArrivedAt: now,
	}), "arrive")
	// The wave's cutoff and window are relative to the real clock (system
	// Clock), so the wave stays induct-eligible whenever the test runs.
	wv, err := usecases.NewScheduleWave(d).Execute(ctx, usecases.ScheduleWaveCommand{
		StationNodeID: station.NodeID, ServiceDate: now.Format("2006-01-02"),
		InductCutoffAt: now.Add(2 * time.Hour),
		WindowStart:    now.Add(6 * time.Hour),
		WindowEnd:      now.Add(14 * time.Hour),
		StopCapacity:   5,
	})
	run(err, "schedule wave")
	pt, err := usecases.NewContractPartner(d).Execute(ctx, usecases.ContractPartnerCommand{StationNodeID: station.NodeID, VanCapacity: 5})
	run(err, "contract partner")
	_, err = usecases.NewCloseInduction(d).Execute(ctx, wave.ID(wv.WaveID))
	run(err, "close induction")
	rt, err := usecases.NewPlanRoute(d).Execute(ctx, usecases.PlanRouteCommand{
		WaveID: wave.ID(wv.WaveID), DSPID: partner.ID(pt.DSPID),
		Stops: []route.Stop{{No: 1, TrackingNumbers: []string{"TRK-91"}}, {No: 2, TrackingNumbers: []string{"TRK-92"}}},
	})
	run(err, "plan route")
	_, err = usecases.NewDispatchWave(d).Execute(ctx, wave.ID(wv.WaveID))
	run(err, "dispatch wave")
	_, err = usecases.NewStartRoute(d).Execute(ctx, route.ID(rt.RouteID))
	run(err, "start route")
	_, err = usecases.NewRecordAttempt(d).Execute(ctx, usecases.RecordAttemptCommand{
		RouteID: route.ID(rt.RouteID), TrackingNumber: "TRK-91", Outcome: route.OutcomeDelivered,
		At: now.Add(time.Hour),
	})
	run(err, "record attempt")
	return seed{stationNodeID: station.NodeID, serviceDate: serviceDate, waveID: wv.WaveID, routeID: rt.RouteID}
}

// skillData sends one skill request and returns the "data" object of the
// answer envelope, failing on a problem.
func skillData(t *testing.T, client *a2aclient.Client, skill string, args map[string]any) map[string]any {
	t.Helper()
	resp, err := client.SendMessage(context.Background(), &a2a.SendMessageRequest{
		Message: a2a.NewMessage(a2a.MessageRoleUser, a2a.NewDataPart(map[string]any{"skill": skill, "arguments": args})),
	})
	if err != nil {
		t.Fatalf("%s: %v", skill, err)
	}
	msg, ok := resp.(*a2a.Message)
	if !ok || len(msg.Parts) == 0 {
		t.Fatalf("%s: reply = %T", skill, resp)
	}
	data, ok := msg.Parts[0].Content.(a2a.Data)
	if !ok {
		t.Fatalf("%s: first reply part is %T, want data", skill, msg.Parts[0].Content)
	}
	env, ok := data.Value.(map[string]any)
	if !ok || env["problem"] != nil {
		t.Fatalf("%s: envelope = %#v", skill, data.Value)
	}
	return env["data"].(map[string]any)
}

// assertSeededReads reads back, through both skills, what
// seedThroughWriteUseCases persisted.
func assertSeededReads(t *testing.T, client *a2aclient.Client, sd seed) {
	t.Helper()
	waves := skillData(t, client, "list_waves", map[string]any{"stationNodeId": sd.stationNodeID, "serviceDate": sd.serviceDate})["waves"].(map[string]any)
	items := waves["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("list_waves = %v, want 1 wave", waves)
	}
	w := items[0].(map[string]any)
	if w["waveId"] != sd.waveID || w["state"] != "DISPATCHED" || w["stopCapacity"] != 5.0 || w["assignedStops"] != 2.0 {
		t.Fatalf("list_waves wave = %v", w)
	}

	rt := skillData(t, client, "get_route", map[string]any{"routeId": sd.routeID})["route"].(map[string]any)
	if rt["state"] != "OUT_FOR_DELIVERY" || rt["stopCount"] != 2.0 {
		t.Fatalf("get_route = %v", rt)
	}
	attempts := rt["attempts"].([]any)
	if len(attempts) != 1 || attempts[0].(map[string]any)["trackingNumber"] != "TRK-91" {
		t.Fatalf("get_route attempts = %v", rt["attempts"])
	}
}
