// Command api is the last-mile-delivery service: the REST API, the three
// consumers and the always-on outbox relay in one process.
//
// Observability environment (docs/adr/0008): OTEL_SERVICE_NAME (default
// last-mile-delivery), OTEL_EXPORTER_OTLP_ENDPOINT (default
// telemetry.DefaultEndpoint; export never blocks), SERVICE_VERSION (default
// dev), ENVIRONMENT (default local) and LOG_LEVEL (debug|info|warn|error,
// default info).
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	inboundhttp "github.com/claudioed/last-mile-delivery/internal/adapters/inbound/http"
	outboundkafka "github.com/claudioed/last-mile-delivery/internal/adapters/outbound/kafka"
	outboxrelay "github.com/claudioed/last-mile-delivery/internal/adapters/outbound/outbox"
	"github.com/claudioed/last-mile-delivery/internal/adapters/outbound/telemetry"
)

const (
	shutdownTimeout           = 10 * time.Second
	workerDrainTimeout        = 10 * time.Second
	defaultShutdownDrainDelay = 5 * time.Second
)

func main() {
	if err := run(context.Background()); err != nil {
		slog.Error("service exited with error", "error", err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	logger := newLogger(getenv("LOG_LEVEL", "info"))
	slog.SetDefault(logger)

	rt, err := loadRuntime(os.Getenv)
	if err != nil {
		return err
	}
	// Telemetry comes up before the router is built (otelchimetric binds the
	// global MeterProvider at construction) and is flushed LAST: deferred before
	// everything below, so the final spans of the drain still leave the process.
	tel := loadTelemetryEnv(os.Getenv)
	flushTelemetry, err := startTelemetry(logger, tel)
	if err != nil {
		return err
	}
	defer flushTelemetry()

	ad, closeAdapters, err := buildAdapters(ctx, rt.databaseURL, logger)
	if err != nil {
		return err
	}
	defer closeAdapters()

	// The business counter binds to the MeterProvider telemetry.Setup installed.
	attemptMetrics, err := telemetry.NewAttemptMetrics()
	if err != nil {
		return err
	}
	ad.deps.Metrics = attemptMetrics

	readiness := newReadiness(ad.pool.Ping, rt.brokers)
	httpServer := &http.Server{
		Addr:              getenv("HTTP_ADDR", ":8080"),
		Handler:           inboundhttp.NewRouter(buildServer(ad, readiness), logger, tel.serviceName),
		ReadHeaderTimeout: 5 * time.Second,
	}
	consumers, closeConsumers := startConsumers(ad, rt, logger)
	relay, closeRelay := startOutboxRelay(ad.outboxStore, rt.brokers, logger)
	return serveUntilSignal(ctx, logger, httpServer, readiness, consumers, closeConsumers, relay, closeRelay)
}

// worker is a started background loop and the channel closed once it returned.
type worker struct {
	name string
	done chan struct{}
}

func startConsumers(ad adapters, rt runtimeEnv, logger *slog.Logger) ([]*worker, func()) {
	ctx, stop := context.WithCancel(context.Background())
	var (
		workers []*worker
		closers []func() error
	)
	for _, spec := range consumerSpecs(ad) {
		group := rt.groups[spec.groupEnv]
		consumer := spec.build(rt.brokers, group, logger)
		w := &worker{name: spec.name + " consumer", done: make(chan struct{})}
		workers = append(workers, w)
		closers = append(closers, consumer.Close)
		go func() {
			defer close(w.done)
			logger.Info("consumer running", "consumer", consumer.Name, "topic", consumer.Topic, "group_id", group, "brokers", rt.brokers)
			if err := consumer.Run(ctx); !errors.Is(err, context.Canceled) {
				logger.Error("consumer stopped", "consumer", consumer.Name, "error", err)
			}
		}()
	}
	return workers, func() {
		stop()
		for _, closeFn := range closers {
			_ = closeFn()
		}
	}
}

// startOutboxRelay starts the always-on relay over the Kafka sink. Kafka is
// dialled lazily on the first send, never here.
func startOutboxRelay(store outboxrelay.Store, brokers []string, logger *slog.Logger) (*worker, func()) {
	sink := outboundkafka.NewRelaySink(brokers)
	interval := parseRelayInterval(os.Getenv("OUTBOX_RELAY_INTERVAL"), logger)
	maxAttempts := outboxMaxAttempts(os.Getenv("OUTBOX_MAX_ATTEMPTS"), logger)
	relay := outboxrelay.NewRelay(store, sink, logger, outboxrelay.WithInterval(interval), outboxrelay.WithMaxAttempts(maxAttempts))

	ctx, stop := context.WithCancel(context.Background())
	w := &worker{name: "outbox-relay", done: make(chan struct{})}
	go func() {
		defer close(w.done)
		defer func() {
			if err := sink.Close(); err != nil {
				logger.Error("kafka relay sink close failed", "error", err)
			}
		}()
		logger.Info("outbox relay running", "interval", interval, "max_attempts", maxAttempts, "topic", outboundkafka.EventsTopic)
		if err := relay.Run(ctx); !errors.Is(err, context.Canceled) {
			logger.Error("outbox relay stopped", "error", err)
		}
	}()
	return w, stop
}

// serveUntilSignal serves HTTP until a signal or a worker dies. On a signal it
// flips /readyz to not-ready, waits for traffic to drain, shuts the server down
// and then stops the consumers and the relay. A worker that dies makes the
// process exit so the orchestrator restarts it.
func serveUntilSignal(ctx context.Context, logger *slog.Logger, httpServer *http.Server, readiness *inboundhttp.Readiness,
	consumers []*worker, closeConsumers func(), relay *worker, closeRelay func(),
) error {
	errCh := make(chan error, 1)
	go func() {
		logger.Info("http server listening", "addr", httpServer.Addr)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	sigCtx, stop := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	died := watchWorkers(append([]*worker{relay}, consumers...)...)
	select {
	case err := <-errCh:
		closeConsumers()
		closeRelay()
		return err
	case name := <-died:
		closeConsumers()
		closeRelay()
		return fmt.Errorf("%s stopped unexpectedly: exiting so the orchestrator restarts the service", name)
	case <-sigCtx.Done():
	}

	readiness.SetNotReady()
	if delay := shutdownDrainDelay(logger); delay > 0 {
		logger.Info("shutdown: readiness flipped to not-ready; waiting for traffic to drain", "drain_delay", delay.String())
		time.Sleep(delay)
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	err := httpServer.Shutdown(shutdownCtx)

	closeConsumers()
	for _, w := range consumers {
		await(logger, w)
	}
	closeRelay()
	await(logger, relay)
	return err
}

func watchWorkers(workers ...*worker) <-chan string {
	died := make(chan string, len(workers))
	for _, w := range workers {
		go func() {
			<-w.done
			died <- w.name
		}()
	}
	return died
}

func await(logger *slog.Logger, w *worker) {
	select {
	case <-w.done:
	case <-time.After(workerDrainTimeout):
		logger.Warn("worker did not stop before the shutdown drain deadline", "worker", w.name)
	}
}
