# ADR 0010: Adopt the fleet REST API guideline

## Status

Accepted (2026-11-01). Supersedes the REST details of
[ADR 0005](./0005-runtime-outbox-http-and-readiness.md) that it names below
(per-route key, 422 on reuse, `Idempotent-Replayed`, bare `{items}` lists).

## Context

The fleet REST API guideline (warehouse-docs, rules R1 to R8, enforced by the
`rest-guideline` Spectral rule in `.spectral.yaml`) removes three divergences
between the services: command URLs written as `:verb` suffixes, an idempotency
contract that differed per service (the key scoped to a route, reuse answered
422, two spellings of the replay header) and unpaged lists. The cutover is
breaking by design and ships as one set across the fleet.

## Decision

- **Nouns, not verbs.** A lifecycle transition is a `POST` to a plural
  transition noun under the aggregate:

  | Old | New |
  | --- | --- |
  | `POST /waves/{waveId}:close-induction` | `POST /waves/{waveId}/induction-closures` |
  | `POST /waves/{waveId}:dispatch` | `POST /waves/{waveId}/dispatches` |
  | `POST /waves/{waveId}:cancel` | `POST /waves/{waveId}/cancellations` |
  | `POST /routes/{routeId}:start` | `POST /routes/{routeId}/starts` |
  | `POST /partners/{dspId}:deactivate` | `POST /partners/{dspId}/deactivations` |

  `operationId`s, status codes and bodies do not change; the old routes are
  gone (`TestRetiredRoutesAreGone`), with no alias.
- **Every POST is idempotent.** All nine POST routes sit behind
  `RequireIdempotencyKey`. The key is 1 to 128 printable ASCII characters:
  missing is 400 `idempotency-key-required`, malformed 400
  `idempotency-key-invalid`. The key is **global** (looked up by key alone);
  the fingerprint is method + path + SHA-256 of the body. Same key and
  fingerprint replays the stored answer with `Idempotency-Replayed: true`
  (`Idempotent-Replayed` is retired); same key and another fingerprint is
  **409** `idempotency-key-reused` (was 422).
- **One transaction.** `postgres.IdempotencyStore.Do` takes an advisory lock on
  the key, begins the transaction and hands it to the handler through the
  context, so each use case's `UnitOfWork.Do` joins it: the key row, the
  aggregate change and the outbox rows commit together. A 2xx and a 4xx answer
  are stored (24 h); a 5xx, an error or a panic rolls everything back and is
  never stored, so the retry re-executes. Migration `0002` drops the `route`
  column from `idempotency_keys` (primary key is the key alone) and adds
  `content_type`, so a replay keeps the `Content-Type`.
  Consequence: a handler that returned a 4xx after writing something would keep
  that write; the use cases here validate before they save, so none does.
- **Keyset paging.** `GET /waves` and `GET /waves/{waveId}/stops` take `limit`
  (1 to 500, default 100) and `cursor` and answer `{items, nextCursor}`. The
  cursor is the base64url JSON of the last row's sort key and id (package
  `internal/adapters/inbound/http/page`): waves order by `(inductCutoffAt,
  waveId)` in both the Postgres and the memory repository; stops by `(first
  induction time, stopKey)` (ties on the induction time now break by stopKey).
  Bad `limit` is 400 `invalid-limit`, an undecodable cursor 400
  `invalid-cursor`, any undeclared query parameter 400
  `unknown-query-parameter`.
- **Exemptions.** None: no operation is marked `x-pagination-exempt`.
- **MCP and A2A** keep their tool schemas and still return complete lists: the
  `Execute` of `ListWaves` and `GetWaveStops` walks the pages (500 rows each,
  at most 20 pages).
- **Consumers in this repo.** The web remote (`web/src/api.ts`) is read-only,
  sends no key, and follows `nextCursor` for both lists. No outbound HTTP client
  exists (this context calls no sibling).

## Consequences

- Clients must send an `Idempotency-Key` on every POST, read lists from `items`
  and loop on `nextCursor`, and treat 409 `idempotency-key-reused` as a bug.
- Deploy together with the rest of the REST cutover set.
