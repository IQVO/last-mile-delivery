---
id: testing
title: Testing
sidebar_label: Testing
sidebar_position: 1
---

# Testing

The test pyramid as it is on `develop`. The pre-push gate is `make check`; CI runs the wider set below. All Go tests live next to the code they test, except the godog features (`features/`) and the architecture fitness tests (`internal/architecture/`). No Go file sits at the repo root (`TestNoGoFilesAtRepoRoot`).

## The pyramid

| Layer | Where | What it proves | Run it |
| --- | --- | --- | --- |
| **Unit (domain)** | `internal/domain/{shared,wave,route,partner,history}/*_test.go` | aggregate invariants and state machines, snapshot rebuilds; no I/O | `make test` |
| **Unit (use cases)** | `internal/application/usecases/*_test.go` over the in-memory doubles (`internal/adapters/outbound/memory`) | every use case, fault paths (`faults_test.go`), the consumers' effects (`consumers_test.go`), the business counter (`attempt_metrics_test.go`) | `make test` |
| **Handler / adapter** | `internal/adapters/inbound/http` (handlers, idempotency, NUL rejection, readiness), `.../inbound/kafka` (consume loop, dead letter, tracing), `.../inbound/mcp` (tools, governance), `.../inbound/a2a` (card, skills), `.../outbound/{kafka,outbox,telemetry,system}`, `internal/adapters/kafka/cloudevents`, `cmd/*` (config, wiring, telemetry, router) | adapters in isolation with fakes; HTTP via `httptest` | `make test` |
| **BDD (godog)** | `features/*.feature`: `waves`, `routes`, `dispatch`, `partners`, `api` (**5 files, 36 scenarios**), runner `features/features_test.go` (`TestFeatures`) with steps in `steps_test.go`, `steps_dispatch_test.go`, `steps_http_test.go` and the `world_test.go` fixture | business behaviour through the use cases and through the HTTP API on the in-memory doubles | `make bdd` |
| **Integration** | build tag `integration`: 9 files, 16 test functions: `internal/adapters/outbound/postgres/*_integration_test.go` (repositories, migrations, idempotency store, outbox trace), `cmd/api/{wiring,e2e}_integration_test.go`, `cmd/mcp/main_integration_test.go`, `cmd/a2a/main_integration_test.go` | real Postgres and real Kafka, started by testcontainers (helpers in `internal/testing/pgtest` and `internal/testing/kafkatest`); `cmd/api` boots `run(ctx)`, drives REST and Kafka only, and stops Kafka to prove `/readyz` answers 503 `dependency-unavailable` while `/healthz` stays 200 | `make integration` (needs Docker) |
| **Contract** | `internal/adapters/outbound/repocontract` (one repository contract run against both the in-memory doubles and Postgres, so the doubles cannot drift); `internal/adapters/inbound/mcp/testdata/tool_registry.golden.json` (the MCP tool registry is golden-pinned); the encoder golden tests in `internal/adapters/outbound/kafka/encoder_test.go`; the event catalogue test against `apis/asyncapi.yaml` (`TestEventCatalogueMatchesContract`) | published contracts match the code | `make test`, `make arch-test` |
| **Mutation** | `.gremlins.yaml`, `internal/domain/*` | tests assert, not just execute. Measured 2026-10-23: efficacy 100 %, mutant coverage 100 %, thresholds 95 / 95 (set strictly below the measurement) | `make mutation` |
| **Architecture fitness** | `internal/architecture/*_test.go` (15 files) | the rules below | `make arch-test` |
| **Web** | `web/` (lint, `tsc -b`, vitest, build) | the operator micro-frontend | CI job `web` |

## What the fitness tests pin

| Test | Rule |
| --- | --- |
| `TestHexagonalArchitecture` | the domain imports no outer layer; adapters do not reach sideways into siblings |
| `TestRuntimeBinaryHasNoInMemoryAdapters`, `TestRuntimeBinaryDoesNotImportTheRepositoryContract` | `cmd/**` never links the memory doubles or the repository contract |
| `TestNoGoFilesAtRepoRoot` | the godog runner lives in `features/`, not at the root |
| `TestCloudEventsOnly`, `TestNoEventEnvelopeToggleOrFlatEnvelope`, `TestEventCatalogueMatchesContract` | every message is a CloudEvents 1.0 envelope built by `internal/adapters/kafka/cloudevents`; no toggle for a flat envelope; the catalogue matches `apis/asyncapi.yaml` |
| `TestKafkaWritersSetRequiredAcks`, `TestKafkaWriterTopicNotSetTwice`, `TestDeadLetterCallsGuardCancellation` | producers use acks=all; a writer sets its topic once (per message, never on both the Writer and the Message); dead-letter calls guard context cancellation |
| `TestKafkaConsumerGroupNeverHardcodedInline`, `TestReplayConsumersSetCommitInterval` | consumer group ids come from the environment; commit interval stays unset so commits are synchronous |
| `TestOutboxRelaysPassAttemptCap` | every relay is built with `WithMaxAttempts` |
| `TestMCPAdapterDependencyRule`, `TestMCPServerIsReadOnly`, `TestMCPOptionalToolInputsCarryOmitempty` | the MCP adapter wires only read use cases and carries read-only annotations |
| `TestA2AAdapterDependencyRule`, `TestA2AAgentIsReadOnlyAndHasNoOutboundClient` | the A2A agent is read-only and has no outbound client |
| `TestNoAuthMiddlewareReintroduced` | no bearer/JWT check under `internal/adapters/inbound` |
| `TestKafkaIntegrationTestsUseTestcontainers`, `TestPostgresIntegrationTestsUseTestcontainers` | integration tests start their own containers; none is gated by `DATABASE_URL` or `KAFKA_BROKERS` or skips |

Each rule has a companion `...SensorBites` / `...FailsOnBadFixtures` test that feeds a bad fixture and asserts the rule fails, so a sensor cannot pass vacuously.

## `make` targets

| Target | Runs |
| --- | --- |
| `make check` | `fmt-check`, `vet`, `build`, `lint`, `test` (`go test ./... -race`): the pre-push gate |
| `make check-all` | `check` + `coverage` + `arch-test` + `bdd` |
| `make check-fast` | `fmt-check`, `vet`, `arch-test`, then the tests of the changed packages (agent stop hook) |
| `make test` | `go test ./... -race` |
| `make coverage` | the same with `-coverpkg=./internal/domain/...,./internal/application/...`; fails below 90 % |
| `make bdd` | `go test ./... -run TestFeatures -v` |
| `make arch-test` | `go test ./internal/architecture/... -v` |
| `make integration` | `go test -tags=integration ./... -race -count=1` (Docker) |
| `make mutation` | gremlins per `./internal/domain/*/` package; `make mutation-full` runs `./internal/domain` with `--workers 1 --timeout-coefficient 30` |
| `make vuln` | `govulncheck ./...` |
| `make lint` | `golangci-lint run --allow-parallel-runners ./...` (v2.14.0 in CI) |
| `make guide-lint`, `make harness-test` | agent-guide and hook checks |

lefthook runs `fmt-check`, `vet` and `lint` on pre-commit and `make check` on pre-push (`lefthook install` once per clone).

## CI jobs (`.github/workflows/ci.yml`)

Triggers: push and pull request on `main` and `develop`, a weekly Monday 06:00 UTC schedule, and manual dispatch.

| Job | What it does |
| --- | --- |
| `lint` | golangci-lint v2.14.0 |
| `guide-lint` | agent-guide linter and hook tests (advisory, `continue-on-error`) |
| `complexity` | gocyclo, gocognit, cyclop, funlen, nestif via golangci-lint |
| `test` | `go build`, `go vet`, then `make coverage` (the only place the coverage scope and gate are defined) |
| `bdd` | `go test ./... -run TestFeatures -v` |
| `integration` | `go test -tags=integration ./... -race -count=1` (testcontainers) |
| `mutation-fast` | gremlins v0.6.0 over every `internal/domain/*/` package; blocking |
| `mutation` | exhaustive gremlins run; schedule and manual only; opens a `harness:red` issue on a scheduled failure |
| `drift` | weekly advisory: dead code, `go mod tidy -diff`, knip on `web/`, coverage-quality; never blocks |
| `api-lint` | Spectral on `apis/openapi.yaml` and `apis/asyncapi.yaml` (`--fail-severity=warn`) |
| `vuln` | `govulncheck ./...` |
| `helm-lint` | `ct lint` and the selector test (pull requests only) |
| `chart-tests` | every `charts/last-mile-delivery/tests/*.sh` and `test_*.py`; fails on `SKIP` |
| `arch-test` | the fitness tests |
| `web` | lint, `tsc -b`, tests and build of `web/` against `warehouse-ui-kit` |
| `docker-publish`, `release` | push to `main` only: signed image, SBOM, chart `.tgz` to `oci://ghcr.io/iqvo`, tag and GitHub release |

This repository's documentation site has its own workflow, `.github/workflows/docs.yml` (builds on pull requests, deploys on push to `main`); there is no `docs-api-drift` job in `ci.yml`.

## Writing a test

- A new use case: add a unit test over the in-memory doubles (`helpers_test.go` has the fixture), a fault case in `faults_test.go`, and a scenario in `features/` if it is user-visible.
- A new event: domain struct + `apis/asyncapi.yaml` + ADR 0004 `type:` line in the **same commit**, an encoder payload and a golden test; `TestEventCatalogueMatchesContract` fails otherwise.
- A new repository method: extend the contract in `internal/adapters/outbound/repocontract` so both the double and Postgres are exercised.
- Never gate an integration test on an environment variable or `t.Skip`; start the container with `pgtest` or `kafkatest`.
