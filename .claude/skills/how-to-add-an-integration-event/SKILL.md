---
name: how-to-add-an-integration-event
description: Publish a cross-service Kafka event from this service: CloudEvents 1.0 type naming, AsyncAPI, the type catalogue ADR, the encoder, the transactional outbox. Use when touching internal/adapters kafka or outbox code, a domain event, or apis/asyncapi*.yaml.
---

# How to add an integration event

last-mile-delivery publishes eight events (seven on `retailnetwork.last-mile-delivery.events`,
`RoutePlanned` on `.analytics`) and consumes four from three producers (ADR 0003,
ADR 0004). The envelope is CloudEvents 1.0 only; read
`.claude/rules/integration-events.md` and `.claude/rules/fleet/cloudevents.md` first.
The worked example is `DeliveryWaveCancelled` (raised by `Wave.Cancel`, G5).

## Publishing a new event

1. **Domain event.** A typed struct in `internal/domain/<pkg>/events.go`
   (`wave.DeliveryWaveCancelled`): embeds `shared.Header`, fields exactly the model's
   `data` list, methods `EventName()`, `Entity()` (`wave`, `route`,
   `packageattempthistory`; lowercase, no separators) and `SubjectID()`, plus a
   `var _ shared.Event = ...` assertion. The aggregate command returns it.
2. **Contract, one commit.** Add the message and its `data` schema to
   `apis/asyncapi.yaml`, AND one `type: com.retailnetwork.logistics.last-mile-delivery.<entity>.<Event> stream: events|analytics`
   line to `docs/adr/0004-cloudevents-envelope-and-type-catalogue.md`.
   `TestEventCatalogueMatchesContract` (`internal/architecture/catalogue_fitness_test.go`)
   fails when the two disagree, so the push fails when they are not in the same
   commit. Lint: `npx --yes @stoplight/spectral-cli lint apis/asyncapi.yaml --ruleset .spectral.asyncapi.yaml`.
   Evolution is additive only; a breaking change is a new `:v2` dataschema.
3. **Encoder.** In `internal/adapters/outbound/kafka/encoder.go` add the payload struct
   (camelCase json tags, instants through `ts`, arrays never null) and a `case` in
   `payloadFor` that returns it with its stream. The envelope itself is built by
   `internal/adapters/kafka/cloudevents` only; never hand-build one. Add the event to
   `fixedEvents()` and its exact JSON to `golden` in `encoder_test.go`
   (`TestEveryPublishedEventHasAnExactGolden` compares byte for byte and counts both).
4. **Publish from the use case** through `writer.publish`
   (`internal/application/usecases/writer.go`) inside the same `ports.UnitOfWork` as
   the `Save`: the aggregate row and the outbox row commit or roll back together.
   Never publish after the commit and never call Kafka from a use case.
5. **Nothing else.** The relay (`internal/adapters/outbound/outbox/relay.go`, started in
   `cmd/api/main.go` with an inline `WithMaxAttempts`) drains every row. Integration
   events go ONLY to `outboundkafka.EventsTopic`, analytics-only events ONLY to
   `AnalyticsTopic`; the stream comes from `payloadFor`.
6. **Prove it.** Extend `cmd/api/e2e_integration_test.go` when the event is part of a
   REST flow: `go test -tags=integration ./cmd/api/ -race -count=1` boots `run(ctx)`
   over real Postgres and Kafka and reads the CloudEvents back.

## Consuming an event from a sibling context

Today: `LoadArrived` (linehaul-transportation), `NodeRegistered` and
`ServiceAreaAssigned` (node-topology), `LabelIssued` (shipping-and-tracking), all
listed in ADR 0003. To add one:

1. Re-read the producer's contract first:
   `git -C ~/warehouse-systems/<producer> show origin/feature/service:apis/asyncapi.yaml`.
   Record the type and the fields you rely on in ADR 0003 (a new ADR when it changes a decision).
2. Constant for the FULL `type` and a tolerant data struct (only the fields you read)
   in `internal/adapters/inbound/kafka/consumers.go`; a handler in the map of the
   topic's `*Handlers` function. Dispatch is on the full type; unknown types and
   unknown fields are ignored. Bad payloads raise `errBadPayload` (dead-lettered).
3. A use case in `internal/application/usecases/consumers.go` that claims the CloudEvents
   `id` through the inbox (`writer.claimed` -> `ports.Inbox`, table `processed_events`) and
   applies the effect in ONE unit of work.
4. A consumer group id from a mandatory env var (`*_CONSUMER_GROUP`, boot error naming
   it in `cmd/api/config.go`), built in `consumerSpecs` (`cmd/api/wiring.go`). The DLQ
   path (`<topic>.dlq`, `deadletter.go`) guards shutdown
   (`TestDeadLetterCallsGuardCancellation`).
5. Tests: `consumers_test.go` and `consume_loop_test.go` in the inbound kafka package,
   the use case test in `internal/application/usecases/consumers_test.go`, and a
   message in `cmd/api/e2e_integration_test.go`.

## Verify before opening the PR

`make check`, `go test ./internal/architecture/ -count=1` (catalogue, CloudEvents-only,
relay cap), the spectral lint of the AsyncAPI, and the integration command above.
