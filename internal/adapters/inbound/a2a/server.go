// Package a2a is the inbound Agent2Agent adapter: it exposes this bounded
// context to the agent ecosystem as a third driving adapter over the same
// application-layer read use cases the REST and MCP adapters use. It is built
// on the official A2A Go SDK (a2aproject/a2a-go) and speaks JSON-RPC 2.0 over
// HTTP, with the Agent Card served at the well-known path.
//
// Per docs/adr/0009-a2a-inbound-adapter.md, A2A is the DELEGATION channel
// between agents: a client sends a goal-shaped skill request as a structured
// message part and this agent answers from its own state through its own use
// cases. It is not a data-sync channel: facts still travel exclusively as
// CloudEvents on Kafka, published through the transactional outbox
// (docs/adr/0005).
//
// This agent is READ-ONLY, exactly like the MCP adapter (docs/adr/0006): both
// skills read last-mile-delivery data. This package never writes, calls no
// sibling context and contains no outbound A2A client; scheduling waves,
// planning routes and recording attempts stay REST operations, and induction
// stays with the event consumers.
//
// Like the MCP adapter, this package depends inward on the application layer
// and the domain only, never on an outbound adapter. The composition root
// (cmd/a2a) wires the concrete repositories into the use cases. Domain types
// never leak across the message boundary. There is no auth of any kind
// (fleet-wide revert 2026-09-11).
package a2a

import (
	"context"
	"iter"
	"net/http"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv"

	"github.com/claudioed/last-mile-delivery/internal/application/usecases"
)

// Config carries the deployment-specific identity of this agent: the absolute
// URL clients use to reach its JSON-RPC endpoint (advertised on the Agent
// Card) and the card version. It is supplied by the composition root from
// A2A_AGENT_URL; the card MUST advertise a URL the agent clients can actually
// dial (in-cluster: http://<release>-a2a.<ns>.svc.cluster.local:8093/).
type Config struct {
	AgentURL string
	Version  string
}

// Deps is everything the A2A skills need, injected by the composition root.
// It carries the SAME read use cases the REST and MCP adapters use; the
// adapter never constructs an outbound adapter itself, and it holds no write
// use case at all.
type Deps struct {
	// ListWaves is the existing read use case behind GET /waves and the MCP
	// tool list_waves, reused unchanged.
	ListWaves *usecases.ListWaves
	// GetRoute is the existing read use case behind GET /routes/{routeId} and
	// the MCP tool get_route, reused unchanged.
	GetRoute *usecases.GetRoute
}

// Card builds this agent's Agent Card. The surface is two curated, goal-shaped
// READ skills derived from the MCP tools list_waves and get_route
// (docs/adr/0006): this context's MCP charter is entirely read-only, so the
// agent is read-only too and never widens what the context exposes. The card
// declares no security schemes on purpose: this surface is unauthenticated by
// the same fleet-wide decision as REST and MCP; schemes are added here, not in
// a redesign, if that decision is ever reversed.
func Card(cfg Config) *a2a.AgentCard {
	return &a2a.AgentCard{
		Name:        "last-mile-delivery",
		Description: "Last-Mile Delivery agent of the retail network: the read-only source of truth for a delivery station's waves and for the routes its delivery service partners run. It answers which waves a station runs on a service date (state, induct cutoff, delivery window, stop capacity and what is left) and where one route stands (its stops with their derived outcome and every recorded delivery attempt). It never schedules a wave, plans a route or records an attempt, and it does not decide induction or dispatch.",
		Version:     cfg.Version,
		Provider: &a2a.AgentProvider{
			Org: "IQVO",
			URL: "https://github.com/IQVO/last-mile-delivery",
		},
		SupportedInterfaces: []*a2a.AgentInterface{
			a2a.NewAgentInterface(cfg.AgentURL, a2a.TransportProtocolJSONRPC),
		},
		DefaultInputModes:  []string{"application/json", "text/plain"},
		DefaultOutputModes: []string{"application/json", "text/plain"},
		Capabilities:       a2a.AgentCapabilities{Streaming: false},
		Skills: []a2a.AgentSkill{
			{
				ID:          skillListWaves,
				Name:        "List a station's delivery waves for a day",
				Description: "Read the delivery waves of one station on one service date, ordered by inductCutoffAt: state (OPEN, INDUCTION_CLOSED, DISPATCHED, COMPLETED or CANCELLED), the induct cutoff and delivery window, the stop capacity with assignedStops and remainingStops, and cancelReason on a cancelled wave. A station and date with no wave is an empty list. A station id or serviceDate that is not YYYY-MM-DD answers a 422 validation-failed problem. Persists nothing.",
				Tags:        []string{"delivery-wave", "station", "read"},
				InputModes:  []string{"application/json"},
				Examples: []string{
					`{"skill":"list_waves","arguments":{"stationNodeId":"node-ds-01","serviceDate":"2026-10-24"}}`,
				},
			},
			{
				ID:          skillGetRoute,
				Name:        "Look up where one route stands",
				Description: "Read one route of a delivery service partner: its state (PLANNED, OUT_FOR_DELIVERY or COMPLETED), its numbered stops with the trackingNumbers of each and the outcome derived from the per-package attempts (omitted until every package of the stop has one), and every recorded attempt (trackingNumber, DELIVERED or FAILED, failureReason, at). Answers a 404 not-found problem when no route has that id. Persists nothing.",
				Tags:        []string{"route", "delivery-attempt", "read"},
				InputModes:  []string{"application/json"},
				Examples: []string{
					`{"skill":"get_route","arguments":{"routeId":"route-1"}}`,
				},
			},
		},
	}
}

// NewExecutor returns the AgentExecutor that maps an incoming message to a
// skill and answers with a single agent Message (a message-only agent in A2A
// terms): both curated skills are transactional request/response reads, so
// there is no long-running Task lifecycle to track in this first A2A surface.
// The response carries the caller's contextId (and taskId when the client
// referenced one) so a delegating saga can group its interactions.
func NewExecutor(deps Deps) a2asrv.AgentExecutor {
	return a2asrv.AgentExecutorFunc(func(ctx context.Context, ec *a2asrv.ExecutorContext) iter.Seq2[a2a.Event, error] {
		return func(yield func(a2a.Event, error) bool) {
			yield(answer(ctx, deps, ec), nil)
		}
	})
}

// Handler wires the executor into the transport-agnostic A2A request handler
// and wraps it in the JSON-RPC 2.0 HTTP handler, the only transport this
// agent ships (docs/adr/0009), mirroring ADR 0006's "Streamable HTTP only"
// minimalism for MCP.
func Handler(executor a2asrv.AgentExecutor) http.Handler {
	return a2asrv.NewJSONRPCHandler(a2asrv.NewHandler(executor))
}
