---
id: tools
title: MCP tools
sidebar_label: MCP tools
sidebar_position: 2
---

# MCP tools

`cmd/mcp` is the context's Model Context Protocol server ([ADR 0006](/docs/adr/0006-mcp-server-adoption)). It speaks **Streamable HTTP only** (no stdio, no SSE) on `MCP_ADDR` (default `:8090`), at both `/mcp` and `/`, plus an open `GET /healthz`. It is **unauthenticated** (access control is the in-cluster boundary) and **read-only by construction**: only the three read use cases `ListWaves`, `GetWaveStops` and `GetRoute` are wired, so the binary has no outbox, no consumer and never dials Kafka. Scheduling a wave, planning a route and recording an attempt are REST operations.

The table below comes from a live `tools/list` against a running `cmd/mcp` (and equals `internal/adapters/inbound/mcp/testdata/tool_registry.golden.json`, which a test pins). The server reports `serverInfo.name = last-mile-delivery-mcp`, version `1.0.0` (`SERVICE_VERSION`).

| Tool | Input (all required) | Annotations | Returns |
| --- | --- | --- | --- |
| `list_waves` | `stationNodeId` (string, e.g. `node-42`), `serviceDate` (string `YYYY-MM-DD`, the station's local date) | `readOnlyHint: true`, `idempotentHint: true`, `openWorldHint: false` | `{items: [...]}` ordered by `inductCutoffAt`: `waveId`, `stationNodeId`, `serviceDate`, `inductCutoffAt`, `windowStart`, `windowEnd`, `stopCapacity`, `assignedStops`, `remainingStops`, `state` (`OPEN`, `INDUCTION_CLOSED`, `DISPATCHED`, `COMPLETED`, `CANCELLED`), `cancelReason` on a cancelled wave. A station and date with no wave is an empty list. |
| `get_wave_stops` | `waveId` (string, e.g. `wave-1b4e28ba-...`) | same three hints | `{items: [...]}`: the packages inducted in the wave grouped by the opaque `stopKey` (keyed hash of the normalised address, or `pkg:<trackingNumber>` when no label data arrived), in the order of each stop's first induction; each stop has `trackingNumbers` and `destinationPostalPrefix` when the label had it. No address is ever returned. |
| `get_route` | `routeId` (string, e.g. `route-9f2c-...`) | same three hints | `routeId`, `waveId`, `dspId`, `stationNodeId`, `state` (`PLANNED`, `OUT_FOR_DELIVERY`, `COMPLETED`), `stopCount`, `stops` (`stopNo`, `trackingNumbers`, derived `outcome`, omitted until every package of the stop has an attempt) and `attempts` (`routeId`, `trackingNumber`, `outcome` `DELIVERED`\|`FAILED`, `failureReason`, `at`). |

All three tools have `additionalProperties: false` on input and output, and every tool is annotated read-only; `TestMCPServerIsReadOnly` in `internal/architecture/mcp_readonly_fitness_test.go` fail the build if a write use case or a non-read-only annotation appears. There are no optional inputs (`mcp_optional_inputs_test.go` pins that).

## Errors

A failure is a tool result with `isError: true` whose text is `<slug>: <detail>`, using the REST problem slugs (`internal/adapters/inbound/mcp/errors.go`):

| Slug | When |
| --- | --- |
| `not-found` | unknown `waveId` or `routeId` |
| `validation-failed` | malformed `stationNodeId`, or `serviceDate` not `YYYY-MM-DD` |
| `internal-error` | anything untyped; logged server-side, reported generically so DSNs and SQL never reach the model |

## Real exchange

Initialise a session, then call a tool (captured from a real run against the quickstart database; `Mcp-Session-Id` comes from the `initialize` response header):

```sh
curl -si -X POST localhost:8090/mcp -H "Content-Type: application/json" -H "Accept: application/json, text/event-stream" \
  -d '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"docs","version":"1"}}}'
```

The response carries `Mcp-Session-Id: <id>` and `serverInfo` `last-mile-delivery-mcp` / `1.0.0`. Send `notifications/initialized`, then:

```sh
curl -s -X POST localhost:8090/mcp -H "Content-Type: application/json" -H "Accept: application/json, text/event-stream" \
  -H "Mcp-Session-Id: <id>" -H "Mcp-Protocol-Version: 2025-06-18" \
  -d '{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"list_waves","arguments":{"stationNodeId":"node-ds-01","serviceDate":"2026-11-02"}}}'
```

```json
{"structuredContent":{"items":[{"assignedStops":0,"inductCutoffAt":"2026-11-02T06:00:00Z","remainingStops":2,"serviceDate":"2026-11-02","state":"OPEN","stationNodeId":"node-ds-01","stopCapacity":2,"waveId":"wave-4587c41f-4d52-49ad-a2bb-6d6199e0c9b6","windowEnd":"2026-11-02T18:00:00Z","windowStart":"2026-11-02T10:00:00Z"}]}}
```

Source: `internal/adapters/inbound/mcp/tools.go`, `server.go`, `errors.go`; `cmd/mcp/main.go`, `router.go`.

## Deployment

The chart renders the server as the optional `mcp` component (Service `:8090`, opt-in Kong route `/api/mcp/last-mile-delivery` to `/mcp`); see the [Runbook](/docs/operations/runbook). The sibling A2A agent offers the two skills `list_waves` and `get_route` over a different protocol; see the [API reference](/docs/api-reference).
