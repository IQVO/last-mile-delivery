---
paths:
  - "internal/adapters/inbound/http/**"
  - "apis/openapi*.yaml"
  - "apis/openapi/**"
---

# REST API (inbound adapter)

Route -> use case, kept in sync with `apis/openapi.yaml` (registered in
`internal/adapters/inbound/http/server.go`, handlers in `handlers.go`, wired in
`cmd/api/wiring.go` `buildServer`):

- `POST /waves` -> `ScheduleWave` (201)
- `GET /waves?stationNodeId=&serviceDate=` -> `ListWaves` (`{"items":[...]}`)
- `GET /waves/{waveId}/stops` -> `GetWaveStops` (`{"items":[...]}`)
- `POST /waves/{waveId}:close-induction` -> `CloseInduction`
- `POST /waves/{waveId}:dispatch` -> `DispatchWave`
- `POST /waves/{waveId}:cancel` -> `CancelWave` (body `{"reason"}`)
- `POST /routes` -> `PlanRoute` (201)
- `GET /routes/{routeId}` -> `GetRoute`
- `POST /routes/{routeId}:start` -> `StartRoute`
- `POST /routes/{routeId}/attempts` -> `RecordAttempt` (201)
- `POST /partners` -> `ContractPartner` (201)
- `POST /partners/{dspId}:deactivate` -> `DeactivatePartner`
- `GET /healthz` (liveness), `GET /readyz` (readiness): not business operations.

## Conventions

- **Errors** are RFC 9457 `application/problem+json`: `type` is a kebab-case slug
  (`internal/adapters/inbound/http/errors.go`, `problemCatalogue`), plus `title`,
  `status`, `detail`, `instance`. Shared: `not-found` 404, `illegal-transition` 409,
  `concurrent-update` 409, `validation-failed` 422; one slug per model `Err...`
  (`route-over-capacity`, `already-delivered`, `invalid-wave-times`, ...); an
  unmapped error is an opaque 500 `internal-error`. A new `Err` needs a catalogue
  row and a row in `TestProblemMapping` (`handlers_test.go`).
- **Idempotency-Key** is required on all nine state-changing routes
  (`idempotency.go`, store `postgres/idempotency.go`, table `idempotency_keys`,
  24 h): same key + same body replays the stored 2xx with
  `Idempotent-Replayed: true`, same key + other body is 422
  `idempotency-key-reused`, a missing key 400 `idempotency-key-required`; the key
  is scoped to `METHOD path`; only 2xx responses are stored; concurrent requests
  with one key run once (advisory lock). A new state-changing route goes in the
  `r.Group` of `server.go` that applies the middleware, never outside it.
- **NUL** (U+0000) is rejected before anything else (`nul.go`): 404 in the path,
  422 `validation-failed` in the query or body.
- Bodies are decoded strictly (`decodeJSON`): unknown fields, an empty body and
  trailing data are 422 `validation-failed`.
- **No PII**: no request or response carries an address or a name; packages are
  tracking numbers, stops are opaque `stopKey` groups.
- **Handlers hold no business logic**: decode, call a use case, encode the view.
- **Auth**: none. REST is unauthenticated, as in the rest of the network
  (`internal/architecture/fitness_test.go` `TestNoAuthMiddlewareReintroduced`
  keeps it so).
- Unit tests (`handlers_test.go`, `idempotency_test.go`) run the real use cases
  over the memory test doubles; the Postgres store is proven in
  `internal/adapters/outbound/postgres/idempotency_integration_test.go` and the
  whole stack in `cmd/api/e2e_integration_test.go`.
