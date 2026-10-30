# ADR 0009: A2A inbound adapter (a read-only agent)

## Status

Accepted (2026-10-29).

## Context

The fleet's agents talk to a bounded context over three transports that must
not overlap: Kafka and CloudEvents carry **facts**; MCP carries an agent's
**tools** ([ADR 0006](./0006-mcp-server-adoption.md)); the Agent2Agent (A2A)
protocol carries the **delegation of a goal** from one agent to another, never
data. last-mile-delivery already serves the first two (the outbox and consumers of
[ADR 0005](./0005-runtime-outbox-http-and-readiness.md), the read-only MCP
server of ADR 0006) and had no way for a peer agent to hand it a goal such as
"how is station X doing today" or "where does route R stand".

## Decision

- **A third driving adapter, a separate binary.**
  `internal/adapters/inbound/a2a` is built on the official A2A Go SDK
  (`github.com/a2aproject/a2a-go/v2`, pinned v2.7.0) and served over JSON-RPC 2.0
  by `cmd/a2a`, mounted at `/` and `/a2a`, with the Agent Card at
  `/.well-known/agent-card.json` and an open `/healthz`. It is a third deployable
  beside `cmd/api` and `cmd/mcp`, over the SAME read use cases (one action surface,
  three protocols). The chart gets an `a2a` Deployment and Service (default off,
  port **8093**; 8080 is the api and 8090 the mcp server) plus an opt-in Kong
  HTTPRoute (`/api/a2a/last-mile-delivery` rewritten to `/`, so the Agent Card
  stays reachable under the same prefix).
- **Two curated, goal-shaped READ skills, derived from the MCP tools.**
  `list_waves(stationNodeId, serviceDate)` answers "what does this station run
  on this day" (state, induct cutoff, delivery window, stop capacity with
  assigned and remaining stops) and `get_route(routeId)` answers "where does this
  route stand" (state, stops with their derived outcome, every recorded
  attempt). They bind the `ListWaves` and `GetRoute` use cases and keep the MCP
  tool names, so one name identifies one question across both agent-facing
  surfaces. `get_wave_stops` stays an MCP-only tool: it lists the tracking
  numbers of a wave, which is data a delegating agent does not need to achieve a
  goal, and the budget is two skills, not one per endpoint. Every argument is
  required, like the REST query parameters and path variables.
- **Read-only is structural, and is the write gate.** The MCP charter of ADR 0006
  is entirely read-only (scheduling waves, planning routes, recording attempts
  and the partner lifecycle stay on REST and the event consumers), so the agent
  is read-only: delegation never widens what the context exposes. The adapter's
  `Deps` holds the two read use cases and nothing else; neither the adapter nor
  `cmd/a2a` imports an event sink, the outbox, a unit of work, the inbox, Kafka
  or a consumer. `cmd/a2a` never dials Kafka and never starts the relay or the
  consumers (those stay in `cmd/api`), so it has no `KAFKA_BROKERS` and no
  consumer-group variables, and a skill can never insert an outbox row. A write
  skill would, if ever added, go through the same use case and transactional
  outbox as REST and the fact would still leave as a CloudEvent; that is a
  separate decision and an amendment of this ADR.
- **No sibling calls, no outbound client, no auth.** The agent reads this
  context's own tables only (`AGENTS.md`: calls no sibling), contains no
  outbound A2A client, and has no auth layer (the fleet-wide revert of
  2026-09-11): the Agent Card declares no security schemes and access control is
  the in-cluster ClusterIP boundary. Both are pinned by tests.
- **Message-only agent, strict input, answers in-band.** The executor yields
  exactly one agent `Message` (a Data part with the envelope
  `{skill, data | problem}` and a Text part with a one-line summary) bound to the
  caller's context. The input is `{"skill": "<id>", "arguments": {...}}` as a
  Data part, or a Text part holding the same JSON, decoded with
  `DisallowUnknownFields` and trailing data rejected, so a misspelled argument can
  never be silently dropped. Rejections are ANSWERED as RFC 7807-shaped
  `problem`s in the envelope, never as JSON-RPC errors, with the same slug as
  `type` and the same status as the REST adapter's `problemCatalogue`:
  `not-found` 404 (`ports.ErrNotFound`), `validation-failed` 422
  (`shared.ErrValidation`), `internal-error` 500 (anything else, logged, never
  echoed). The request-shape problems that only exist on this surface are 400:
  `malformed-skill-request`, `unknown-skill`, `invalid-argument` (a missing,
  blank, mistyped or unknown argument). The `detail` text is adapter-owned, so
  the shared errors' own wording never reaches an agent. No shared domain error
  names a deprecated alias of an identifier here, so there is nothing to
  translate.
- **Observability.** One OTel span per skill, `a2a.skill <id>` with the attribute
  `a2a.skill`, beside the router's fleet-standard server span, request log and
  duration histogram ([ADR 0008](./0008-fleet-observability-standard.md)); the
  telemetry service name defaults to `last-mile-delivery-a2a` (chart:
  `<otel.serviceName>-a2a`).
- **Boot.** `DATABASE_URL` and `A2A_AGENT_URL` are mandatory and validated before
  telemetry, any connection and the listener (every problem reported at once);
  `A2A_AGENT_URL` must be an absolute http(s) URL because the Agent Card
  advertises it. The chart defaults it to the release's in-cluster Service DNS
  (`http://<release>-a2a.<ns>.svc.cluster.local:8093/`) and `a2a.agentUrl`
  overrides it. Like `cmd/api` and `cmd/mcp` the binary runs the idempotent
  embedded migrations on start (optional `MIGRATIONS_DATABASE_URL`).
  `A2A_ADDR` (`:8093`), `LOG_LEVEL`, `SERVICE_VERSION` and `ENVIRONMENT` complete
  the contract.

## Consequences

- A peer agent can discover the agent from its card and delegate the two reads
  without REST glue or MCP session handling; the answer is a machine-readable
  envelope plus a one-line summary.
- Adding a skill is a reviewed act: the card, the `Deps` allowlist
  (`TestA2AAgentIsReadOnlyAndHasNoOutboundClient`) and the tests change together.
  Any skill that writes needs its own ADR first.
- `TestA2AAdapterDependencyRule` keeps the adapter additive (application and
  domain only; nothing depends on it); `TestA2AReadOnlySensorBites` proves the
  allowlist detectors flag a write use case, a write seam and an outbound client.
- The binary is a third consumer of the service database, as `cmd/mcp` already
  is; sizing and the connection budget of the pool apply to all three.
- Not in this change: any write skill, a Task lifecycle or streaming, an outbound
  A2A client, authentication, and the warehouse-infra values that turn the
  component and its Kong route on.
