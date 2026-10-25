# ADR 0006: a read-only MCP server

## Status

Accepted (2026-10-24).

## Context

Every other bounded context of the fleet that exposes an HTTP API also ships an
MCP server so that agents (warehouse-ops-agent first) can read its data without
a bespoke REST client. last-mile-delivery had none. The service already has the
read use cases behind its REST adapter (`ListWaves`, `GetWaveStops`,
`GetRoute`), so an MCP adapter is another driving adapter over them, not new
domain.

## Decision

- **A second driving adapter, a separate binary.** `internal/adapters/inbound/mcp`
  is built on the official MCP Go SDK (`github.com/modelcontextprotocol/go-sdk`
  v1.8.0) and served over Streamable HTTP only (no stdio, no SSE) by `cmd/mcp`,
  mounted at `/mcp` and `/` with an open `/healthz`. It is a second deployable
  beside `cmd/api`; the Helm chart gets an `mcp` Deployment and Service
  (default off) plus an opt-in Kong HTTPRoute (`/api/mcp/last-mile-delivery`
  rewritten to `/mcp`).
- **Three read tools over the existing queries, one per REST GET, nothing
  else.** `list_waves(stationNodeId, serviceDate)`, `get_wave_stops(waveId)`
  and `get_route(routeId)` bind exactly the read use cases the REST adapter
  serves; every input is required, like the REST contract's query parameters
  and path variables. Arguments and results are lowerCamelCase like the REST
  bodies; typed errors carry the REST problem slug (`not-found`,
  `validation-failed`) as the prefix of the tool error. Every write stays on
  REST and the event consumers.
- **Read-only is structural.** The adapter's `Deps` holds the three read use
  cases and nothing that can write; the adapter and `cmd/mcp` import no event
  sink, no outbox, no Kafka adapter, no consumer and no unit of work (the
  ports package is imported for the typed read errors only, exactly like the
  REST adapter's error mapping — the write seams it carries are refused by
  name). `cmd/mcp` never dials Kafka and never runs the consumers or the
  relay: those stay in `cmd/api`. `TestMCPServerIsReadOnly` pins the imports,
  the `Deps` fields and the use cases `cmd/mcp` may name; `TestToolSurface`
  pins the exact tool set (budget 3), the read-only annotations and the
  absence of write verbs in tool names; `TestToolRegistryGolden` pins the
  whole advertised registry; the integration test proves the binary reads
  what `cmd/api`'s use cases wrote in a real Postgres and adds no outbox row.
- **No sibling calls, no auth.** The server reads this context's own tables
  only (the event-fed local copies of ADR 0003 are not exposed beyond the
  wave-stop grouping the REST API already serves), calls no other context over
  REST or MCP, and has no auth layer (the fleet-wide revert of 2026-09-11;
  `TestNoAuthIsRequired`). Access control is the in-cluster ClusterIP
  boundary.
- **Boot.** `DATABASE_URL` is mandatory (no in-memory mode); the binary runs
  the idempotent embedded migrations on start (with
  `MIGRATIONS_DATABASE_URL` for a direct DSN behind PgBouncer) so it can start
  against a fresh database before `cmd/api` has, exactly like `cmd/api`.
  `MCP_ADDR` (default `:8090`), `LOG_LEVEL`, `SERVICE_VERSION` and
  `ENVIRONMENT` complete the environment contract the chart renders.

## Consequences

- Agents can answer "which waves does this station run today, what packages
  does each hold, where does this route stand" without REST glue.
- Adding a tool is a reviewed act: the tool budget, the curated list and the
  golden file all change together (an amendment of this ADR). A new REST GET
  gains an MCP tool in the same change, so the two surfaces cannot drift.
- Scheduling waves, planning routes and recording attempts stay REST
  operations with `Idempotency-Key`; induction stays with the consumers.
