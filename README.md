# last-mile-delivery

Retail network, bounded context **Last-Mile Delivery** (Supporting, logistics tier).

Runs each delivery station: publishes delivery waves (induct cutoff, delivery window, stop capacity) that the promise relies on, plans routes for delivery service partners, and records every delivery attempt.

It owns the `DeliveryWave` (scheduled per station and service date), `Route` (packages handed to a `DeliveryServicePartner`) and `PackageAttemptHistory` aggregates. It does **not** own the delivery promise (delivery-promise's — it consumes the waves), the linehaul legs (linehaul-transportation's `LoadArrived` is consumed), the nodes/service areas (node-topology's, consumed) or the package labels (shipping-and-tracking's `LabelIssued` is consumed). It is a downstream conformist of those Open Host Services. This repository is a service of the retail network, not of the warehouse-systems fleet; the fleet reaches the network only through the `vendor-connectivity` Vendor API.

Runtime: Go, hexagonal, real PostgreSQL and Kafka only, CloudEvents 1.0 on every message (`com.retailnetwork.logistics.last-mile-delivery.<entity>.<Event>`), transactional outbox. Design source of truth: [retail-network docs](https://iqvo.github.io/warehouse-docs/network/contexts/last-mile-delivery) (`model/model.yaml`); cross-context decisions that supersede it are recorded in `docs/adr/`.

## Disclaimer

This project is a domain-driven design study. It is not associated with, endorsed by or affiliated with any company, and it uses no company's code, data, logos or trademarks. All names, figures and examples are illustrative or synthetic.

## Documentation

This repository has no docs site of its own. Its design documentation lives on the warehouse-docs retail-network pages:

| Area | Pages |
|---|---|
| Retail network | [Network overview](https://iqvo.github.io/warehouse-docs/network) |
| This context | [Index / service facts](https://iqvo.github.io/warehouse-docs/network/contexts/last-mile-delivery), [Business context](https://iqvo.github.io/warehouse-docs/network/contexts/last-mile-delivery/business-context), [Domain events](https://iqvo.github.io/warehouse-docs/network/contexts/last-mile-delivery/domain-events), [Use cases](https://iqvo.github.io/warehouse-docs/network/contexts/last-mile-delivery/use-cases) |
| DDD artifact pack | [Ubiquitous language](https://iqvo.github.io/warehouse-docs/network/contexts/last-mile-delivery/ubiquitous-language), [Context map](https://iqvo.github.io/warehouse-docs/network/contexts/last-mile-delivery/context-map), [Core domain chart](https://iqvo.github.io/warehouse-docs/network/contexts/last-mile-delivery/core-domain-chart), [Bounded context canvas](https://iqvo.github.io/warehouse-docs/network/contexts/last-mile-delivery/bounded-context-canvas), [Aggregate design canvas](https://iqvo.github.io/warehouse-docs/network/contexts/last-mile-delivery/aggregate-design-canvas), [EventStorming](https://iqvo.github.io/warehouse-docs/network/contexts/last-mile-delivery/eventstorming), [Class diagram](https://iqvo.github.io/warehouse-docs/network/contexts/last-mile-delivery/class-diagram), [Sequence diagrams](https://iqvo.github.io/warehouse-docs/network/contexts/last-mile-delivery/sequence-diagrams), [ER diagram](https://iqvo.github.io/warehouse-docs/network/contexts/last-mile-delivery/entity-relationship), [Domain message flow](https://iqvo.github.io/warehouse-docs/network/contexts/last-mile-delivery/domain-message-flow) |
| Decisions | [Network ADRs](https://iqvo.github.io/warehouse-docs/network/adr) |

In-tree: [docs/adr/](docs/adr/index.md) (decisions, index), `docs/before-first-release.md` (packaging checklist).

## Architecture

Hexagonal / ports & adapters. Dependencies point inward only:

```
cmd/api/                     composition root: REST API + outbox relay + the three Kafka
                             consumers in one process, on real Postgres and real Kafka
cmd/mcp/                     read-only MCP server (Streamable HTTP on /mcp)
cmd/a2a/                     read-only A2A agent (JSON-RPC over HTTP, Agent Card at
                             /.well-known/agent-card.json)
internal/
  domain/                    wave, route, partner, history (PackageAttemptHistory),
                             shared — pure business logic
  application/
    ports/                   OUT interfaces only (repos, EventPublisher, Clock)
    usecases/                one struct per use case
    repository/ outbox/      repository orchestration; outbox
    views/                   the read models
  adapters/
    inbound/http/            chi handlers, RFC 9457 problem mapping, idempotency middleware
    inbound/kafka/           linehaul-transportation, node-topology, shipping-and-tracking
                             consumers (inbox table, retry, <topic>.dlq)
    inbound/mcp/             the three read-only MCP tools
    inbound/a2a/             the two read-only A2A skills
    inbound/requestlog/      the request log line
    outbound/postgres/       pgxpool repos, embedded golang-migrate, UnitOfWork
    outbound/outbox/         transactional outbox writer + relay
    outbound/kafka/          the topic publisher behind the relay
    outbound/telemetry/      OTel wiring + the business counter
    outbound/system/         wall clock, id source
    outbound/memory/         test doubles only, never linked into cmd/** (fitness-tested)
    outbound/repocontract/   the repository contract tests
  architecture/              the fitness tests below
apis/                        openapi.yaml (REST) + asyncapi.yaml (events)
docs/adr/                    9 ADRs + index
features/                    10 godog feature files
charts/last-mile-delivery/   the Helm chart
web/                         the operator micro-frontend remote
```

The layering is an executable test, not a convention: `internal/architecture/architecture_test.go` (`TestHexagonalArchitecture`) fails the build if the domain ever imports an outer layer or an adapter reaches sideways into a sibling. The same package pins CloudEvents-only envelopes and the event catalogue (`catalogue_fitness_test.go`, `events_fitness_test.go`), the Kafka DLQ/acks/topic rules (`kafka_*_fitness_test.go`), the outbox relay attempt cap, and the read-only MCP/A2A surfaces (`mcp_readonly_fitness_test.go`, `mcp_optional_inputs_test.go`, `a2a_fitness_test.go`).

## What it does

- **Waves**: `DeliveryWave` is scheduled per station and service date (induct cutoff, delivery window, stop capacity), inducts packages, closes induction, dispatches, completes or is cancelled. Cancelling re-inducts its packages onto the next wave.
- **Routes**: a `Route` hands packages inducted in its wave to a `DeliveryServicePartner`, starts out for delivery, records one attempt per package and completes; the wave completes with its last route.
- **Attempts**: `PackageAttemptHistory` counts failures per tracking number; the third failure publishes `PackageReturnInitiated`, a package with no eligible wave publishes `PackageInductionDeferred`.

## Domain events

All facts leave as CloudEvents 1.0 (structured content mode) through the transactional outbox; the envelope and type catalogue are [ADR 0004](docs/adr/0004-cloudevents-envelope-and-type-catalogue.md). The type convention is `com.retailnetwork.logistics.last-mile-delivery.<entity>.<Event>`, for example:

```
com.retailnetwork.logistics.last-mile-delivery.wave.DeliveryWaveScheduled
com.retailnetwork.logistics.last-mile-delivery.route.RoutePlanned
```

Specified in `apis/asyncapi.yaml`:

| Topic | Direction | Messages |
|---|---|---|
| `retailnetwork.last-mile-delivery.events` | publishes (via the transactional outbox) | `DeliveryWaveScheduled`, `DeliveryWaveCapacityChanged`, `DeliveryWaveCancelled`, `PackageOutForDelivery`, `DeliveryAttempted`, `PackageInductionDeferred`, `PackageReturnInitiated` |
| `retailnetwork.last-mile-delivery.analytics` | publishes (analytics-only) | `RoutePlanned` |
| `retailnetwork.linehaul-transportation.events` | consumes | `LoadArrived` |
| `retailnetwork.node-topology.events` | consumes | `NodeRegistered`, `ServiceAreaAssigned` |
| `retailnetwork.shipping-and-tracking.events` | consumes | `LabelIssued` |

Consumption goes through the inbox table with retry and a `<topic>.dlq` per topic. `delivery-promise` and `order-sourcing` plan the last leg on `DeliveryWaveScheduled`; a wave with no remaining stops is no longer a candidate.

## Running it

Runtime: Go, hexagonal, real PostgreSQL and Kafka only. Mandatory environment: `DATABASE_URL`, `KAFKA_BROKERS`, `LINEHAUL_CONSUMER_GROUP`, `NODE_TOPOLOGY_CONSUMER_GROUP`, `SHIPPING_CONSUMER_GROUP` (the process exits at boot naming the missing one).

```sh
export DATABASE_URL="postgres://user:pass@localhost:5432/last_mile_delivery?sslmode=disable"
export KAFKA_BROKERS="localhost:9092"
export LINEHAUL_CONSUMER_GROUP=last-mile-delivery-linehaul
export NODE_TOPOLOGY_CONSUMER_GROUP=last-mile-delivery-node-topology
export SHIPPING_CONSUMER_GROUP=last-mile-delivery-shipping
go run ./cmd/api                   # migrations run at boot; REST on :8080
```

### Configuration

`cmd/api`:

| Env var | Default | Meaning |
|---|---|---|
| `HTTP_ADDR` | `:8080` | Listen address |
| `DATABASE_URL` | **required** | Postgres DSN |
| `MIGRATIONS_DATABASE_URL` | `DATABASE_URL` | Direct DSN for the boot migrations |
| `KAFKA_BROKERS` | **required** | Comma-separated brokers (relay, consumers, `/readyz`) |
| `LINEHAUL_CONSUMER_GROUP` | **required** | Stable group id of the linehaul-transportation consumer |
| `NODE_TOPOLOGY_CONSUMER_GROUP` | **required** | Stable group id of the node-topology consumer |
| `SHIPPING_CONSUMER_GROUP` | **required** | Stable group id of the shipping-and-tracking consumer |
| `OUTBOX_RELAY_INTERVAL` | `1s` | Relay poll interval |
| `OUTBOX_MAX_ATTEMPTS` | `20` | Failures before the relay excludes a row (`0` = unlimited) |
| `SHUTDOWN_DRAIN_DELAY` | `5s` | Wait after flipping `/readyz` before closing |
| `LOG_LEVEL` | `info` | `debug`/`info`/`warn`/`error` |
| `OTEL_SERVICE_NAME` | `last-mile-delivery` | `service.name` of spans and metrics |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | telemetry default | Collector OTLP/gRPC endpoint; export never blocks |
| `SERVICE_VERSION` | `dev` | `service.version` |
| `ENVIRONMENT` | `local` | `deployment.environment.name` |

`cmd/mcp` (read-only: no write use case wired, no outbox, no consumer, Kafka never dialled): `MCP_ADDR` (`:8090`), `DATABASE_URL` (**required**), `MIGRATIONS_DATABASE_URL` (optional, default `DATABASE_URL`), `LOG_LEVEL`, `SERVICE_VERSION`, `ENVIRONMENT` plus the telemetry variables.

`cmd/a2a` (read-only, same rules as `cmd/mcp`): `A2A_ADDR` (`:8093`), `A2A_AGENT_URL` (**required**, the absolute URL the Agent Card advertises), `DATABASE_URL` (**required**), `MIGRATIONS_DATABASE_URL`, `LOG_LEVEL`, `SERVICE_VERSION`, `ENVIRONMENT` plus the telemetry variables.

## Observability

Logs, metrics and traces follow the fleet standard ([ADR 0008](docs/adr/0008-fleet-observability-standard.md)): JSON logs on stdout carry `trace_id`/`span_id` and a `request_id`; `cmd/api`, `cmd/mcp` and `cmd/a2a` export OTLP/gRPC to the Collector (`OTEL_EXPORTER_OTLP_ENDPOINT`, `OTEL_SERVICE_NAME`, `SERVICE_VERSION`, `ENVIRONMENT`; export never blocks, so no Collector costs telemetry, nothing else) with HTTP request duration, Go runtime metrics and the business counter `last_mile_delivery.package.attempted` (`outcome`, `reason`); the trace continues across Kafka in the `traceparent` header of each published event and is picked up by the consumers. The chart's `otel.*` values render the env.

## API

REST contract: [`apis/openapi.yaml`](apis/openapi.yaml) — 12 operations plus `/healthz` and `/readyz`. State-changing calls need `Idempotency-Key`; errors are RFC 9457 problem+json.

| Method | Route | Purpose |
|---|---|---|
| `POST` | `/waves` | Schedule a delivery wave (Idempotency-Key) |
| `GET` | `/waves` | List waves (station + service date) |
| `GET` | `/waves/{waveId}/stops` | The wave's stops |
| `POST` | `/waves/{waveId}:close-induction` | Close induction (Idempotency-Key) |
| `POST` | `/waves/{waveId}:dispatch` | Dispatch (Idempotency-Key) |
| `POST` | `/waves/{waveId}:cancel` | Cancel; re-inducts packages onto the next wave (Idempotency-Key) |
| `POST` | `/routes` | Plan a route (Idempotency-Key) |
| `GET` | `/routes/{routeId}` | One route |
| `POST` | `/routes/{routeId}:start` | Start out for delivery (Idempotency-Key) |
| `POST` | `/routes/{routeId}/attempts` | Record one delivery attempt per package (Idempotency-Key) |
| `POST` | `/partners` | Register a delivery service partner (Idempotency-Key) |
| `POST` | `/partners/{dspId}:deactivate` | Deactivate a partner (Idempotency-Key) |

## Helm chart

The Helm chart is `charts/last-mile-delivery/` (one `api` Deployment, Service, ConfigMap, optional Secret, ServiceAccount; HPA, Ingress and Gateway API HTTPRoute are off by default, as are the optional `mcp`, `a2a` and `frontend` components of ADR 0006/0009/0007 — `charts/last-mile-delivery/ci/all-components-values.yaml` renders them all together). The `release` job packages it as `last-mile-delivery-<version>.tgz` and pushes it to `oci://ghcr.io/iqvo`.

```sh
helm template r charts/last-mile-delivery -f charts/last-mile-delivery/ci/default-values.yaml
```

- The chart refuses to render without `database.url` or `database.existingSecret` (`DATABASE_URL` always comes from a Secret; `database.migrationsExistingSecretKey` optionally names a direct connection for the migrations), `kafka.brokers`, and the three stable consumer groups `config.linehaulConsumerGroup`, `config.nodeTopologyConsumerGroup`, `config.shippingConsumerGroup` (service-prefixed, never the pod name). A `*_MODE` in `extraEnv` is refused: there is no mode switch.
- The pod runs non-root with a read-only root filesystem, no privilege escalation, all capabilities dropped and the `RuntimeDefault` seccomp profile; probes are `/healthz` (startup, liveness) and `/readyz` (readiness). `terminationGracePeriodSeconds` (60) must cover `config.shutdownDrainDelay` + 50 s (HTTP 10 s, three consumers 10 s each, relay 10 s); the chart refuses less.
- Tests: `bash charts/last-mile-delivery/tests/render_test.sh` and `python3 charts/last-mile-delivery/tests/test_service_selectors.py` (needs helm and PyYAML); CI runs them in `chart-tests`, and `helm-lint` runs `ct lint` on pull requests.

### Running the MCP server in Kubernetes

`cmd/mcp` ([ADR 0006](docs/adr/0006-mcp-server-adoption.md)) is a second deployable: the same read use cases the REST adapter serves, exposed to AI agents over the official MCP Go SDK on Streamable HTTP (`/mcp` and `/`, plus an open `/healthz`). Three tools, one per REST GET: `list_waves(stationNodeId, serviceDate)`, `get_wave_stops(waveId)`, `get_route(routeId)`; every input required, results camelCase like the REST bodies, typed errors carrying the REST problem slugs (`not-found`, `validation-failed`). Read-only is structural: no write use case is wired, no outbox, no consumer, Kafka never dialled, no auth (fleet decision). The chart deploys it as an optional `mcp` component (Service `:8090`, opt-in Kong route `/api/mcp/last-mile-delivery` → `/mcp`).

### Running the A2A agent in Kubernetes

`cmd/a2a` ([ADR 0009](docs/adr/0009-a2a-inbound-adapter.md)) is a third deployable: the same read use cases again, offered to peer agents as a goal-delegation surface over the official A2A Go SDK (JSON-RPC at `/` and `/a2a`, the Agent Card at `/.well-known/agent-card.json`, plus an open `/healthz`). Two skills derived from the MCP tools, sent as `{"skill": "<id>", "arguments": {...}}` in a data part: `list_waves(stationNodeId, serviceDate)` (a station's waves for a day) and `get_route(routeId)` (where one route stands). Input is strict (unknown arguments and trailing data are refused) and rejections are answered in-band as RFC 7807-shaped problems with the REST statuses (`not-found` 404, `validation-failed` 422, `internal-error` 500; request-shape problems 400), never as JSON-RPC errors. Read-only is structural, like MCP: no write use case, no outbox, no consumer, Kafka never dialled, no outbound A2A client, no auth (fleet decision). The chart deploys it as an optional `a2a` component (Service `:8093`, `A2A_AGENT_URL` defaulting to the in-cluster Service DNS, opt-in Kong route `/api/a2a/last-mile-delivery` → `/`).

## Operator micro-frontend (`web/`)

`web/` ([ADR 0007](docs/adr/0007-console-remote.md)) is the console's Module Federation remote (`last_mile_mfe`, exposes `./App`, dev/preview port `4014`, asset base `/mfes/last-mile-delivery/` on build). It is a small READ-ONLY React UI over this context's three GETs, resolved from the shell's runtime config (`apiOrigin` + `/api/last-mile-delivery`): Waves (a station + service date search), Wave stops (packages grouped by `stopKey`) and Routes (stops with derived outcomes and every attempt). No write path, no sibling API, no address ever shown.

```sh
cd web && npm ci && npm run lint && npx tsc -b && npm test && npm run build
docker build --build-context uikit=../../warehouse-ui-kit .
```

The chart deploys it as an optional `frontend` component (own nginx pod, ClusterIP only; the Nginx web gateway owns the public path).

## Local development / quality gate

- `make check`: format, vet, lint, unit, handler and BDD tests, architecture fitness tests (the pre-push gate).
- `make coverage` (>= 90% over `internal/domain` + `internal/application`), `make mutation` (gremlins per domain package), `make integration` (testcontainers Postgres and Kafka, needs Docker), `make vuln`.
- `go test -tags=integration ./cmd/api/` boots `run(ctx)` over real Postgres and Kafka, drives it through REST and Kafka only, and stops Kafka to prove `/readyz` answers 503 `dependency-unavailable` while `/healthz` stays 200.

lefthook wires the same targets as hooks (`lefthook install` once per clone): pre-commit runs `make fmt-check`, `make vet`, `make lint`; pre-push runs `make check`.

## Tests

- **Unit**: `make test` — domain, use cases, handlers, adapters.
- **BDD**: `make bdd` — godog scenarios in `features/` (10 feature files).
- **Integration**: `make integration` — testcontainers Postgres + Kafka (incl. the `/readyz` 503 proof).
- **Mutation**: `make mutation` (blocking subset) / `make mutation-full` — gremlins, config in `.gremlins.yaml`.
- **Architecture fitness**: `make arch-test` — `internal/architecture/` (hexagonal rule, CloudEvents-only, catalogue, Kafka rules, outbox cap, read-only MCP/A2A).
- **Web**: lint/typecheck/test/build of `web/` against `warehouse-ui-kit`.

CI (`.github/workflows/ci.yml`) runs `lint`, `guide-lint`, `complexity`, `test`, `bdd`, `integration`, `mutation-fast`, `api-lint`, `vuln`, `helm-lint`, `chart-tests`, `arch-test` and `web` on every push and PR; the exhaustive `mutation` job and the advisory `drift` sensor run on schedule; `docker-publish` pushes the image and `release` tags and publishes the chart.

## Architecture Decision Records

[docs/adr/](docs/adr/index.md) — index plus 9 ADRs (0001–0009: bounded context, aggregates and invariants, consumed contracts and local read models, CloudEvents envelope and type catalogue, runtime/outbox/HTTP and readiness, MCP server adoption, console remote, fleet observability standard, A2A inbound adapter). `docs/before-first-release.md` is the packaging checklist (rows 5 to 9, the chart, are done; row 11, the registry secrets, is for the repo admin).

## License

[MIT](LICENSE).
