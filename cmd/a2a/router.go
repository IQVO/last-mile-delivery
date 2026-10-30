package main

import (
	"log/slog"
	"net/http"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/riandyrn/otelchi"
	otelchimetric "github.com/riandyrn/otelchi/metric"

	inbounda2a "github.com/claudioed/last-mile-delivery/internal/adapters/inbound/a2a"
	"github.com/claudioed/last-mile-delivery/internal/adapters/inbound/requestlog"
)

// newAgent composes the A2A surface over the read use cases: the SAME ones the
// REST and MCP adapters use (ListWaves and GetRoute), the Agent Card
// advertising agentURL, and the HTTP router around them. The adapter never
// writes, so no EventOutbox, UnitOfWork, Clock or EventPublisher is wired, and
// it calls no other context.
func newAgent(deps inbounda2a.Deps, agentURL, version string, logger *slog.Logger, serviceName string) http.Handler {
	card := inbounda2a.Card(inbounda2a.Config{AgentURL: agentURL, Version: version})
	return newRouter(inbounda2a.Handler(inbounda2a.NewExecutor(deps)), card, logger, serviceName)
}

// newRouter wraps the A2A handler in the process's HTTP surface:
//
//   - GET /healthz  -> 200 {"status":"ok"} (open; liveness/readiness probes).
//   - GET /.well-known/agent-card.json -> the Agent Card (the discovery
//     contract, docs/adr/0009).
//   - the JSON-RPC endpoint at BOTH "/" (the canonical root) and "/a2a" (the
//     fleet's <CTX>_A2A_ENDPOINT convention, mirroring cmd/mcp's dual "/mcp"
//     mount).
//
// The middleware order is the fleet observability standard, identical to the
// REST and MCP routers: RequestID, otelchi (server span named after the route
// pattern), the request-duration histogram, RequestLogger, Recoverer.
// telemetry.Setup must have run before this call (otelchimetric binds the
// global MeterProvider at construction).
//
// No auth middleware is ever added here: A2A is unauthenticated by the
// fleet-wide decision.
func newRouter(a2aHandler http.Handler, card *a2a.AgentCard, logger *slog.Logger, serviceName string) http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(otelchi.Middleware(serviceName, otelchi.WithChiRoutes(r)))
	r.Use(otelchimetric.NewServerRequestDuration(otelchimetric.NewBaseConfig(serviceName)))
	r.Use(requestlog.RequestLogger(logger))
	r.Use(middleware.Recoverer)
	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})
	r.Handle(a2asrv.WellKnownAgentCardPath, a2asrv.NewStaticAgentCardHandler(card))
	r.Mount("/a2a", a2aHandler)
	r.Mount("/", a2aHandler)
	return r
}
