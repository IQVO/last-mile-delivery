# last-mile-delivery

Retail network, bounded context **Last-Mile Delivery** (Supporting, logistics tier).

## Disclaimer

This project is a domain-driven design study. It is not associated with, endorsed by or affiliated with any company, and it uses no company's code, data, logos or trademarks. All names, figures and examples are illustrative or synthetic.

## Overview

Runs each delivery station: publishes delivery waves (induct cutoff, delivery window, stop capacity) that the promise relies on, plans routes for delivery service partners, and records every delivery attempt.

Design source of truth: [retail-network docs](https://iqvo.github.io/warehouse-docs/network/contexts/last-mile-delivery) (`model/model.yaml`);
cross-context decisions that supersede it are recorded in `docs/adr/`. This repository is a service of the retail network, not of the
warehouse-systems fleet; the fleet reaches the network only through the `vendor-connectivity` Vendor API.

## What it does

- **Waves**: `DeliveryWave` is scheduled per station and service date (induct cutoff, delivery window, stop capacity), inducts packages, closes induction, dispatches, completes or is cancelled. Cancelling re-inducts its packages onto the next wave.
- **Routes**: a `Route` hands packages inducted in its wave to a `DeliveryServicePartner`, starts out for delivery, records one attempt per package and completes; the wave completes with its last route.
- **Attempts**: `PackageAttemptHistory` counts failures per tracking number; the third failure publishes `PackageReturnInitiated`, a package with no eligible wave publishes `PackageInductionDeferred`.

## Interfaces

- REST (`apis/openapi.yaml`, 12 operations plus `/healthz`, `/readyz`): `POST /waves`, `GET /waves`, `GET /waves/{waveId}/stops`, `POST /waves/{waveId}:close-induction|:dispatch|:cancel`, `POST /routes`, `GET /routes/{routeId}`, `POST /routes/{routeId}:start`, `POST /routes/{routeId}/attempts`, `POST /partners`, `POST /partners/{dspId}:deactivate`. State-changing calls need `Idempotency-Key`; errors are RFC 9457 problem+json.
- Published (`apis/asyncapi.yaml`, CloudEvents 1.0, transactional outbox): `DeliveryWaveScheduled`, `DeliveryWaveCapacityChanged`, `DeliveryWaveCancelled`, `PackageOutForDelivery`, `DeliveryAttempted`, `PackageInductionDeferred`, `PackageReturnInitiated` on `retailnetwork.last-mile-delivery.events`; `RoutePlanned` on `.analytics`.
- Consumed: `LoadArrived` (linehaul-transportation), `NodeRegistered` and `ServiceAreaAssigned` (node-topology), `LabelIssued` (shipping-and-tracking); inbox table, retry, `<topic>.dlq`.

## MCP (read-only)

`cmd/mcp` (ADR 0006) is a second deployable: the same read use cases the REST adapter serves, exposed to AI agents over the official MCP Go SDK on Streamable HTTP (`/mcp` and `/`, plus an open `/healthz`). Three tools, one per REST GET: `list_waves(stationNodeId, serviceDate)`, `get_wave_stops(waveId)`, `get_route(routeId)`; every input required, results camelCase like the REST bodies, typed errors carrying the REST problem slugs (`not-found`, `validation-failed`). Read-only is structural: no write use case is wired, no outbox, no consumer, Kafka never dialled, no auth (fleet decision). Boot mirrors `cmd/api`: mandatory `DATABASE_URL`, idempotent embedded migrations (optional `MIGRATIONS_DATABASE_URL`), `MCP_ADDR` (`:8090`), `LOG_LEVEL`, `SERVICE_VERSION`, `ENVIRONMENT`. The chart deploys it as an optional `mcp` component (Service `:8090`, opt-in Kong route `/api/mcp/last-mile-delivery` -> `/mcp`).

## Web remote (console MFE)

`web/` (ADR 0007) is the console's Module Federation remote (`last_mile_mfe`, exposes `./App`, dev/preview port `4014`, asset base `/mfes/last-mile-delivery/` on build). It is a small READ-ONLY React UI over this context's three GETs, resolved from the shell's runtime config (`apiOrigin` + `/api/last-mile-delivery`): Waves (a station + service date search), Wave stops (packages grouped by `stopKey`) and Routes (stops with derived outcomes and every attempt). No write path, no sibling API, no address ever shown. `npm ci && npm run lint && npx tsc -b && npm test && npm run build` inside `web/` is the gate (the CI `web` job); the image builds with `docker build --build-context uikit=../../warehouse-ui-kit .` and the chart deploys it as an optional `frontend` component (own nginx pod, ClusterIP only; the Nginx web gateway owns the public path).

## Run and test

Runtime: Go, hexagonal, real PostgreSQL and Kafka only. Mandatory environment: `DATABASE_URL`, `KAFKA_BROKERS`, `LINEHAUL_CONSUMER_GROUP`, `NODE_TOPOLOGY_CONSUMER_GROUP`, `SHIPPING_CONSUMER_GROUP` (the process exits at boot naming the missing one). Optional: `HTTP_ADDR` (`:8080`), `MIGRATIONS_DATABASE_URL`, `OUTBOX_RELAY_INTERVAL`, `OUTBOX_MAX_ATTEMPTS`, `SHUTDOWN_DRAIN_DELAY`, `LOG_LEVEL`.

- `make check`: format, vet, lint, unit, handler and BDD tests, architecture fitness tests (the pre-push gate).
- `make coverage` (>= 90% over `internal/domain` + `internal/application`), `make mutation` (gremlins per domain package), `make integration` (testcontainers Postgres and Kafka, needs Docker), `make vuln`.
- `go test -tags=integration ./cmd/api/` boots `run(ctx)` over real Postgres and Kafka, drives it through REST and Kafka only, and stops Kafka to prove `/readyz` answers 503 `dependency-unavailable` while `/healthz` stays 200.

## Deployment

The Helm chart is `charts/last-mile-delivery/` (one `api` Deployment, Service, ConfigMap, optional Secret, ServiceAccount; HPA, Ingress and Gateway API HTTPRoute are off by default, as are the optional `mcp` and `frontend` components of ADR 0006/0007 — `charts/last-mile-delivery/ci/all-components-values.yaml` renders them all together). The `release` job packages it as `last-mile-delivery-<version>.tgz` and pushes it to `oci://ghcr.io/iqvo`.

```sh
helm template r charts/last-mile-delivery -f charts/last-mile-delivery/ci/default-values.yaml
```

- The chart refuses to render without `database.url` or `database.existingSecret` (`DATABASE_URL` always comes from a Secret; `database.migrationsExistingSecretKey` optionally names a direct connection for the migrations), `kafka.brokers`, and the three stable consumer groups `config.linehaulConsumerGroup`, `config.nodeTopologyConsumerGroup`, `config.shippingConsumerGroup` (service-prefixed, never the pod name). A `*_MODE` in `extraEnv` is refused: there is no mode switch.
- The pod runs non-root with a read-only root filesystem, no privilege escalation, all capabilities dropped and the `RuntimeDefault` seccomp profile; probes are `/healthz` (startup, liveness) and `/readyz` (readiness). `terminationGracePeriodSeconds` (60) must cover `config.shutdownDrainDelay` + 50 s (HTTP 10 s, three consumers 10 s each, relay 10 s); the chart refuses less.
- Tests: `bash charts/last-mile-delivery/tests/render_test.sh` and `python3 charts/last-mile-delivery/tests/test_service_selectors.py` (needs helm and PyYAML); CI runs them in `chart-tests`, and `helm-lint` runs `ct lint` on pull requests.

See `docs/adr/` for decisions and `docs/before-first-release.md` for the packaging checklist (rows 5 to 9, the chart, are done; row 11, the registry secrets, is for the repo admin).
