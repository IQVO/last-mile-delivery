//go:build integration

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	inboundkafka "github.com/claudioed/last-mile-delivery/internal/adapters/inbound/kafka"
	outboundkafka "github.com/claudioed/last-mile-delivery/internal/adapters/outbound/kafka"
	"github.com/claudioed/last-mile-delivery/internal/adapters/outbound/postgres"
	"github.com/claudioed/last-mile-delivery/internal/testing/kafkatest"
	"github.com/claudioed/last-mile-delivery/internal/testing/pgtest"
)

func freeAddr(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().String()
}

func getJSON(t *testing.T, url string) (int, map[string]any) {
	t.Helper()
	res, err := http.Get(url)
	if err != nil {
		return 0, nil
	}
	defer res.Body.Close()
	var body map[string]any
	_ = json.NewDecoder(res.Body).Decode(&body)
	return res.StatusCode, body
}

// TestRunBootsTheRealServiceAndReportsADeadBroker boots run(ctx) with its real
// wiring against testcontainers Postgres and Kafka and drives it only through
// REST and Kafka. It asserts the Postgres rows and the exact CloudEvents on the
// output topic, then stops Kafka: /readyz must answer 503 dependency-unavailable
// while /healthz stays 200 and writes are still accepted, and the service must
// then exit naming the dead worker. It runs last in the binary
// because it kills the broker.
func TestRunBootsTheRealServiceAndReportsADeadBroker(t *testing.T) {
	brokers := kafkatest.Brokers(t)
	for _, topic := range []string{inboundkafka.LinehaulTopic, inboundkafka.TopologyTopic, inboundkafka.ShippingTopic, outboundkafka.EventsTopic, outboundkafka.AnalyticsTopic} {
		kafkatest.CreateTopic(t, topic)
	}
	dbURL := pgtest.URL(t)
	addr := freeAddr(t)
	t.Setenv("DATABASE_URL", dbURL)
	t.Setenv("KAFKA_BROKERS", strings.Join(brokers, ","))
	t.Setenv("HTTP_ADDR", addr)
	t.Setenv("SHUTDOWN_DRAIN_DELAY", "0s")
	t.Setenv("OUTBOX_RELAY_INTERVAL", "50ms")
	t.Setenv("LOG_LEVEL", "warn")
	for _, env := range []string{linehaulGroupEnv, topologyGroupEnv, shippingGroupEnv} {
		t.Setenv(env, "g-"+uuid.NewString())
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- run(ctx) }()
	t.Cleanup(func() { cancel() })
	base := "http://" + addr
	eventually(t, "/healthz", func() bool { code, _ := getJSON(t, base+"/healthz"); return code == 200 })
	eventually(t, "/readyz to be ready with Postgres and Kafka up", func() bool { code, _ := getJSON(t, base+"/readyz"); return code == 200 })

	kafkatest.Produce(t, inboundkafka.TopologyTopic, ceEnvelope(inboundkafka.TypeNodeRegistered, `{"nodeId":"node-ds-01","nodeType":"DELIVERY_STATION"}`))
	c := &client{t: t, url: base}
	var waveID string
	eventually(t, "the station to be known", func() bool {
		code, body := c.call("POST", "/waves", `{"stationNodeId":"node-ds-01","serviceDate":"2026-10-24","inductCutoffAt":"2026-10-24T08:00:00Z","windowStart":"2026-10-24T12:00:00Z","windowEnd":"2026-10-24T20:00:00Z","stopCapacity":3}`)
		waveID, _ = body["waveId"].(string)
		return code == http.StatusCreated
	})

	// Postgres holds the wave and its outbox row; the relay publishes it.
	pool, err := postgres.NewPool(ctx, dbURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	var state string
	var version int
	if err := pool.QueryRow(ctx, `SELECT state, version FROM delivery_waves WHERE wave_id = $1`, waveID).Scan(&state, &version); err != nil || state != "OPEN" || version != 1 {
		t.Fatalf("wave row = %q v%d, %v", state, version, err)
	}
	pgtest.WaitAllPublished(t, pool)
	var eventID string
	if err := pool.QueryRow(ctx, `SELECT event_id FROM outbox_events WHERE event_type LIKE '%DeliveryWaveScheduled'`).Scan(&eventID); err != nil {
		t.Fatalf("outbox row: %v", err)
	}

	got := kafkatest.ReadN(t, outboundkafka.EventsTopic, 1, 30*time.Second)[0]
	want := fmt.Sprintf(`{"specversion":"1.0","id":%q,"source":"/retailnetwork/last-mile-delivery","type":"com.retailnetwork.logistics.last-mile-delivery.wave.DeliveryWaveScheduled","subject":%q,"datacontenttype":"application/json","dataschema":"urn:retailnetwork:last-mile-delivery:events:DeliveryWaveScheduled:v1","time":%q,"data":{"waveId":%q,"stationNodeId":"node-ds-01","serviceDate":"2026-10-24","inductCutoffAt":"2026-10-24T08:00:00Z","windowStart":"2026-10-24T12:00:00Z","windowEnd":"2026-10-24T20:00:00Z","stopCapacity":3}}`,
		eventID, waveID, timeOf(t, got.Value), waveID)
	if string(got.Value) != want || got.Key != waveID {
		t.Fatalf("event on the topic\n got  %s (key %s)\n want %s (key %s)", got.Value, got.Key, want, waveID)
	}

	// Kill Kafka. While the process is still up, /readyz must turn 503
	// dependency-unavailable naming only kafka, /healthz must stay 200 (liveness
	// never asks a dependency) and a write is still accepted (the outbox holds
	// it). Then the consumers and the relay lose their broker and the service
	// EXITS naming the dead worker, so the orchestrator restarts it instead of
	// leaving a pod alive whose consumers will never come back.
	kafkatest.Stop(t)
	eventually(t, "/readyz 503 dependency-unavailable with Kafka stopped", func() bool {
		code, body := getJSON(t, base+"/readyz")
		return code == http.StatusServiceUnavailable && body["type"] == "dependency-unavailable" && body["detail"] == "not ready: kafka"
	})
	if code, _ := getJSON(t, base+"/healthz"); code != http.StatusOK {
		t.Fatalf("/healthz with Kafka stopped = %d, want 200", code)
	}
	if code, body := c.call("POST", "/waves", `{"stationNodeId":"node-ds-01","serviceDate":"2026-10-25","inductCutoffAt":"2026-10-25T08:00:00Z","windowStart":"2026-10-25T12:00:00Z","windowEnd":"2026-10-25T20:00:00Z","stopCapacity":3}`); code != http.StatusCreated {
		t.Fatalf("a write with Kafka stopped = %d %v, want 201 (the outbox holds the event)", code, body)
	}
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "stopped unexpectedly") {
			t.Fatalf("run returned %v after Kafka went away, want a 'stopped unexpectedly' error", err)
		}
	case <-time.After(120 * time.Second):
		t.Fatal("run did not exit after Kafka went away")
	}
}

func timeOf(t *testing.T, value []byte) string {
	t.Helper()
	var env struct {
		Time string `json:"time"`
	}
	if err := json.Unmarshal(value, &env); err != nil || env.Time == "" {
		t.Fatalf("no time in %s: %v", value, err)
	}
	return env.Time
}
