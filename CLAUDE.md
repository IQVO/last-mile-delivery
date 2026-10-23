# last-mile-delivery

harness-template: v4

Retail network, bounded context Last-Mile Delivery (Supporting, logistics tier). Owns DeliveryWave, Route, DeliveryServicePartner and the PackageAttemptHistory read-side counter; publishes waves, routes and attempts; consumes LoadArrived (linehaul-transportation), NodeRegistered + ServiceAreaAssigned (node-topology) and LabelIssued (shipping-and-tracking); calls no sibling.
Design source of truth: IQVO/retail-network-docs `model/model.yaml` (context last-mile-delivery), superseded where docs/adr and the cross-context contract decisions say so. This service belongs to the retail network, not the warehouse-systems fleet.
This project is a domain-driven design study, not associated with any company: never name a real company, brand or product in code, comments, ADRs, specs, tests or fixtures (README `## Disclaimer`).

## Commands
`make check` (fmt, vet, build, lint, unit + handler + BDD + architecture tests; the pre-push gate), `make coverage` (>= 90% over internal/domain + internal/application), `make mutation` (gremlins per internal/domain/<pkg>, thresholds below measurement),
`make arch-test`, `make integration` (testcontainers Postgres + Kafka, needs Docker; build tag `integration`), `make vuln`. Lint: `golangci-lint run --allow-parallel-runners ./...`.
Guides: `python3 scripts/harness/repo_lint.py`, `python3 scripts/harness/guide_lint.py` must stay clean. Never gate a commit on a piped command.

## Layout (hexagonal)
`internal/domain/{shared,wave,route,partner,history}` pure Go; `internal/application/{ports (interfaces only),repository (filters),views,usecases,outbox}`;
`internal/adapters/inbound/{http (chi),kafka (consumers, inbox, DLQ)}`; `internal/adapters/outbound/{postgres,kafka,outbox,system,memory,repocontract}`; `internal/adapters/kafka/cloudevents` (the ONLY envelope builder);
`internal/testing/{pgtest,kafkatest}` (testcontainers helpers); `cmd/api` (composition root, `run(ctx)`); `features/` (godog, never a .go file at the repo root). Migrations are embedded in `internal/adapters/outbound/postgres/migrations`.

## Runtime rule
Real PostgreSQL and Kafka only. `DATABASE_URL`, `KAFKA_BROKERS`, `LINEHAUL_CONSUMER_GROUP`, `NODE_TOPOLOGY_CONSUMER_GROUP`, `SHIPPING_CONSUMER_GROUP` are mandatory (boot error naming the variable, before the listener binds; `MIGRATIONS_DATABASE_URL` optional).
The outbox relay is always on (`OUTBOX_MAX_ATTEMPTS` default 20, 0 = unlimited; `OUTBOX_RELAY_INTERVAL` 1s); the process exits non-zero when the relay or a consumer ends on its own.
`/healthz` = liveness; `/readyz` = drain gate + Postgres ping + Kafka metadata request with an EMPTY non-nil topic list on a FRESH connection per check (kafka-go's Client.Metadata is cached and stays "ready" after the broker is gone).
Shutdown: /readyz 503, `SHUTDOWN_DRAIN_DELAY` (5s), drain HTTP, stop consumers and relay. cmd/** never imports internal/adapters/outbound/memory or repocontract. No authentication on REST. See docs/adr/0005.

## REST
chi router implementing every operation of apis/openapi.yaml (12 routes) plus /healthz, /readyz. Every state-changing POST takes `Idempotency-Key` (table `idempotency_keys`, key = (route, key), 24h, replays the first response, same key + other request = 422 `idempotency-key-reused`).
NUL: 404 in the path, 422 `validation-failed` in query/body. Errors are RFC 9457 problem+json, `type` = slug: not-found, illegal-transition, concurrent-update, validation-failed + one kebab-case slug per model Err (internal/adapters/inbound/http/errors.go).

## Events: CloudEvents 1.0 is MANDATORY
type `com.retailnetwork.logistics.last-mile-delivery.<entity>.<Event>` (entity: wave, route, packageattempthistory); source `/retailnetwork/last-mile-delivery`; dataschema `urn:retailnetwork:last-mile-delivery:<stream>:<Event>:v1`;
seven events on `retailnetwork.last-mile-delivery.events`, `RoutePlanned` ONLY on `.analytics`; subject and Kafka key = aggregate id (tracking number for the history events); header `content-type: application/cloudevents+json; charset=UTF-8`.
Use internal/adapters/kafka/cloudevents only. Transactional outbox: aggregates + outbox rows in ONE Postgres transaction. Consumers: full-type dispatch, tolerant decoding, inbox `processed_events` in the same transaction as the effect, retry then `<topic>.dlq`.
A new event: domain struct + apis/asyncapi.yaml + ADR 0004 `type:` line in the SAME commit + encoder payload + golden test (.claude/skills/how-to-add-an-integration-event).

## Domain
Aggregates, invariants and assumptions A1-A29 are in docs/adr/0002; consumed contracts and read models in 0003; envelope catalogue in 0004; runtime in 0005. Gap decisions: G5 (DeliveryWaveCancelled is an integration event), G11 (stops grouped by stopKey), G16 (deferral + third failure return), G17 (cancel re-inducts), G34 (partner deactivation, route only from INDUCTION_CLOSED, wave completes with its last route).

## Workflow
GitFlow: feature/* -> PR into develop, CI green before merge, never push develop or main. lefthook: pre-commit fmt/vet/lint, pre-push `make check`. Commit and push after each step.
Chart: charts/last-mile-delivery (api only; renders exactly the env cmd/api reads; refuses empty database, brokers or `*_CONSUMER_GROUP`, any `*_MODE`, and a grace period below `config.shutdownDrainDelay` + 50s). Tests: charts/last-mile-delivery/tests; docs/before-first-release.md rows 5-9 are done, row 11 (registry secrets) is for the repo admin.
