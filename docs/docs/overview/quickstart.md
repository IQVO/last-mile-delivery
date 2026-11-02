---
id: quickstart
title: Quickstart
sidebar_label: Quickstart
sidebar_position: 3
---

# Quickstart

Run `cmd/api` on your machine against a local Postgres and the fleet's Kafka broker, then drive it with `curl`. The responses below were captured from a real run of the binary built from this branch (ids and timestamps differ on your machine).

## Prerequisites

- Go 1.27.3 (`go.mod`).
- Docker, for a throwaway Postgres and for `make integration` (testcontainers).
- A Kafka broker. The fleet has **one** broker platform-wide: from the host it is `localhost:9092` (the kind cluster maps it). The old docker-compose Kafka is retired; do not start another one. The service needs a reachable broker at boot for `/readyz`, the outbox relay and the three consumers; a broker that is slow to come up only delays them (Kafka is dialled lazily).
- `golangci-lint` and `lefthook` if you plan to commit (`lefthook install` once per clone).

## `make` targets

| Target | What it runs |
| --- | --- |
| `make build` | `go build ./...` |
| `make check` | fmt-check, vet, build, lint, tests: the pre-push gate |
| `make test` | unit, handler and godog tests |
| `make bdd` | the godog features in `features/` |
| `make arch-test` | the architecture fitness tests in `internal/architecture/` |
| `make coverage` | coverage gate, at least 90 % over `internal/domain` and `internal/application` |
| `make mutation` / `make mutation-full` | gremlins per domain package (config `.gremlins.yaml`) |
| `make integration` | testcontainers Postgres and Kafka, needs Docker (build tag `integration`) |
| `make vuln` | `govulncheck` |
| `make guide-lint`, `make harness-test` | agent guide linters |

## Run the API

Start a Postgres, then export the five mandatory variables (the process exits at boot naming the first missing one):

```sh
docker run -d --name lmd-pg -e POSTGRES_PASSWORD=postgres -e POSTGRES_DB=last_mile_delivery -p 5432:5432 postgres:16-alpine

export DATABASE_URL="postgres://postgres:postgres@localhost:5432/last_mile_delivery?sslmode=disable"
export KAFKA_BROKERS="localhost:9092"
export LINEHAUL_CONSUMER_GROUP=last-mile-delivery-linehaul
export NODE_TOPOLOGY_CONSUMER_GROUP=last-mile-delivery-node-topology
export SHIPPING_CONSUMER_GROUP=last-mile-delivery-shipping
go run ./cmd/api        # migrations run at boot; REST on :8080
```

Boot log (JSON lines, real output):

```text
{"level":"INFO","msg":"postgres adapters configured"}
{"level":"INFO","msg":"consumer running","consumer":"node-topology consumer","topic":"retailnetwork.node-topology.events","group_id":"lmd-docs-topology"}
{"level":"INFO","msg":"consumer running","consumer":"linehaul consumer","topic":"retailnetwork.linehaul-transportation.events","group_id":"lmd-docs-linehaul"}
{"level":"INFO","msg":"outbox relay running","interval":1000000000,"max_attempts":20,"topic":"retailnetwork.last-mile-delivery.events"}
{"level":"INFO","msg":"http server listening","addr":":8080"}
{"level":"INFO","msg":"consumer running","consumer":"shipping consumer","topic":"retailnetwork.shipping-and-tracking.events","group_id":"lmd-docs-shipping"}
```

(the capture ran with `HTTP_ADDR=:18080` and `lmd-docs-*` group ids on a throwaway local broker; the ports below are therefore `18080`, replace with `8080`.)

```sh
curl -i localhost:8080/readyz
```

```text
HTTP/1.1 200 OK
Content-Type: application/json

{"status":"ready"}
```

## Seed a station

A wave can only be scheduled at a **known delivery station**, and stations come from node-topology's `NodeRegistered` events consumed into the `rm_stations` read model. Without one, every write answers `422 unknown-station`:

```sh
curl -s -X POST localhost:8080/waves -H "Content-Type: application/json" -H "Idempotency-Key: quickstart-wave-1" \
  -d '{"stationNodeId":"node-ds-01","serviceDate":"2026-11-02","inductCutoffAt":"2026-11-02T06:00:00Z","windowStart":"2026-11-02T10:00:00Z","windowEnd":"2026-11-02T18:00:00Z","stopCapacity":2}'
```

```json
{"type":"unknown-station","title":"The station is not a known delivery station","status":422,"detail":"unknown station: \"node-ds-01\"","instance":"/waves"}
```

On the fleet the station arrives through Kafka. For a standalone run seed the read model directly:

```sh
docker exec lmd-pg psql -U postgres -d last_mile_delivery -c "INSERT INTO rm_stations (node_id, node_type, region_id, timezone) VALUES ('node-ds-01','DELIVERY_STATION','region-se','America/Sao_Paulo')"
```

## First calls

Schedule a wave (state-changing calls need an `Idempotency-Key` of 1 to 128 printable ASCII characters; without one the answer is `400 idempotency-key-required`):

```sh
curl -i -X POST localhost:8080/waves -H "Content-Type: application/json" -H "Idempotency-Key: quickstart-wave-1" \
  -d '{"stationNodeId":"node-ds-01","serviceDate":"2026-11-02","inductCutoffAt":"2026-11-02T06:00:00Z","windowStart":"2026-11-02T10:00:00Z","windowEnd":"2026-11-02T18:00:00Z","stopCapacity":2}'
```

```text
HTTP/1.1 201 Created
Content-Type: application/json

{"waveId":"wave-4587c41f-4d52-49ad-a2bb-6d6199e0c9b6","stationNodeId":"node-ds-01","serviceDate":"2026-11-02","inductCutoffAt":"2026-11-02T06:00:00Z","windowStart":"2026-11-02T10:00:00Z","windowEnd":"2026-11-02T18:00:00Z","stopCapacity":2,"assignedStops":0,"remainingStops":2,"state":"OPEN"}
```

Contract a delivery service partner:

```sh
curl -i -X POST localhost:8080/partners -H "Content-Type: application/json" -H "Idempotency-Key: quickstart-dsp-1" \
  -d '{"stationNodeId":"node-ds-01","vanCapacity":40}'
```

```text
HTTP/1.1 201 Created

{"dspId":"dsp-35651205-c56d-4d05-a64a-760fd69d4ab6","stationNodeId":"node-ds-01","vanCapacity":40,"state":"ACTIVE"}
```

List the station's waves for the day:

```sh
curl -s "localhost:8080/waves?stationNodeId=node-ds-01&serviceDate=2026-11-02"
```

```json
{"items":[{"waveId":"wave-4587c41f-4d52-49ad-a2bb-6d6199e0c9b6","stationNodeId":"node-ds-01","serviceDate":"2026-11-02","inductCutoffAt":"2026-11-02T06:00:00Z","windowStart":"2026-11-02T10:00:00Z","windowEnd":"2026-11-02T18:00:00Z","stopCapacity":2,"assignedStops":0,"remainingStops":2,"state":"OPEN"}]}
```

The wave write also wrote a `DeliveryWaveScheduled` row in the outbox in the same transaction, and the relay published it (the first pass can fail while the broker creates the topic, hence `attempts = 2`):

```text
 id |                  topic                  |                                event_type                                 | attempts | published
----+-----------------------------------------+---------------------------------------------------------------------------+----------+-----------
  1 | retailnetwork.last-mile-delivery.events | com.retailnetwork.logistics.last-mile-delivery.wave.DeliveryWaveScheduled |        2 | t
```

Packages reach a wave through `LoadArrived` (linehaul) and `LabelIssued` (shipping) events; planning a route (`POST /routes`) needs inducted packages. The rest of the 12 operations are in the [API reference](/docs/api-reference).

## Run the read-only MCP server and A2A agent

Both are separate binaries over the same database and never touch Kafka.

```sh
DATABASE_URL="$DATABASE_URL" go run ./cmd/mcp        # MCP on :8090 (/mcp and /), GET /healthz
DATABASE_URL="$DATABASE_URL" A2A_AGENT_URL=http://localhost:8093/ go run ./cmd/a2a   # A2A on :8093
```

See [MCP tools](/docs/mcp/tools) and the A2A section of the [API reference](/docs/api-reference).

## Where next

[Configuration](/docs/operations/configuration), [Runbook](/docs/operations/runbook), [Testing](/docs/development/testing), [Architecture](/docs/overview/architecture).
