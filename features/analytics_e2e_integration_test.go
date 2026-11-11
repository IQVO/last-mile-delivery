//go:build integration

package features_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"sync"
	"testing"
	"time"

	kafkago "github.com/segmentio/kafka-go"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/claudioed/last-mile-delivery/internal/adapters/inbound/kafka"
	"github.com/claudioed/last-mile-delivery/internal/adapters/inbound/reportshttp"
	"github.com/claudioed/last-mile-delivery/internal/adapters/kafka/cloudevents"
	"github.com/claudioed/last-mile-delivery/internal/adapters/outbound/analyticsstore"
	outboundkafka "github.com/claudioed/last-mile-delivery/internal/adapters/outbound/kafka"
	"github.com/claudioed/last-mile-delivery/internal/adapters/outbound/memory"
	relay "github.com/claudioed/last-mile-delivery/internal/adapters/outbound/outbox"
	"github.com/claudioed/last-mile-delivery/internal/adapters/outbound/postgres"
	"github.com/claudioed/last-mile-delivery/internal/adapters/outbound/system"
	"github.com/claudioed/last-mile-delivery/internal/application/outbox"
	"github.com/claudioed/last-mile-delivery/internal/application/usecases"
	"github.com/claudioed/last-mile-delivery/internal/application/views"
	"github.com/claudioed/last-mile-delivery/internal/domain/partner"
	"github.com/claudioed/last-mile-delivery/internal/domain/route"
	"github.com/claudioed/last-mile-delivery/internal/domain/wave"
	"github.com/claudioed/last-mile-delivery/internal/testing/kafkatest"
	"github.com/claudioed/last-mile-delivery/internal/testing/pgtest"
)

// The whole analytics read side (ADR 0012) against real infrastructure, all
// started with testcontainers (never skip-gated, never a DSN from env): the REAL
// use cases run a delivery day over the OLTP Postgres, the REAL encoder shapes
// the CloudEvents onto this context's own events AND analytics topics, the
// relay drains them to a real Kafka (losing one ack, so a row is republished
// under the SAME id), the projector's consumer projects both topics into a
// SEPARATE analytical Postgres (its own container), and the reports answer from
// it over a read-only pool -- while legacy, unknown-type, poison and duplicate
// messages share the topic. The DLQ is read back, and a later marker event
// proves the duplicate was consumed, not stuck.

func TestMain(m *testing.M) {
	code := m.Run()
	kafkatest.Shutdown()
	pgtest.Shutdown()
	os.Exit(code)
}

const e2eStation = "node-ds-e2e"

func e2eAnalyticalPostgres(t *testing.T) string {
	t.Helper()
	ctx := context.Background()
	c, err := tcpostgres.Run(ctx, "postgres:16-alpine",
		tcpostgres.WithDatabase("last_mile_analytics"), tcpostgres.WithUsername("itest"), tcpostgres.WithPassword("itest"),
		testcontainers.WithWaitStrategy(wait.ForLog("database system is ready to accept connections").WithOccurrence(2).WithStartupTimeout(90*time.Second)))
	if err != nil {
		t.Fatalf("start the analytical postgres: %v", err)
	}
	t.Cleanup(func() { _ = testcontainers.TerminateContainer(c) })
	url, err := c.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	return url
}

// lostAckOnce delivers to the real sink and THEN reports failure once: the
// "published, but the row was not marked" crash window, so the relay
// republishes the first batch with the SAME CloudEvents ids.
type lostAckOnce struct {
	inner relay.Sink
	mu    sync.Mutex
	fired bool
}

func (f *lostAckOnce) Send(ctx context.Context, msgs ...outbox.Message) error {
	if err := f.inner.Send(ctx, msgs...); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.fired {
		f.fired = true
		return errors.New("simulated crash after the broker ack, before the row was marked published")
	}
	return nil
}

func e2eAt(hour, minute int) time.Time { return time.Date(2026, 11, 3, hour, minute, 0, 0, time.UTC) }

func must[T any](t *testing.T, what string) func(T, error) T {
	t.Helper()
	return func(v T, err error) T {
		t.Helper()
		if err != nil {
			t.Fatalf("%s: %v", what, err)
		}
		return v
	}
}

// e2eDay runs one delivery day through the real use cases over the OLTP
// database: two waves (one cancelled), a cost declaration, three packages on two
// stops (A and B share one), one route, two deliveries and one failure.
func e2eDay(t *testing.T, d usecases.Deps, clock *memory.Clock) {
	ctx := context.Background()
	clock.Set(e2eAt(5, 0))
	if err := usecases.NewApplyNodeRegistered(d).Execute(ctx, usecases.NodeRegisteredCommand{
		EventID: "evt-node", EventType: kafka.TypeNodeRegistered, NodeID: e2eStation, NodeType: views.DeliveryStation, RegionID: "region-se", Timezone: "UTC",
	}); err != nil {
		t.Fatal(err)
	}
	for i, tn := range []string{"TRK-A", "TRK-B", "TRK-C"} {
		stop := map[string]string{"TRK-A": "stop-ab", "TRK-B": "stop-ab", "TRK-C": "stop-c"}[tn]
		if err := usecases.NewApplyLabelIssued(d).Execute(ctx, usecases.LabelIssuedCommand{
			EventID: fmt.Sprintf("evt-label-%d", i), EventType: kafka.TypeLabelIssued, TrackingNumber: tn, AssignmentID: "asg-" + tn, StopKey: stop, DestinationPostalPrefix: "013",
		}); err != nil {
			t.Fatal(err)
		}
	}
	schedule := func(cutoffHour, capacity int) views.Wave {
		return must[views.Wave](t, "schedule a wave")(usecases.NewScheduleWave(d).Execute(ctx, usecases.ScheduleWaveCommand{
			StationNodeID: e2eStation, ServiceDate: "2026-11-03", InductCutoffAt: e2eAt(cutoffHour, 0),
			WindowStart: e2eAt(12, 0), WindowEnd: e2eAt(20, 0), StopCapacity: capacity,
		}))
	}
	w1 := schedule(8, 4)
	w2 := schedule(9, 5)
	must[views.Wave](t, "cancel the second wave")(usecases.NewCancelWave(d).Execute(ctx, usecases.CancelWaveCommand{WaveID: wave.ID(w2.WaveID), Reason: "no van"}))
	clock.Set(e2eAt(6, 1))
	if err := usecases.NewInductOnArrival(d).Execute(ctx, usecases.LoadArrivedCommand{
		EventID: "evt-load", EventType: kafka.TypeLoadArrived, LoadID: "load-1", DepartureID: "dep-1", DestinationNodeID: e2eStation,
		NodeType: views.DeliveryStation, TrackingNumbers: []string{"TRK-A", "TRK-B", "TRK-C"}, ArrivedAt: e2eAt(6, 0),
	}); err != nil {
		t.Fatal(err)
	}
	clock.Set(e2eAt(8, 30))
	must[views.Wave](t, "close the induction")(usecases.NewCloseInduction(d).Execute(ctx, wave.ID(w1.WaveID)))
	dsp := must[views.Partner](t, "contract a partner")(usecases.NewContractPartner(d).Execute(ctx, usecases.ContractPartnerCommand{StationNodeID: e2eStation, VanCapacity: 3}))
	rt := must[views.Route](t, "plan the route")(usecases.NewPlanRoute(d).Execute(ctx, usecases.PlanRouteCommand{
		WaveID: wave.ID(w1.WaveID), DSPID: partner.ID(dsp.DSPID),
		Stops: []route.Stop{{No: 1, TrackingNumbers: []string{"TRK-A", "TRK-B"}}, {No: 2, TrackingNumbers: []string{"TRK-C"}}},
	}))
	must[views.Wave](t, "dispatch the wave")(usecases.NewDispatchWave(d).Execute(ctx, wave.ID(w1.WaveID)))
	clock.Set(e2eAt(11, 0))
	must[views.Route](t, "start the route")(usecases.NewStartRoute(d).Execute(ctx, route.ID(rt.RouteID)))
	must[views.Wave](t, "declare the cost")(usecases.NewDeclareWaveCost(d).Execute(ctx, usecases.DeclareWaveCostCommand{WaveID: wave.ID(w1.WaveID), CostPerStopCents: 250}))
	for i, a := range []struct {
		tn      string
		outcome route.Outcome
		reason  string
	}{{"TRK-A", route.OutcomeDelivered, ""}, {"TRK-B", route.OutcomeDelivered, ""}, {"TRK-C", route.OutcomeFailed, "NO_ACCESS"}} {
		at := e2eAt(13, 10*i)
		clock.Set(at.Add(time.Minute))
		must[views.Attempt](t, "attempt "+a.tn)(usecases.NewRecordAttempt(d).Execute(ctx, usecases.RecordAttemptCommand{
			RouteID: route.ID(rt.RouteID), TrackingNumber: a.tn, Outcome: a.outcome, FailureReason: a.reason, At: at,
		}))
	}
}

func TestAnalyticsReadSide_EndToEnd(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	brokers := kafkatest.Brokers(t)
	oltp := pgtest.NewPool(t)
	analyticalURL := e2eAnalyticalPostgres(t)
	if err := analyticsstore.RunMigrations(analyticalURL); err != nil {
		t.Fatal(err)
	}
	eventsTopic := kafkatest.Topic(t, kafka.OwnEventsTopic)
	analyticsTopic := kafkatest.Topic(t, kafka.OwnAnalyticsTopic)
	dlqTopic := kafkatest.Topic(t, kafka.AnalyticsDLQTopic)

	// ---- OLTP writes through the real use cases and the real encoder ----
	clock := memory.NewClock(e2eAt(5, 0))
	d := usecases.Deps{
		Waves: postgres.NewWaveRepository(oltp), Routes: postgres.NewRouteRepository(oltp), Partners: postgres.NewPartnerRepository(oltp),
		Histories: postgres.NewHistoryRepository(oltp), Stations: postgres.NewStationRepository(oltp),
		ServiceAreas: postgres.NewServiceAreaRepository(oltp), PackageStops: postgres.NewPackageStopRepository(oltp),
		Inbound: postgres.NewInboundPackageRepository(oltp), Inbox: postgres.NewInbox(oltp),
		Outbox: postgres.NewOutbox(oltp, &outboundkafka.Encoder{EventsTopic: eventsTopic, AnalyticsTopic: analyticsTopic}),
		UoW:    postgres.NewUnitOfWork(oltp), Clock: clock, IDs: system.IDs{},
	}
	e2eDay(t, d, clock)

	// ---- the events topic also carries messages the projector must survive ----
	legacy := []byte(`{"event_id":"e-1","event_type":"DeliveryAttempted","payload":{"trackingNumber":"x"}}`)
	unknown := hand(t, "evt-unknown", cloudevents.Type("route", "RouteTeleported"), "R-U", e2eAt(13, 5), map[string]any{"routeId": "R-U"})
	poison := hand(t, "evt-poison", cloudevents.Type("route", "DeliveryAttempted"), "R-P", e2eAt(13, 5),
		map[string]any{"routeId": "R-OTHER", "trackingNumber": "TRK-P", "outcome": "DELIVERED", "at": e2eAt(13, 5)}) // subject != aggregate id
	kafkatest.Produce(t, eventsTopic, kafkatest.Message{Key: "x", Value: legacy}, kafkatest.Message{Key: "R-U", Value: unknown},
		kafkatest.Message{Key: "R-P", Value: poison})

	// ---- relay -> real Kafka (with one lost ack: a republish under the SAME ids) ----
	sink := outboundkafka.NewRelaySink(brokers)
	t.Cleanup(func() { _ = sink.Close() })
	r := relay.NewRelay(postgres.NewOutboxRepo(oltp), &lostAckOnce{inner: sink}, quiet, relay.WithInterval(50*time.Millisecond))
	relayDone := make(chan struct{})
	go func() { defer close(relayDone); _ = r.Run(ctx) }()
	t.Cleanup(func() { cancel(); <-relayDone })

	// ---- the projector: one consumer over BOTH own topics, a writer pool on the analytical database ----
	writer, err := analyticsstore.NewPool(ctx, analyticalURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(writer.Close)
	consumer := kafka.NewAnalyticsConsumer(brokers, []string{eventsTopic, analyticsTopic}, dlqTopic,
		fmt.Sprintf("lm-projector-e2e-%d", time.Now().UnixNano()), analyticsstore.NewProjection(writer), quiet)
	consumer.Retry = kafka.RetryPolicy{Initial: 50 * time.Millisecond, Max: 200 * time.Millisecond}
	consumerDone := make(chan struct{})
	go func() { defer close(consumerDone); _ = consumer.Run(ctx) }()
	t.Cleanup(func() { cancel(); <-consumerDone; _ = consumer.Close() })

	// ---- the reports over a READ-ONLY pool ----
	readOnly, err := analyticsstore.NewReadOnlyPool(ctx, analyticalURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(readOnly.Close)
	reader := analyticsstore.NewReader(readOnly)
	srv := httptest.NewServer(reportshttp.NewRouter(&reportshttp.Server{Reader: reader, Ping: reader, Now: func() time.Time { return e2eAt(23, 0) }}, quiet, "e2e"))
	t.Cleanup(srv.Close)
	const span = "?from=2026-11-03&to=2026-11-04"
	days := func(path string) []map[string]any {
		var out struct {
			From string           `json:"from"`
			To   string           `json:"to"`
			Days []map[string]any `json:"days"`
		}
		getJSON(t, srv.URL+path+span, &out)
		if out.From != "2026-11-03" || out.To != "2026-11-04" {
			t.Fatalf("%s range = %s..%s", path, out.From, out.To)
		}
		return out.Days
	}

	// Stops: A+B share stop 1, C is stop 2 -> 2 stops, 3 packages; the wave declared 250/stop.
	wantRoutes := []map[string]any{{"day": "2026-11-03", "stationNodeId": e2eStation, "routesCompleted": 1.0, "stopsPerRoute": 2.0,
		"packagesPerStop": 1.5, "declaredCostPerStopCents": 250.0, "costPerPackageCents": 250.0 * 2 / 3}}
	wantAttempts := []map[string]any{{"day": "2026-11-03", "stationNodeId": e2eStation, "attempts": 3.0, "deliveredAttempts": 2.0,
		"failedAttempts": 1.0, "packagesAttempted": 3.0, "packagesWithFirstAttemptSuccess": 2.0, "firstAttemptSuccessRate": 2.0 / 3,
		"meanAttemptsPerPackage": 1.0, "returnsInitiated": 0.0}}
	// W1 (capacity 4) holds the two stops; W2 (capacity 5) was cancelled empty.
	wantWaves := []map[string]any{{"day": "2026-11-03", "stationNodeId": e2eStation, "wavesScheduled": 2.0, "wavesCancelled": 1.0,
		"stopCapacity": 9.0, "stopsInducted": 2.0, "utilisation": 2.0 / 9}}
	// The route started at 11:00, before the wave window (12:00 to 20:00) opened: two stops spread over
	// it get 13:00-15:00 and 17:00-19:00 (ADR 0013). A (13:00, the first bound) and B (13:10) were
	// delivered inside stop 1's window; C failed, and a failed attempt never counts.
	wantEta := []map[string]any{{"day": "2026-11-03", "stationNodeId": e2eStation, "deliveriesWithEta": 2.0, "deliveriesInsideEta": 2.0,
		"deliveriesBeforeEta": 0.0, "deliveriesAfterEta": 0.0, "etaHitRate": 1.0}}
	waitForReports(t, days, map[string][]map[string]any{
		"/reports/route-efficiency": wantRoutes, "/reports/attempt-outcomes": wantAttempts, "/reports/wave-utilisation": wantWaves,
		"/reports/eta-window": wantEta,
	})
	var fresh struct {
		AsOf       *time.Time `json:"asOf"`
		LagSeconds *float64   `json:"lagSeconds"`
	}
	getJSON(t, srv.URL+"/reports/freshness", &fresh)
	if fresh.AsOf == nil || fresh.LagSeconds == nil || *fresh.LagSeconds < 0 {
		t.Errorf("freshness = %+v, want an asOf and a non-negative lag", fresh)
	}
	assertOnlyThePoisonWasDeadLettered(t, brokers, dlqTopic, poison)

	// ---- a redelivered DeliveryAttempted (same id) changes nothing; a later marker proves it was consumed ----
	var dup []byte
	if err := oltp.QueryRow(ctx, `SELECT value FROM outbox_events WHERE topic = $1 AND event_type LIKE '%DeliveryAttempted' LIMIT 1`,
		eventsTopic).Scan(&dup); err != nil {
		t.Fatal(err)
	}
	marker := hand(t, "evt-marker", cloudevents.Type("wave", "DeliveryWaveScheduled"), "W-MARK", e2eAt(22, 0),
		map[string]any{"waveId": "W-MARK", "stationNodeId": "node-ds-marker", "serviceDate": "2026-11-04", "stopCapacity": 1,
			"inductCutoffAt": e2eAt(22, 0), "windowStart": e2eAt(22, 0), "windowEnd": e2eAt(23, 0)})
	kafkatest.Produce(t, eventsTopic, kafkatest.Message{Key: "dup", Value: dup}, kafkatest.Message{Key: "W-MARK", Value: marker})
	waitFor(t, 60*time.Second, "the marker projected", func() bool { return len(days("/reports/wave-utilisation")) == 2 })
	if got := days("/reports/attempt-outcomes"); !reflect.DeepEqual(got, wantAttempts) {
		t.Errorf("after a redelivered DeliveryAttempted attempt-outcomes = %v\nwant still %v (idempotent on the CloudEvents id)", got, wantAttempts)
	}
	pgtest.WaitAllPublished(t, oltp)
}

// waitForReports polls every report until each equals its want, failing with
// the last answers on timeout.
func waitForReports(t *testing.T, days func(string) []map[string]any, want map[string][]map[string]any) {
	t.Helper()
	deadline := time.Now().Add(90 * time.Second)
	for {
		got := map[string][]map[string]any{}
		for path := range want {
			got[path] = days(path)
		}
		if reflect.DeepEqual(got, want) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for the projected day:\ngot  %v\nwant %v", got, want)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// assertOnlyThePoisonWasDeadLettered reads the DLQ: exactly the poison bytes,
// nothing for the legacy or unknown-type messages.
func assertOnlyThePoisonWasDeadLettered(t *testing.T, brokers []string, dlqTopic string, poison []byte) {
	t.Helper()
	dlq := kafkago.NewReader(kafkago.ReaderConfig{Brokers: brokers, Topic: dlqTopic, Partition: 0, MinBytes: 1, MaxBytes: 10e6})
	defer func() { _ = dlq.Close() }()
	readCtx, stop := context.WithTimeout(context.Background(), 60*time.Second)
	defer stop()
	m, err := dlq.ReadMessage(readCtx)
	if err != nil {
		t.Fatalf("nothing reached the DLQ: %v", err)
	}
	if !bytes.Equal(m.Value, poison) {
		t.Errorf("DLQ value differs from the poison message:\n%s", m.Value)
	}
	quietCtx, quietStop := context.WithTimeout(context.Background(), 3*time.Second)
	defer quietStop()
	if extra, err := dlq.ReadMessage(quietCtx); err == nil {
		t.Errorf("a second message reached the DLQ (legacy and unknown types are skipped, not dead-lettered): %s", extra.Value)
	}
}

func hand(t *testing.T, id, typ, subject string, at time.Time, data map[string]any) []byte {
	t.Helper()
	b, err := json.Marshal(map[string]any{
		"specversion": "1.0", "id": id, "source": "/retailnetwork/last-mile-delivery", "type": typ, "subject": subject,
		"datacontenttype": "application/json", "time": at.Format(time.RFC3339), "data": data,
	})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func getJSON(t *testing.T, url string, into any) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s = %d %s", url, resp.StatusCode, body)
	}
	if err := json.Unmarshal(body, into); err != nil {
		t.Fatalf("decode %s: %v", body, err)
	}
}

func waitFor(t *testing.T, timeout time.Duration, what string, cond func() bool) {
	t.Helper()
	for deadline := time.Now().Add(timeout); ; time.Sleep(200 * time.Millisecond) {
		if cond() {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for: %s", what)
		}
	}
}
