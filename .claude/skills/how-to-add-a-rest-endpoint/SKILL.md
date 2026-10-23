---
name: how-to-add-a-rest-endpoint
description: Add or change a REST endpoint in this service in the hexagonal order (domain invariant, use case, handler, apis/openapi.yaml, godog scenario). Use when touching internal/adapters/inbound/http, apis/openapi.yaml, or exposing a use case over HTTP.
---

# How to add a REST endpoint

Follow this order: domain first, adapter last. A handler written before the
invariant it relies on validates nothing and gets bypassed.

The worked example is `POST /waves/{waveId}:cancel`:
`internal/domain/wave/wave.go` `Cancel` -> `internal/application/usecases/waves.go`
`CancelWave` -> `internal/adapters/inbound/http/handlers.go` `handleCancelWave`. The
read side example is `GET /routes/{routeId}` (`usecases/queries.go` `GetRoute`
-> `handleGetRoute`).

## 1. Domain first

Does the rule already exist in `internal/domain/<wave|route|partner|history>/`?
A handler holds no business logic. A new rule goes into the aggregate with a
table-driven test in the same package (`wave_test.go`, `route_test.go`, ...)
and a named `Err...` variable. Pure Go: no I/O, `now` and ids passed in. Then
re-run the mutation gate for that package (`gremlins unleash ./internal/domain/<pkg>`).

## 2. Application: the use case

Add it to `internal/application/usecases/` (`waves.go`, `routes.go`,
`partners.go` for commands; `consumers.go` for event policies; `queries.go` for reads). A command
embeds `writer` (see `writer.go`): `transact` runs load, domain call, `Save` and
`publish` (outbox append) in ONE `ports.UnitOfWork` and retries once on
`ports.ErrConcurrentUpdate`. A query holds only its read port. Return a view
from `internal/application/views`. If a new repository method is needed, add it
to `internal/application/ports/ports.go` (interfaces only), implement it in
`internal/adapters/outbound/postgres/` AND `internal/adapters/outbound/memory/`,
and extend the shared suite in `internal/adapters/outbound/repocontract/` so both
run it. Unit-test the use case in `internal/application/usecases/*_test.go`.

## 3. Adapter: the handler

In `internal/adapters/inbound/http/`:

- add the request and response bodies to `dto.go` (strict decoding, unknown fields
  are 422 `validation-failed`);
- add the use case field to `Server` (`server.go`) and the handler to `handlers.go`;
- register a state-changing route inside the `r.Group` of `NewRouter` that applies
  the Idempotency-Key middleware; a read uses `r.Get` outside it;
- map a new domain `Err` in `errors.go` `problemCatalogue` (kebab-case slug, the
  status the OpenAPI documents) and add a row to `TestProblemMapping`
  (`handlers_test.go`);
- wire the use case in `cmd/api/wiring.go` `buildServer`
  (`TestBuildServerWiresEveryUseCase` fails until you do).

## 4. Contract: apis/openapi.yaml

Describe the operation, its `Idempotency-Key` parameter (writes), the success
body and every problem it can return, then lint:
`spectral lint apis/openapi.yaml --ruleset .spectral.yaml` (or
`npx --yes @stoplight/spectral-cli lint ...`)
(the `api-lint` CI job runs the same). Keep `.claude/rules/rest-api.md` and the
README table in step.

## 5. Behaviour

Add the happy path and each refusal to `features/*.feature` (`features/steps_test.go`
drive the use cases over the memory doubles; refusals are named by the error text)
and an HTTP case to `internal/adapters/inbound/http/handlers_test.go`. The real
middleware store and Postgres are proven by
`internal/adapters/outbound/postgres/idempotency_integration_test.go` and
`cmd/api/e2e_integration_test.go`.

## 6. Verify before opening the PR

`make check`, `make coverage`, `go test -tags=integration ./internal/adapters/outbound/postgres/ ./cmd/... -race -count=1`,
and the spectral lint above.
