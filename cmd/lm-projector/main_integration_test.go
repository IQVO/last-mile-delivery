//go:build integration

package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"testing"
	"time"

	inboundkafka "github.com/claudioed/last-mile-delivery/internal/adapters/inbound/kafka"
	"github.com/claudioed/last-mile-delivery/internal/adapters/outbound/analyticsstore"
	outboundkafka "github.com/claudioed/last-mile-delivery/internal/adapters/outbound/kafka"
	"github.com/claudioed/last-mile-delivery/internal/domain/history"
	"github.com/claudioed/last-mile-delivery/internal/domain/route"
	"github.com/claudioed/last-mile-delivery/internal/domain/shared"
	"github.com/claudioed/last-mile-delivery/internal/domain/wave"
	"github.com/claudioed/last-mile-delivery/internal/testing/kafkatest"
	"github.com/claudioed/last-mile-delivery/internal/testing/pgtest"
)

func TestMain(m *testing.M) {
	code := m.Run()
	kafkatest.Shutdown()
	pgtest.Shutdown()
	os.Exit(code)
}

func freeAddr(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()
	return l.Addr().String()
}

func waitUntil(t *testing.T, what string, cond func() bool) {
	t.Helper()
	for deadline := time.Now().Add(90 * time.Second); time.Now().Before(deadline); time.Sleep(200 * time.Millisecond) {
		if cond() {
			return
		}
	}
	t.Fatalf("timed out waiting for %s", what)
}

// The real entry point: env -> embedded analytical migrations on an EMPTY
// database -> /readyz -> the consumer projects the events the real encoder
// wrote to BOTH own topics -> a cancelled context drains to a clean exit.
func TestProjectorBootsMigratesProjectsAndStopsCleanly(t *testing.T) {
	url := pgtest.EmptyURL(t)
	brokers := kafkatest.Brokers(t)
	for _, topic := range inboundkafka.OwnTopics() {
		kafkatest.CreateTopic(t, topic)
	}
	at := shared.Header{Version: 3, At: time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)}
	msgs, err := outboundkafka.NewEncoder().Encode(
		history.PackageReturnInitiated{Header: at, TrackingNumber: "TN-PROJECTOR-1"},
		route.RoutePlanned{Header: at, RouteID: "RT-PROJECTOR-1", WaveID: wave.ID("WV-1"), DSPID: "DSP-1", StopCount: 4},
	)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range msgs {
		kafkatest.Produce(t, m.Topic, kafkatest.Message{Key: string(m.Key), Value: m.Value})
	}

	addr := freeAddr(t)
	env := map[string]string{"ANALYTICS_DATABASE_URL": url, "KAFKA_BROKERS": brokers[0], "ADMIN_ADDR": addr, "LOG_LEVEL": "error",
		"ANALYTICS_CONSUMER_GROUP": fmt.Sprintf("lm-projector-itest-%d", time.Now().UnixNano())}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- run(ctx, func(k string) string { return env[k] }) }()
	t.Cleanup(cancel)

	waitUntil(t, "/readyz 200", func() bool {
		resp, err := http.Get("http://" + addr + "/readyz")
		if err != nil {
			return false
		}
		_ = resp.Body.Close()
		return resp.StatusCode == http.StatusOK
	})
	pool, err := analyticsstore.NewPool(context.Background(), url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	waitUntil(t, "the PackageReturnInitiated (events topic) and the RoutePlanned (analytics topic) projected", func() bool {
		var n int
		err := pool.QueryRow(context.Background(), `SELECT (SELECT count(*) FROM package_returns WHERE tracking_number = 'TN-PROJECTOR-1')
			+ (SELECT count(*) FROM route_plans WHERE route_id = 'RT-PROJECTOR-1' AND stop_count = 4)`).Scan(&n)
		return err == nil && n == 2
	})
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("a cancelled projector must drain cleanly, got %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("the projector did not stop")
	}
}
