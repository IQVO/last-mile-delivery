//go:build integration

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	inboundhttp "github.com/claudioed/last-mile-delivery/internal/adapters/inbound/http"
	inboundkafka "github.com/claudioed/last-mile-delivery/internal/adapters/inbound/kafka"
	outboundkafka "github.com/claudioed/last-mile-delivery/internal/adapters/outbound/kafka"
	outboxrelay "github.com/claudioed/last-mile-delivery/internal/adapters/outbound/outbox"
	"github.com/claudioed/last-mile-delivery/internal/adapters/outbound/postgres"
	"github.com/claudioed/last-mile-delivery/internal/application/usecases"
	"github.com/claudioed/last-mile-delivery/internal/testing/kafkatest"
	"github.com/claudioed/last-mile-delivery/internal/testing/pgtest"
)

func TestMain(m *testing.M) {
	code := m.Run()
	kafkatest.Shutdown()
	pgtest.Shutdown()
	os.Exit(code)
}

func ceEnvelope(typ, data string) kafkatest.Message {
	id := uuid.NewString()
	return kafkatest.Message{Key: id, Value: []byte(fmt.Sprintf(
		`{"specversion":"1.0","id":%q,"source":"/retailnetwork/test","type":%q,"subject":"s","datacontenttype":"application/json","time":"2026-10-24T05:00:00Z","data":%s}`, id, typ, data))}
}

type client struct {
	t   *testing.T
	url string
	n   int
}

func (c *client) call(method, path, body string) (int, map[string]any) {
	c.t.Helper()
	req, _ := http.NewRequest(method, c.url+path, strings.NewReader(body))
	if method == http.MethodPost {
		c.n++
		req.Header.Set("Idempotency-Key", fmt.Sprintf("e2e-%d", c.n))
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	var out map[string]any
	_ = json.Unmarshal(b, &out)
	return res.StatusCode, out
}

func eventually(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(60 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// TestADeliveryDayOverRealPostgresAndKafka drives the real adapters end to end:
// consumed events feed the read models, REST commands run the aggregates, the
// outbox relay publishes the CloudEvents, a repeated Idempotency-Key replays.
func TestADeliveryDayOverRealPostgresAndKafka(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	pool, err := postgres.NewPool(ctx, pgtest.URL(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	brokers := kafkatest.Brokers(t)
	eventsTopic, analyticsTopic := kafkatest.Topic(t, "lmd-events"), kafkatest.Topic(t, "lmd-analytics")
	linehaulTopic, topologyTopic := kafkatest.Topic(t, "lmd-linehaul"), kafkatest.Topic(t, "lmd-topology")

	ad := postgresAdapters(pool)
	ad.deps.Outbox = postgres.NewOutbox(pool, &outboundkafka.Encoder{EventsTopic: eventsTopic, AnalyticsTopic: analyticsTopic})
	srv := httptest.NewServer(inboundhttp.NewRouter(buildServer(ad, nil)))
	t.Cleanup(srv.Close)
	c := &client{t: t, url: srv.URL}

	logger := testLogger()
	sink := outboundkafka.NewRelaySink(brokers)
	t.Cleanup(func() { _ = sink.Close() })
	go func() {
		_ = outboxrelay.NewRelay(ad.outboxStore, sink, logger, outboxrelay.WithInterval(50*time.Millisecond)).Run(ctx)
	}()
	for _, cons := range []*inboundkafka.Consumer{
		inboundkafka.NewLinehaulConsumerForTopic(brokers, linehaulTopic, "g-"+uuid.NewString(), usecases.NewInductOnArrival(ad.deps), logger),
		inboundkafka.NewTopologyConsumerForTopic(brokers, topologyTopic, "g-"+uuid.NewString(), usecases.NewApplyNodeRegistered(ad.deps), usecases.NewApplyServiceAreaAssigned(ad.deps), logger),
	} {
		go func() { _ = cons.Run(ctx) }()
		t.Cleanup(func() { _ = cons.Close() })
	}

	// 1. the station is learned from node-topology
	kafkatest.Produce(t, topologyTopic, ceEnvelope(inboundkafka.TypeNodeRegistered, `{"nodeId":"node-ds-01","nodeType":"DELIVERY_STATION","regionId":"region-se","timezone":"America/Sao_Paulo"}`))
	var waveID string
	eventually(t, "the station to be known", func() bool {
		code, body := c.call("POST", "/waves", `{"stationNodeId":"node-ds-01","serviceDate":"2026-10-24","inductCutoffAt":"2026-10-24T08:00:00Z","windowStart":"2026-10-24T12:00:00Z","windowEnd":"2026-10-24T20:00:00Z","stopCapacity":5}`)
		if code == http.StatusCreated {
			waveID, _ = body["waveId"].(string)
		}
		return code == http.StatusCreated
	})
	code, partner := c.call("POST", "/partners", `{"stationNodeId":"node-ds-01","vanCapacity":2}`)
	dsp, _ := partner["dspId"].(string)
	if code != http.StatusCreated || dsp == "" {
		t.Fatalf("partner = %d %v", code, partner)
	}

	// 2. a load arrives: both packages are inducted into the wave
	kafkatest.Produce(t, linehaulTopic, ceEnvelope(inboundkafka.TypeLoadArrived,
		`{"loadId":"load-1","departureId":"dep-1","destinationNodeId":"node-ds-01","nodeType":"DELIVERY_STATION","trackingNumbers":["TRK-1","TRK-2"],"arrivedAt":"2026-10-24T06:00:00Z"}`))
	eventually(t, "both packages to be inducted", func() bool {
		_, body := c.call("GET", "/waves/"+waveID+"/stops", "")
		items, _ := body["items"].([]any)
		return len(items) == 2
	})

	// 3. the day runs through REST
	for _, step := range []struct{ path, body string }{{"/waves/" + waveID + ":close-induction", ""}} {
		if code, b := c.call("POST", step.path, step.body); code != 200 {
			t.Fatalf("%s = %d %v", step.path, code, b)
		}
	}
	code, rt := c.call("POST", "/routes", fmt.Sprintf(`{"waveId":%q,"dspId":%q,"stops":[{"stopNo":1,"trackingNumbers":["TRK-1"]},{"stopNo":2,"trackingNumbers":["TRK-2"]}]}`, waveID, dsp))
	routeID, _ := rt["routeId"].(string)
	if code != http.StatusCreated {
		t.Fatalf("plan = %d %v", code, rt)
	}
	for _, path := range []string{"/waves/" + waveID + ":dispatch", "/routes/" + routeID + ":start"} {
		if code, b := c.call("POST", path, ""); code != 200 {
			t.Fatalf("%s = %d %v", path, code, b)
		}
	}
	for _, tn := range []string{"TRK-1", "TRK-2"} {
		if code, b := c.call("POST", "/routes/"+routeID+"/attempts", fmt.Sprintf(`{"trackingNumber":%q,"outcome":"DELIVERED","at":"2026-10-24T13:00:00Z"}`, tn)); code != 201 {
			t.Fatalf("attempt %s = %d %v", tn, code, b)
		}
	}
	if _, b := c.call("GET", "/routes/"+routeID, ""); b["state"] != "COMPLETED" {
		t.Fatalf("route = %v", b)
	}

	// 4. a repeated key replays and changes nothing
	req, _ := http.NewRequest("POST", srv.URL+"/partners", strings.NewReader(`{"stationNodeId":"node-ds-01","vanCapacity":3}`))
	req.Header.Set("Idempotency-Key", "same-key")
	first, _ := http.DefaultClient.Do(req)
	_ = first.Body.Close()
	req2, _ := http.NewRequest("POST", srv.URL+"/partners", strings.NewReader(`{"stationNodeId":"node-ds-01","vanCapacity":3}`))
	req2.Header.Set("Idempotency-Key", "same-key")
	second, _ := http.DefaultClient.Do(req2)
	_ = second.Body.Close()
	if first.StatusCode != 201 || second.StatusCode != 201 || second.Header.Get("Idempotent-Replayed") != "true" {
		t.Fatalf("replay: %d then %d (replayed %q)", first.StatusCode, second.StatusCode, second.Header.Get("Idempotent-Replayed"))
	}
	var partners int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM delivery_service_partners`).Scan(&partners); err != nil || partners != 2 {
		t.Fatalf("partners = %d, %v (the replay must not create one)", partners, err)
	}

	// 5. the relay publishes every outbox row; check the topics
	pgtest.WaitAllPublished(t, pool)
	var onEvents, onAnalytics int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM outbox_events WHERE topic = $1`, eventsTopic).Scan(&onEvents)
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM outbox_events WHERE topic = $1`, analyticsTopic).Scan(&onAnalytics)
	if onAnalytics != 1 || onEvents < 6 {
		t.Fatalf("outbox rows: events %d analytics %d", onEvents, onAnalytics)
	}
	types := map[string]int{}
	for _, m := range kafkatest.ReadN(t, eventsTopic, onEvents, 30*time.Second) {
		if m.Headers["content-type"] != "application/cloudevents+json; charset=UTF-8" {
			t.Errorf("headers = %v", m.Headers)
		}
		var env struct{ Type, Source, Dataschema string }
		if err := json.Unmarshal(m.Value, &env); err != nil {
			t.Fatal(err)
		}
		types[env.Type[strings.LastIndex(env.Type, ".")+1:]]++
		if env.Source != "/retailnetwork/last-mile-delivery" {
			t.Errorf("source = %s", env.Source)
		}
	}
	for _, want := range []string{"DeliveryWaveScheduled", "DeliveryWaveCapacityChanged", "PackageOutForDelivery", "DeliveryAttempted"} {
		if types[want] == 0 {
			t.Errorf("no %s event on the events topic: %v", want, types)
		}
	}
	if types["PackageOutForDelivery"] != 2 || types["DeliveryAttempted"] != 2 || types["RoutePlanned"] != 0 {
		t.Errorf("counts = %v", types)
	}
	analytics := kafkatest.ReadN(t, analyticsTopic, 1, 30*time.Second)
	if !strings.Contains(string(analytics[0].Value), "RoutePlanned") {
		t.Errorf("analytics = %s", analytics[0].Value)
	}
}
