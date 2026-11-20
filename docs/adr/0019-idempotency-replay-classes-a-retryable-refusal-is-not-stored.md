# ADR 0019: Idempotency replay classes — a retryable refusal is not stored

## Status

Accepted (2026-11-20). Implements the fleet decision of 2026-11-18
(warehouse-harness-template `docs/idempotency-replay-classes.md`). Supersedes the
sentence "A 2xx and a 4xx answer are stored (24 h)" of
[ADR 0010](./0010-adopt-the-fleet-rest-api-guideline.md) (decision "One
transaction") for the refusals classed `RETRYABLE` below; the rest of ADR 0010
stands. Does not edit other ADR bodies.

## Context

`RequireIdempotencyKey` (`internal/adapters/inbound/http/idempotency.go`) runs
the request through the port `idempotency.Store` (`Do(ctx, key, hash, fn)`). The
store stored every answer below 500, so a refusal that depends on state that can
change was pinned under its key for 24 hours: a `409 concurrent-update` replayed
to a client that did the right thing and retried, or a `422 unknown-station`
replayed after node-topology had delivered the station.

Phase-0 facts, read from the code:

- **Postgres store** (`outbound/postgres/idempotency.go`, `Do`): begins a
  transaction, takes `pg_advisory_xact_lock(fnv64(key))` (`:79`) so duplicates
  serialise, reads the key within 24 h (`:84-85`) and replays it; otherwise it
  opens `SAVEPOINT handler_run` (`:92`) and runs the handler with the transaction
  in the context. A status of 500 or above returns without writing, and the
  deferred `Rollback` drops the key row and every write of the handler (`:98`).
  Otherwise `healAbortedHandler` (`:46-70`) releases the savepoint (rolling back
  to it only when a statement aborted the transaction), the row is written with
  `INSERT ... ON CONFLICT (idempotency_key) DO UPDATE` (`:108-114`) and the
  transaction commits (`:116`). It stores the status, content type and body, no
  other header.
- **A stored refusal keeps the handler's writes.** `UnitOfWork.Do` joins the
  store's transaction without a savepoint of its own (`postgres/unit_of_work.go`
  `Do`; `ports.UnitOfWork`: "the outermost Do owns commit and rollback"), so a
  use case that wrote and then returned an error leaves its writes in the
  transaction and a stored 4xx commits them. ADR 0010 relied on "the use cases
  validate before they save"; the audit below checks it per problem type.
- **Memory store** (`outbound/memory/idempotency.go`): the same contract, one
  mutex over lookup and handler; its use cases' units of work restore the state
  on an error.
- **The hook did not exist**: the store's only "do not store" input was
  `status >= 500`.
- The fingerprint is method, path and body hash; no other header.
- No outbound caller sends a deterministic key (this context calls no sibling);
  MCP and A2A are read-only (no write tool, no store).

## Decision

1. **Every problem type has exactly one replay class**, a field of its catalogue
   entry (`problem.class`, `errors.go`: a fourth field of the `problem` literal,
   so the `{err, problem}` slice and the problems written outside it declare it
   in one place):
   - `FINAL`: the answer is a function of the request alone, or of a fact that
     never changes back. Stored and replayed.
   - `OUTCOME`: the command committed state and then answered an error that must
     be remembered. Stored, and its writes commit with the stored answer.
   - `RETRYABLE`: nothing committed and the answer depends on state that can
     change. NOT stored: the key is released and a retry executes again.
2. **The middleware reads the class and holds no list of slugs.** The error
   writer (`writeProblem`) marks the request context with the class
   (`replay_class.go`); the middleware sets `idempotency.Record.Transient` from
   it: a 2xx is stored, a 5xx never is, every other answer is stored only when
   its class is `FINAL` or `OUTCOME`. A refusal that declared no class (written
   outside the catalogue) is released like `RETRYABLE`. The stores gain one
   condition: `Transient` is treated exactly like the 5xx branch (return
   unstored, the deferred rollback drops the key row and every write), in the
   Postgres and the memory store alike.
3. **A problem type without a class fails a unit test**
   (`TestEveryProblemTypeDeclaresAReplayClass`); an AST test fails a `problem`
   declared anywhere that `ReplayCatalogue` does not list; the table below is held
   equal to the catalogue by `TestReplayClassTableMatchesTheCatalogue`; the
   OpenAPI text is held equal by `TestOpenAPIListsTheRetryableRefusals`; a test
   fails a slug named in `idempotency.go` or `replay_class.go`.
4. **Releasing a key makes a different body under it a new request**, not
   `idempotency-key-reused`: nothing is left to compare against.
5. **A concurrent duplicate while the first ends `RETRYABLE`**: it waits on the
   advisory lock; the first request rolled back, so the second finds no row and
   EXECUTES as a fresh request (not an error, not a replay). It sees a replay
   only when the first ended `FINAL`, `OUTCOME` or 2xx. In memory the mutex
   serialises them the same way.
6. **`RELEASED` is not a retry policy.** Releasing a key only lets whoever sent
   the request try again; nothing in this repository retries (no consumer-side
   or client-side behaviour is added; the web remote is read-only).

### OUTCOME: none in this service

An `OUTCOME` needs a refusal that provably commits state. None does. Every
problem type is raised before the first write of its use case, or is `RETRYABLE`
(a full rollback) when it can follow a write:

- `ScheduleWave` (`waves.go:31-57`): `requireStation` and `wave.Schedule` before
  `Waves.Save`; after the save only the sweep can fail, with `not-found`,
  `concurrent-update` or `illegal-transition` (all `RETRYABLE`).
- `CloseInduction`, `CancelWave`, `DeclareWaveCost`, `DeclareWaveEmission`,
  `DispatchWave`: `mutateWave` loads and applies the command before `Save`
  (`waves.go:63-77`); the later re-induction can only fail with `not-found`
  (`induct.go:86`, a package without a history), `concurrent-update` or
  `illegal-transition`.
- `PlanRoute` (`routes.go:30-62`), `StartRoute` (`:70-100`): every check precedes
  `Routes.Save`.
- `RecordAttempt` (`routes.go:136-185`): `r.RecordAttempt` (the aggregate's own
  checks, in memory) and, for a delivery, `h.RecordDelivered` (`:190-198`) run
  before the first write. The failure path writes a wave (`place`,
  `induct.go:57-76`) before `h.RecordFailure` (`routes.go:214`) can answer
  `illegal-transition`: `RETRYABLE`, so the induction is rolled back with the key.
- `ContractPartner`, `DeactivatePartner`: checks before `Partners.Save`.

So the class exists in the vocabulary and the mechanism (a stored refusal keeps
the handler's writes, in both stores) is proven with a synthetic problem that is
not in the catalogue (`SyntheticOutcomeEntry`, run by the harness suite over the
memory store and, in the integration build, over Postgres). The one conceivable
`FINAL` refusal after a write is `validation-failed` from `Induct` in
`reinduct` on persisted data (`wave_induct.go:21-26`); persisted values were
validated when written, so it is unreachable (a corrupt row is a 500). A future
refusal that commits state must be declared `OUTCOME` in the PR that adds it.

## Class table

The table is held equal to the catalogue by test (31 rows: the 21 catalogue
entries plus the 10 problems written outside the catalogue slice).
"Request fields" means the answer is computed from the request alone before any
state is read. Rows marked JUDGEMENT are explained after the table.

| Problem type | Status | Class | Reason |
| --- | --- | --- | --- |
| `validation-failed` | 422 | FINAL | JUDGEMENT 2. Body, path and query values (`decodeJSON`, `errMalformedRequest`, `shared.Invalid`); the one state-reading cause is `COLLECTED` needing a PICKUP induction (`route.go:206`), whose inputs are immutable. |
| `invalid-wave-times` | 422 | FINAL | Request fields (`wave.go:176-182`): `cutoff < start < end`. |
| `invalid-van-capacity` | 422 | FINAL | Request fields (`partner.go:63-64`). |
| `unknown-station` | 422 | RETRYABLE | `writer.go:121-131`: the station is a fact node-topology delivers later (`StationView`). |
| `not-found` | 404 | RETRYABLE | JUDGEMENT 4. Mixed slug: the command's own target (ids minted here, never deleted) and referenced facts that another request or an event delivers (`routes.go:32`, `:36` wave and partner of the body; `routes.go:191`, `induct.go:86` the package history a consumer creates). |
| `package-not-on-route` | 404 | RETRYABLE | JUDGEMENT 1. `route.go:178`; immutable by code (route stops never change), kept RETRYABLE. |
| `already-delivered` | 409 | FINAL | JUDGEMENT 3. `route.go:182` (an attempt of this package on this route was DELIVERED; attempts are append-only) and `history.go:185-192` (DELIVERED is terminal). |
| `attempt-already-recorded` | 409 | FINAL | JUDGEMENT 3. `route.go:184`: attempts are append-only. |
| `package-already-routed` | 409 | FINAL | JUDGEMENT 3. `route_planning.go:67-68`: a route's packages are never removed and no route is deleted. |
| `induction-closed` | 409 | FINAL | JUDGEMENT 3, 6. `wave_induct.go:27-28`: the close is one-way and the cutoff fixed; not reachable on a keyed route (`induct.go:65` swallows it). |
| `already-inducted` | 409 | FINAL | JUDGEMENT 3, 6. `wave_induct.go:30-31`: inductions are append-only; not reachable on a keyed route (`induct.go:68`). |
| `package-not-inducted` | 409 | RETRYABLE | JUDGEMENT 1. `route_planning.go:64-65`; immutable once the wave is closed, kept RETRYABLE. |
| `stop-key-mismatch` | 409 | RETRYABLE | JUDGEMENT 1. `route_planning.go:70-71`; stop keys come from label data, kept RETRYABLE. |
| `route-over-capacity` | 409 | RETRYABLE | JUDGEMENT 1. `route_planning.go:49-50` against the partner's van capacity; kept RETRYABLE. |
| `partner-inactive` | 409 | RETRYABLE | JUDGEMENT 1. `route_planning.go:43-44`; deactivation is one-way (`partner.go:92-96`), kept RETRYABLE. |
| `partner-station-mismatch` | 409 | RETRYABLE | JUDGEMENT 1. `route_planning.go:46-47`. |
| `route-not-started` | 409 | RETRYABLE | `route.go:174-175`: the route moves PLANNED to OUT_FOR_DELIVERY (`StartRoute`), so an attempt that is refused now passes after the start. |
| `route-incomplete` | 409 | RETRYABLE | JUDGEMENT 6. `route.go:236`: a state not reached yet; not reachable on a keyed route (`routes.go:224` completes only when every package has an attempt). |
| `wave-full` | 409 | RETRYABLE | JUDGEMENT 6. `wave_induct.go:34-35`; not reachable on a keyed route (`induct.go:65` skips a full wave). |
| `illegal-transition` | 409 | RETRYABLE | The aggregate state is not the one the command needs (`wave.go:245-248`, `route_start.go:19-20`, `partner.go:92-96`, `wave_declarations.go:22-23`, `history.go:131-132`): the state it waits for may be reached by another request or event. A slug that mixes terminal and not-yet-reached states. |
| `concurrent-update` | 409 | RETRYABLE | `ports.ErrConcurrentUpdate`: an optimistic-lock loss (`postgres/waves.go:154,164` and the other repositories); `transact` retries once in the unit of work (`writer.go:97`) and reports the second loss. |
| `idempotency-key-required` | 400 | FINAL | Request header, answered before the store (`idempotency.go:77`), never reaches it. |
| `idempotency-key-invalid` | 400 | FINAL | Request header, answered before the store (`idempotency.go:79`). |
| `idempotency-key-reused` | 409 | RETRYABLE | JUDGEMENT 5. Answered by the middleware on a stored row (`idempotency.go:116`), never stored itself; the row it compares against is released by a RETRYABLE answer. |
| `invalid-limit` | 400 | FINAL | Request fields (GET only, `handlers_pages.go`). |
| `invalid-cursor` | 400 | FINAL | Request fields (GET only). |
| `unknown-query-parameter` | 400 | FINAL | Request fields (GET only). |
| `resource-not-found` | 404 | FINAL | A path that names no resource (`nul.go`, the router's `NotFound`); never on a keyed route. |
| `method-not-allowed` | 405 | FINAL | The method does not exist for the path (`route_problems.go`); never on a keyed route. |
| `dependency-unavailable` | 503 | RETRYABLE | `/readyz` only (`readiness.go:106`); a 5xx is never stored. |
| `internal-error` | 500 | RETRYABLE | An unmapped error; a 5xx is never stored. |

### Judgement calls

1. **Kept `RETRYABLE` although the code makes the cause immutable** (most
   consequential: it chooses the cheaper error). `package-not-on-route`,
   `package-not-inducted`, `stop-key-mismatch`, `route-over-capacity`,
   `partner-inactive`, `partner-station-mismatch`. In today's code a route's
   stops, a closed wave's inductions, a partner's station and van capacity never
   change and `ACTIVE` to `INACTIVE` is one-way (`partner.go:92-96`), so a stored
   refusal could not go stale. The default of the rule applies (a released refusal
   costs one more execution; a wrongly `FINAL` one pins a stale refusal for 24 h
   the day a route can be amended or a partner reactivated). Flipping any of them
   to `FINAL` is one word in `errors.go` and one table row.
2. **`validation-failed` is `FINAL`.** Its causes are the body, path and query. The
   only cause that reads a stored fact is `outcome COLLECTED needs a PICKUP
   induction` (`route.go:206`, `routes.go:150-157`): the package's induction kind
   in a wave is append-only and the route's stops are fixed, so the answer for a
   given route and package never changes.
3. **`FINAL` by a terminal or append-only fact**: `already-delivered`,
   `attempt-already-recorded`, `package-already-routed`, `induction-closed`,
   `already-inducted` (cites in the table). None can become false: no row of
   `route_stop_packages`, `wave_inductions`, attempts or histories is ever deleted
   (no `DELETE` in `internal/adapters/outbound`).
4. **`not-found` is `RETRYABLE`** because the slug is shared with referenced facts
   that another request or an event delivers later: the wave and the partner of a
   `POST /routes` body, and the package history a consumer creates on arrival
   (`RecordAttempt`, `reinduct`). Splitting it is a follow-up, never part of this PR.
5. **`idempotency-key-reused` `RETRYABLE`, the two key-header problems `FINAL`**:
   none of the three reaches the store; the classes only keep the table complete
   and consistent with the fleet.
6. **Classes of problem types no keyed route can answer today** (`wave-full`,
   `induction-closed`, `already-inducted`, `route-incomplete`): `place` swallows
   the first three (`induct.go:57-76`) and the route completes only when every
   package has an attempt (`routes.go:224`). They are classified by meaning so the
   first route that surfaces one needs no new decision.

## Consequences

- A `RETRYABLE` refusal is answered once and forgotten: the same request retried
  under the same key executes again and, once the state allows it, succeeds and
  is stored and replayed. A different body under the released key is a new
  request. Callers that treated `409 idempotency-key-reused` as the answer to a
  reused key after a refusal now get their retry executed.
- 2xx, 5xx and `FINAL` answers are byte-identical to before; wire slugs, titles
  and statuses are unchanged.
- Not changed here (open): retention. The age check on replay exists (24 h) but
  no job deletes old rows of `idempotency_keys`.
