// Command a2a is the composition root for the last-mile-delivery A2A agent
// (docs/adr/0009-a2a-inbound-adapter.md): it wires env config to the outbound
// Postgres repositories, the repositories to the SAME read use cases the REST
// and MCP adapters use (ListWaves, GetRoute), and those to the inbound A2A
// adapter, then serves the Agent2Agent protocol over JSON-RPC (and nothing
// else). It is a third, independent deployable beside cmd/api and cmd/mcp.
//
// The surface is READ-ONLY, like cmd/mcp (docs/adr/0006): no write use case is
// wired here at all, so this binary can never insert into the outbox. It never
// starts the outbox relay, never runs the local-copy consumers and never dials
// Kafka (no KAFKA_BROKERS, no consumer groups): those run in cmd/api only. It
// makes no REST, MCP or A2A call to any sibling context.
//
// There is NO auth of any kind (fleet-wide revert 2026-09-11). Access control
// is the in-cluster ClusterIP boundary.
//
// Environment:
//
//	A2A_ADDR                 listen address (default :8093)
//	A2A_AGENT_URL            REQUIRED. Absolute http(s) URL the Agent Card advertises for the JSON-RPC endpoint
//	DATABASE_URL             REQUIRED. Postgres DSN (the service database cmd/api owns)
//	MIGRATIONS_DATABASE_URL  direct DSN for the boot migrations (default DATABASE_URL)
//	SERVICE_VERSION          version reported on the Agent Card and service.version (default 1.0.0 for the card, dev for telemetry)
//	ENVIRONMENT              deployment.environment.name, logged at boot too (default unset, local in telemetry)
//	LOG_LEVEL                debug | info | warn | error (default info)
//	OTEL_SERVICE_NAME        service.name of spans and metrics (default last-mile-delivery-a2a)
//	OTEL_EXPORTER_OTLP_ENDPOINT  Collector OTLP/gRPC endpoint (default telemetry.DefaultEndpoint); export never blocks
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	inbounda2a "github.com/claudioed/last-mile-delivery/internal/adapters/inbound/a2a"
	"github.com/claudioed/last-mile-delivery/internal/adapters/outbound/postgres"
	"github.com/claudioed/last-mile-delivery/internal/adapters/outbound/telemetry"
	"github.com/claudioed/last-mile-delivery/internal/application/usecases"
	"github.com/claudioed/last-mile-delivery/internal/bootretry"
)

// shutdownTimeout bounds the HTTP server's graceful drain.
const shutdownTimeout = 10 * time.Second

// errNoDatabase is the boot error of a missing DATABASE_URL: this binary has
// no in-memory mode.
var errNoDatabase = errors.New("DATABASE_URL is required: this context runs on real Postgres")

// errNoAgentURL is the boot error of a missing A2A_AGENT_URL: the Agent Card
// must advertise an address the delegating agents can dial, and there is no
// safe default for it.
var errNoAgentURL = errors.New("A2A_AGENT_URL is required: the Agent Card must advertise the absolute http(s) URL clients dial for the JSON-RPC endpoint")

// bootConfig is the mandatory configuration, validated before telemetry, any
// connection and the listener.
type bootConfig struct {
	databaseURL string
	agentURL    string
}

// loadBootConfig reads and validates the mandatory environment. Every problem
// is reported at once (errors.Join) so an operator fixes the environment in
// one pass.
func loadBootConfig() (bootConfig, error) {
	cfg := bootConfig{databaseURL: os.Getenv("DATABASE_URL"), agentURL: strings.TrimSpace(os.Getenv("A2A_AGENT_URL"))}
	var errs []error
	if cfg.databaseURL == "" {
		errs = append(errs, errNoDatabase)
	}
	if err := checkAgentURL(cfg.agentURL); err != nil {
		errs = append(errs, err)
	}
	if len(errs) > 0 {
		return bootConfig{}, errors.Join(errs...)
	}
	return cfg, nil
}

// checkAgentURL requires A2A_AGENT_URL to be present and an absolute http(s)
// URL with a host.
func checkAgentURL(raw string) error {
	if raw == "" {
		return errNoAgentURL
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("A2A_AGENT_URL=%q is not an absolute http(s) URL (e.g. http://last-mile-delivery-a2a.warehouse-systems.svc.cluster.local:8093/)", raw)
	}
	return nil
}

func main() {
	if err := run(); err != nil {
		slog.Error("a2a server exited with error", "error", err)
		os.Exit(1)
	}
}

func run() error {
	// A bad configuration fails fast: before telemetry, any connection and
	// the listener.
	cfg, err := loadBootConfig()
	if err != nil {
		return err
	}
	logger := newLogger(getenv("LOG_LEVEL", "info"))
	slog.SetDefault(logger)

	// Telemetry comes up before the router is built (otelchimetric binds the
	// global MeterProvider at construction) and is flushed last.
	tel := loadTelemetryEnv(os.Getenv)
	flushTelemetry, err := startTelemetry(logger, tel)
	if err != nil {
		return err
	}
	defer flushTelemetry()

	deps, closeAdapters, err := buildDeps(context.Background(), logger, cfg.databaseURL, getenv("MIGRATIONS_DATABASE_URL", cfg.databaseURL))
	if err != nil {
		return err
	}
	defer closeAdapters()

	srv := &http.Server{
		Addr:              getenv("A2A_ADDR", ":8093"),
		Handler:           newAgent(deps, cfg.agentURL, getenv("SERVICE_VERSION", "1.0.0"), logger, tel.serviceName),
		ReadHeaderTimeout: 5 * time.Second,
	}
	logger.Info("a2a server starting", "environment", os.Getenv("ENVIRONMENT"), "agent_url", cfg.agentURL)
	return serveA2A(logger, srv)
}

// buildDeps migrates the database, connects a pgx pool and wires the two
// read use cases over the Postgres repositories. The returned close func
// releases the pool. An empty databaseURL is a boot error: there is no
// fallback runtime.
//
// Like cmd/api this binary runs the idempotent embedded migrations on start,
// so it can boot against a fresh database before cmd/api has; golang-migrate's
// advisory lock makes concurrent starts safe. migrationsDatabaseURL is used
// ONLY for that step (a DIRECT Postgres DSN where DATABASE_URL points at
// PgBouncer, which cannot honour the session-scoped advisory lock); the
// runtime pool always uses databaseURL.
func buildDeps(ctx context.Context, logger *slog.Logger, databaseURL, migrationsDatabaseURL string) (inbounda2a.Deps, func(), error) {
	noop := func() {}
	if databaseURL == "" {
		return inbounda2a.Deps{}, noop, errNoDatabase
	}
	// The first outbound dial of an injected pod can be reset (Istio native
	// sidecars), so migrations and the first ping retry with backoff; on
	// exhaustion the process refuses to boot.
	if err := bootretry.Retry(ctx, logger, "run migrations", func() error {
		return postgres.RunMigrations(migrationsDatabaseURL)
	}); err != nil {
		return inbounda2a.Deps{}, noop, err
	}
	pool, err := postgres.NewPool(ctx, databaseURL)
	if err != nil {
		return inbounda2a.Deps{}, noop, err
	}
	if err := bootretry.Retry(ctx, logger, "ping postgres", func() error { return pool.Ping(ctx) }); err != nil {
		pool.Close()
		return inbounda2a.Deps{}, noop, err
	}
	logger.Info("postgres repositories configured")
	d := usecases.Deps{
		Waves:        postgres.NewWaveRepository(pool),
		Routes:       postgres.NewRouteRepository(pool),
		PackageStops: postgres.NewPackageStopRepository(pool),
	}
	return inbounda2a.Deps{
		ListWaves: usecases.NewListWaves(d),
		GetRoute:  usecases.NewGetRoute(d),
	}, pool.Close, nil
}

// serveA2A runs srv until it fails or SIGINT/SIGTERM arrives, then drains it
// gracefully.
func serveA2A(logger *slog.Logger, srv *http.Server) error {
	serverErr := make(chan error, 1)
	go func() {
		logger.Info("a2a server listening (JSON-RPC)", "addr", srv.Addr)
		serverErr <- srv.ListenAndServe()
	}()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	select {
	case err := <-serverErr:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	case <-ctx.Done():
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	return srv.Shutdown(shutdownCtx)
}

// newLogger builds the process-wide JSON logger. LOG_LEVEL maps
// debug|info|warn|warning|error (case-insensitive; "warning" is accepted
// like "warn" fleet-wide), defaulting to Info.
func newLogger(level string) *slog.Logger {
	var lvl slog.Level
	switch strings.ToLower(level) {
	case "debug":
		lvl = slog.LevelDebug
	case "warn", "warning":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	default:
		lvl = slog.LevelInfo
	}
	return slog.New(telemetry.WithTraceContext(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: lvl})))
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
