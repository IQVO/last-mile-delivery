# ADR 0014: delivery-point profile (outcome detail, DeliveryPointProfile, DeliveryPointFlagged; roadmap C5)

## Status

Accepted (2026-11-11).

## Context

Wave 3 of the network evolution (roadmap item C5) wants the network to learn which
drop-off points fail repeatedly, so that a planner and, later, the shopper can be
told. The plan assumed more than this context can support, so the slice is reduced
to what the code can honour (the Wave 3 design, section 2):

- **The outcome vocabulary does not exist.** An attempt is `DELIVERED` or `FAILED`
  and `failureReason` is free text (1 to 200 characters, required exactly when
  `FAILED`). The model defines no vocabulary. Parsing the text to guess a category
  is forbidden: it would invent a contract and would copy free text, which can
  hold personal data, into a new table.
- **There is no stop sequencer.** The caller (the station planner) sends the stops;
  `route.Plan` only validates them. A profile therefore cannot reorder a route and
  must not refuse one.
- **No entrance, parking or locker knowledge exists** in any event or table; locker
  and pickup belong to a later item that needs its own model amendment.
- **No address, name or free text may enter any new event, table or log line.** The
  stop is identified only by the opaque `stopKey` (an HMAC derived by
  shipping-and-tracking at label time); the profile stores that key and counters.

The alternatives weighed: (a) add new `outcome` values (`NOT_HOME`, ...): rejected,
the wire `outcome` is consumed by shipping-and-tracking and by the analytics CHECK,
which would treat an unknown value as a failure or reject it; (b) build the profile
from our own `DeliveryAttempted` events through a second consumer group: rejected,
it is lagged, adds a consumer, and the data is already in hand inside
`RecordAttempt`; (c) a sweeper that decays strikes: rejected, this context has no
periodic job, so decay is evaluated lazily at the next observation.

## Decision

1. **Outcome detail (additive, optional, dormant until a client sends it).** The
   attempt gains `outcomeDetail`: for `FAILED` one of `NOT_HOME`, `ACCESS_PROBLEM`,
   `OTHER`; for `DELIVERED` `SAFE_PLACE_USED`. Empty means "not reported" (a legacy
   client) and is NOT the same as `OTHER`. `failureReason` stays required for
   `FAILED` and forbidden for `DELIVERED`; a detail never replaces it. A mismatch of
   detail and outcome is `422 validation-failed`. `outcomeDetail` is on the REST
   request and response, the MCP and A2A `get_route` attempts, the
   `DeliveryAttempted` event (optional, omitted when empty, same type, dataschema
   `:v1` and topic) and in `route_attempts.outcome_detail` (migration 0004).
2. **Aggregate `DeliveryPointProfile`** (`internal/domain/deliverypoint`), root id =
   the `stopKey`, entity segment `deliverypointprofile`. Counters `attempts`,
   `delivered`, `safePlaceUsed`, `failedNotHome`, `failedAccess`, `failedOther`
   (saturating at `shared.MaxInt32`), a ring of at most `MaxStrikes` strikes
   `{routeId, trackingNumber, category, at, notified}`, `flaggedAt`,
   `lastAttemptAt` and the version. Named constants: `FlagAfterStrikes = 2`,
   `StrikeWindow = 60 days`, `MaxStrikes = 5`; only `NOT_HOME` and `ACCESS_PROBLEM`
   are counted failures; `OTHER` and "not reported" are tallied, never counted.
   `Restore` validates: a stopKey that is valid and does not start with `pkg:`;
   `attempts == delivered + failedNotHome + failedAccess + failedOther`;
   `safePlaceUsed <= delivered`; at most `MaxStrikes` strikes, each a counted
   category, no two with one `routeId`; `flaggedAt` is non-zero exactly when the
   strikes reach `FlagAfterStrikes`.
3. **The one command `Observe`** (steps in order): (1) `ref = max(at, lastAttemptAt)`,
   drop strikes older than `ref - StrikeWindow`, clear the flag when fewer than
   `FlagAfterStrikes` remain; (2) tally the attempt (`OTHER` and empty both count as
   `failedOther`); (3) a counted failure whose `routeId` has no strike appends one
   (two parcels failing at one stop on one route are ONE strike, one visit) and the
   ring is trimmed oldest-first; (4) a `DELIVERED` attempt ends the episode (clears
   strikes and flag) unless a strike of the same `routeId` exists; (5)
   `lastAttemptAt = max(...)`, version up; (6) while at least `FlagAfterStrikes`
   strikes are open, the distinct tracking numbers of strikes not yet notified (and
   with no notified strike of their own number) are marked notified and raise ONE
   `DeliveryPointFlagged` carrying only those numbers and `failures = len(strikes)`;
   `reason` is `ACCESS_PROBLEM` when any open strike has it, else `NOT_HOME`. A later
   parcel of another order failing at an already flagged point is therefore told too,
   once.
4. **Event `DeliveryPointFlagged`** (integration, topic
   `retailnetwork.last-mile-delivery.events`): type
   `com.retailnetwork.logistics.last-mile-delivery.deliverypointprofile.DeliveryPointFlagged`,
   source `/retailnetwork/last-mile-delivery`, dataschema
   `urn:retailnetwork:last-mile-delivery:events:DeliveryPointFlagged:v1`, subject and
   Kafka key = the `stopKey`. `data`: `stopKey`, `reason` (`NOT_HOME` or
   `ACCESS_PROBLEM`), `failures` (integer, at least 2), `trackingNumbers` (1 to 5, the
   newly affected packages only), `flaggedAt` (RFC 3339 UTC, the attempt instant that
   triggered it). No optional fields. No event means "no new package was affected".
   There is no "cleared" event.
5. **Persistence.** Migration 0005 creates `delivery_point_profiles` (stop key
   primary key with a `pkg:` CHECK, the counters, `strikes JSONB`, `flagged_at`,
   `last_attempt_at`, `version`). Port `ports.DeliveryPointRepository` with `Get`
   (`ErrNotFound`), `GetMany` (missing keys absent, never an error) and `Save` under
   the optimistic rule (create at version 1 expects absence; a unique violation is
   `ErrConcurrentUpdate`). No retention job in this slice: a row is about 300 bytes
   per distinct address.
6. **The use case.** `RecordAttempt.execute` calls `updateProfile` after
   `updateHistory`, in the same unit of work: no lag, no second consumer group. The
   key comes from `stopInfo`; a key starting with `pkg:` (a package with no label
   data) has no address key and gets no profile and no event. Counter
   `last_mile_delivery.delivery_point.flagged` (attribute `reason`).
7. **Read-only exposure, no influence.** `GET /delivery-points/{stopKey}`
   (`getDeliveryPoint`): 200 `DeliveryPoint`, 404 `not-found`, 422
   `validation-failed` for an invalid key. `WaveStop` gains an optional
   `deliveryPoint{flagged, openStrikes, safePlaceUses}` present only when a profile
   exists, loaded with one `GetMany` per page. The MCP `get_wave_stops` output gains
   the same object; the MCP charter stays read-only, no new tool, no A2A skill.
   **`PlanRoute` is unchanged**: no new error and no new status; the planner's
   invariants (`ErrRouteOverCapacity`, `ErrStopKeyMismatch`,
   `ErrPackageAlreadyRouted`, numbering) are untouched. A flagged point changes no
   ETA, no sequence and no promise.
8. **Stop key source.** The profile uses the `PackageStopView` key; a package
   inducted before its label data arrived is `pkg:<trackingNumber>` and never gets a
   profile. Rotating `STOP_KEY_SECRET` produces new keys: the old profiles are
   orphaned and learning restarts.

## Consequences

- C5 is **dormant** until the driver client sends `outcomeDetail`: before that the
  profile tallies attempts as "other", never counts a strike and never publishes.
  Activation is a client change, not a deploy flag.
- An invalid stop key on `Observe` cannot occur through the use case; `Observe`
  returns `shared.Invalid` for a malformed observation instead of guessing.
- The profile holds only outcome-derived facts, never entrance, parking or locker
  knowledge. No address, name, phone or free text is copied, and `failureReason` is
  not copied to the profile.
- Two routes failing at one point concurrently conflict on the profile version;
  `transact` retries once and the second run sees the first strike.
- Strikes decay lazily: a point that is never visited again keeps its row and its
  open flag (a row with no consumer interest); the open decision on retention is for
  the product owner.
- No new env var, topic, consumer group, chart value or secret. Migrations 0004 and
  0005 run at boot and are additive. Deploy order for the chain: customer-ordering,
  shipping-and-tracking, then this context, because a consumer commits past a type it
  has no handler for.
- `docs/adr/0004` lists the new type in its catalogue; the projector acknowledges it
  and needs no fact from it.
