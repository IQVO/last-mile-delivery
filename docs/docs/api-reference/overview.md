---
id: overview
title: API overview
sidebar_label: API overview
slug: /api-reference
sidebar_position: 1
---

# API overview

`cmd/api` serves every REST route below (`internal/adapters/inbound/http/server.go`) on `HTTP_ADDR` (default `:8080`); `cmd/mcp` and `cmd/a2a` serve the two agent surfaces over the same read use cases. The contracts are [`apis/openapi.yaml`](https://github.com/IQVO/last-mile-delivery/blob/develop/apis/openapi.yaml) and [`apis/asyncapi.yaml`](https://github.com/IQVO/last-mile-delivery/blob/develop/apis/asyncapi.yaml). The router registers exactly the 12 spec operations plus `/healthz` and `/readyz` (probes, not in the spec). See the [Quickstart](/docs/overview/quickstart) for real responses.

| Interface | Source of truth | Notes |
| --- | --- | --- |
| REST | `apis/openapi.yaml` (OpenAPI 3.0.3, spec version 0.1.0) | The pages under *REST* in the sidebar are generated from the spec with `npm run gen-api-docs`; do not edit them by hand. Servers named in the spec: `http://localhost:8080`. Through Kong in the kind cluster: `http://localhost:8000/api/last-mile-delivery` (prefix stripped before the pod). |
| Events | `apis/asyncapi.yaml` (AsyncAPI 3.0.0, version 1.0.0) | Summarised on the [event catalogue](/docs/api-reference/events); every field on [Domain events](/docs/ddd/domain-events). |
| MCP | [ADR 0006](/docs/adr/0006-mcp-server-adoption), `internal/adapters/inbound/mcp` | `cmd/mcp` serves Streamable HTTP on `/mcp` and `/` (port 8090; through Kong `/api/mcp/last-mile-delivery`). Three read-only tools, no write tool: `list_waves`, `get_wave_stops`, `get_route`. See [MCP tools](/docs/mcp/tools). No auth, by fleet decision. |
| A2A | [ADR 0009](/docs/adr/0009-a2a-inbound-adapter), `internal/adapters/inbound/a2a` | `cmd/a2a` serves JSON-RPC 2.0 on `/` and `/a2a` (port 8093; through Kong `/api/a2a/last-mile-delivery`) and the Agent Card at `/.well-known/agent-card.json`. Two read-only skills, no write skill: `list_waves` (`stationNodeId`, `serviceDate`) and `get_route` (`routeId`). No auth, by fleet decision. |

## Endpoints

| Tag | Endpoint | Operation id | Success |
| --- | --- | --- | --- |
| Waves | `POST /waves` | `scheduleWave` | `201`, the wave `OPEN`; `Idempotency-Key` required |
| Waves | `GET /waves?stationNodeId=&serviceDate=` | `listWaves` | `200 {items}` ordered by `inductCutoffAt`; an empty list when none |
| Waves | `GET /waves/{waveId}/stops` | `getWaveStops` | `200 {items}`, packages grouped by `stopKey` |
| Waves | `POST /waves/{waveId}/induction-closures` | `closeInduction` | `200`, the wave `INDUCTION_CLOSED` |
| Waves | `POST /waves/{waveId}/dispatches` | `dispatchWave` | `200`, the wave `DISPATCHED` (or `COMPLETED` when it has no route to run) |
| Waves | `POST /waves/{waveId}/cancellations` | `cancelWave` | `200`, the wave `CANCELLED`, its packages re-inducted; body `{reason}` |
| Routes | `POST /routes` | `planRoute` | `201`, the route `PLANNED`; body `{waveId, dspId, stops[{stopNo, trackingNumbers[]}]}` |
| Routes | `GET /routes/{routeId}` | `getRoute` | `200`, stops with derived outcome and every attempt |
| Routes | `POST /routes/{routeId}/starts` | `startRoute` | `200`, the route `OUT_FOR_DELIVERY` |
| Routes | `POST /routes/{routeId}/attempts` | `recordAttempt` | `201`, the attempt; body `{trackingNumber, outcome, failureReason?, at}` |
| Partners | `POST /partners` | `contractPartner` | `201`, the partner `ACTIVE`; body `{stationNodeId, vanCapacity}` |
| Partners | `POST /partners/{dspId}/deactivations` | `deactivatePartner` | `200`, the partner `INACTIVE` |
| Health | `GET /healthz`, `GET /readyz` | none (not in the spec) | `200`; `/readyz` is `503` while draining or when Postgres or Kafka is unreachable |

Source: `internal/adapters/inbound/http/server.go`, `handlers.go`, `dto.go`.

## Conventions in the contract

- **`Idempotency-Key`.** Required on every state-changing `POST` (the nine in the table above). Header of 1 to 128 printable ASCII characters; missing is `400 idempotency-key-required`, malformed or longer is `400 idempotency-key-invalid`. The key is global: the fingerprint is method + path + SHA-256 of the body. The same key with another method, path or body is `409 idempotency-key-reused`; an equal request within 24 hours replays the stored answer with the header `Idempotency-Replayed: true`. Concurrent calls with one key are serialised by a Postgres advisory lock, and the key row commits in the same transaction as the aggregate and its outbox rows. 2xx and 4xx answers are stored; a 5xx is not. Table `idempotency_keys`.
- **Paging.** `GET /waves` and `GET /waves/{waveId}/stops` take `limit` (1 to 500, default 100) and `cursor` and answer `{items, nextCursor}` (`nextCursor` is absent on the last page). `400 invalid-limit`, `400 invalid-cursor`, and `400 unknown-query-parameter` for any parameter the collection does not declare.
- **Errors** are RFC 9457 `application/problem+json` with `type` = the slug, `title`, `status`, `detail`, `instance`. Shared slugs: `not-found`, `illegal-transition`, `concurrent-update`, `validation-failed`, `internal-error`, plus one kebab-case slug per domain error (`wave-full`, `induction-closed`, `already-inducted`, `invalid-wave-times`, `invalid-van-capacity`, `unknown-station`, `route-over-capacity`, `route-not-started`, `already-delivered`, `attempt-already-recorded`, `package-not-on-route`, `package-not-inducted`, `package-already-routed`, `stop-key-mismatch`, `partner-inactive`, `partner-station-mismatch`, `route-incomplete`) and the idempotency slugs. The full mapping with status codes is on [Use cases](/docs/ddd/use-cases#error-mapping); remedies are on [Troubleshooting](/docs/operations/troubleshooting).
- **NUL bytes.** A NUL in the path is `404`; in the query or body it is `422 validation-failed`.
- **Body limit.** 64 KiB per request.
- **Time.** All instants are RFC 3339 UTC; `serviceDate` is the station's local `YYYY-MM-DD`.
- **Identifiers.** Ids are generated by the service (`wave-<uuid>`, `route-<uuid>`, `dsp-<uuid>`); tracking numbers and node ids come from other contexts and are opaque strings.
- **Concurrency.** Aggregates carry a `version`; a stale write is `409 concurrent-update`.
- **No PII.** No response contains an address or name; stops are identified by an opaque `stopKey` and a postal prefix.
- **Auth**: none. The fleet reverted REST, MCP and A2A auth on 2026-09-11, and `TestNoAuthMiddlewareReintroduced` fails CI if auth middleware returns under `internal/adapters/inbound`.

## A2A in practice

The Agent Card advertises the JSON-RPC endpoint taken from `A2A_AGENT_URL` and the two skills with an example each. A request is a `SendMessage` whose first part is a data part `{"skill": "<id>", "arguments": {...}}`; the protocol version header `A2A-Version: 1.0` is required (a client that omits it gets JSON-RPC error `-32009`, `VERSION_NOT_SUPPORTED`). Arguments are strict: unknown arguments and trailing data are refused. Domain rejections are answered **inside the message** as RFC 7807-shaped problems with the REST statuses (`not-found` 404, `validation-failed` 422, `internal-error` 500; request-shape problems 400), never as JSON-RPC errors. A captured exchange from a real run:

```sh
curl -s -X POST localhost:8093/a2a -H "Content-Type: application/json" -H "A2A-Version: 1.0" \
  -d '{"jsonrpc":"2.0","id":1,"method":"SendMessage","params":{"message":{"messageId":"m1","role":"ROLE_USER","parts":[{"data":{"skill":"list_waves","arguments":{"stationNodeId":"node-ds-01","serviceDate":"2026-11-02"}}}]}}}'
```

The reply carries a data part `{"skill":"list_waves","data":{"waves":{"stationNodeId":"node-ds-01","serviceDate":"2026-11-02","items":[...]}}}` and a text part such as `1 waves at node-ds-01 on 2026-11-02: wave-4587c41f-... OPEN (0/2 stops assigned)`. The Agent Card (`GET /.well-known/agent-card.json`) reports `name: last-mile-delivery`, `version: 1.0.0`, `streaming: false`, `pushNotifications: false`.

A2A carries delegation only; facts stay CloudEvents on Kafka.
