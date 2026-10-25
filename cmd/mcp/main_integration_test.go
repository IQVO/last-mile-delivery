//go:build integration

package main

import (
	"context"
	"os"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	inboundmcp "github.com/claudioed/last-mile-delivery/internal/adapters/inbound/mcp"
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
// are already migrated, a second run must be a no-op), then reads through MCP
// tools exactly what the write use cases persisted, and its reads add no
// outbox row.
func TestMCPAgainstPostgres_ReadsWhatTheAPIWroteAndNeverWrites(t *testing.T) {
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

	ct, st := sdk.NewInMemoryTransports()
	ss, err := inboundmcp.NewServer(deps, "test").Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ss.Close()
	session, err := sdk.NewClient(&sdk.Implementation{Name: "t", Version: "0"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()

	assertSeededReads(t, session, sd)

	if after := countOutbox(ctx, pool); after != before {
		t.Fatalf("outbox rows %d -> %d: the MCP binary wrote an event", before, after)
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

// assertSeededReads reads back, through every MCP tool, what
// seedThroughWriteUseCases persisted.
func assertSeededReads(t *testing.T, session *sdk.ClientSession, sd seed) {
	t.Helper()
	ctx := context.Background()
	call := func(name string, args map[string]any) map[string]any {
		t.Helper()
		res, err := session.CallTool(ctx, &sdk.CallToolParams{Name: name, Arguments: args})
		if err != nil || res.IsError {
			t.Fatalf("%s: err=%v res=%+v", name, err, res)
		}
		return res.StructuredContent.(map[string]any)
	}

	page := call("list_waves", map[string]any{"stationNodeId": sd.stationNodeID, "serviceDate": sd.serviceDate})
	items := page["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("list_waves = %v, want 1 wave", page)
	}
	w := items[0].(map[string]any)
	if w["waveId"] != sd.waveID || w["state"] != "DISPATCHED" || w["stopCapacity"] != 5.0 || w["assignedStops"] != 2.0 {
		t.Fatalf("list_waves wave = %v", w)
	}

	stops := call("get_wave_stops", map[string]any{"waveId": sd.waveID})
	stopItems := stops["items"].([]any)
	if len(stopItems) != 2 {
		t.Fatalf("get_wave_stops = %v, want 2 stops", stops)
	}
	first := stopItems[0].(map[string]any)
	if first["stopKey"] != "stop-se-91" || first["destinationPostalPrefix"] != "12345" {
		t.Fatalf("first stop = %v", first)
	}

	rt := call("get_route", map[string]any{"routeId": sd.routeID})
	if rt["state"] != "OUT_FOR_DELIVERY" || rt["stopCount"] != 2.0 {
		t.Fatalf("get_route = %v", rt)
	}
	attempts := rt["attempts"].([]any)
	if len(attempts) != 1 || attempts[0].(map[string]any)["trackingNumber"] != "TRK-91" {
		t.Fatalf("get_route attempts = %v", rt["attempts"])
	}
}
