package main

import (
	"context"
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"

	inboundhttp "github.com/claudioed/last-mile-delivery/internal/adapters/inbound/http"
	inboundkafka "github.com/claudioed/last-mile-delivery/internal/adapters/inbound/kafka"
	outboundkafka "github.com/claudioed/last-mile-delivery/internal/adapters/outbound/kafka"
	outboxrelay "github.com/claudioed/last-mile-delivery/internal/adapters/outbound/outbox"
	"github.com/claudioed/last-mile-delivery/internal/adapters/outbound/postgres"
	"github.com/claudioed/last-mile-delivery/internal/adapters/outbound/system"
	"github.com/claudioed/last-mile-delivery/internal/application/usecases"
	"github.com/claudioed/last-mile-delivery/internal/bootretry"
)

// adapters is the set of outbound adapters the composition root wires.
type adapters struct {
	deps        usecases.Deps
	outboxStore outboxrelay.Store
	idempotency inboundhttp.IdempotencyStore
	pool        *pgxpool.Pool
}

// buildAdapters runs the migrations, opens the pool and wires every Postgres
// adapter. The first outbound dial of a freshly started pod can be reset, so
// the migrations and the first ping retry with backoff; on exhaustion the last
// error is returned and the process refuses to boot.
func buildAdapters(ctx context.Context, databaseURL string, logger *slog.Logger) (adapters, func(), error) {
	noop := func() {}
	if databaseURL == "" {
		return adapters{}, noop, errMissingDatabaseURL
	}
	migrationsURL := getenv("MIGRATIONS_DATABASE_URL", databaseURL)
	if err := bootretry.Retry(ctx, logger, "run migrations", func() error { return postgres.RunMigrations(migrationsURL) }); err != nil {
		return adapters{}, noop, err
	}
	pool, err := postgres.NewPool(ctx, databaseURL)
	if err != nil {
		return adapters{}, noop, err
	}
	if err := bootretry.Retry(ctx, logger, "ping postgres", func() error { return pool.Ping(ctx) }); err != nil {
		pool.Close()
		return adapters{}, noop, err
	}
	logger.Info("postgres adapters configured")
	return postgresAdapters(pool), pool.Close, nil
}

func postgresAdapters(pool *pgxpool.Pool) adapters {
	return adapters{
		deps: usecases.Deps{
			Waves: postgres.NewWaveRepository(pool), Routes: postgres.NewRouteRepository(pool), Partners: postgres.NewPartnerRepository(pool),
			Histories: postgres.NewHistoryRepository(pool), Stations: postgres.NewStationRepository(pool),
			ServiceAreas: postgres.NewServiceAreaRepository(pool), PackageStops: postgres.NewPackageStopRepository(pool),
			Inbound: postgres.NewInboundPackageRepository(pool), Inbox: postgres.NewInbox(pool),
			Outbox: postgres.NewOutbox(pool, outboundkafka.NewEncoder()), UoW: postgres.NewUnitOfWork(pool),
			Clock: system.Clock{}, IDs: system.IDs{},
		},
		outboxStore: postgres.NewOutboxRepo(pool),
		idempotency: postgres.NewIdempotencyStore(pool, inboundhttp.ErrKeyReused),
		pool:        pool,
	}
}

// buildServer wires every use case into the REST adapter.
func buildServer(ad adapters, readiness *inboundhttp.Readiness) *inboundhttp.Server {
	d := ad.deps
	return &inboundhttp.Server{
		ScheduleWave: usecases.NewScheduleWave(d), CloseInduction: usecases.NewCloseInduction(d), DispatchWave: usecases.NewDispatchWave(d),
		CancelWave: usecases.NewCancelWave(d), ListWaves: usecases.NewListWaves(d), GetWaveStops: usecases.NewGetWaveStops(d),
		PlanRoute: usecases.NewPlanRoute(d), StartRoute: usecases.NewStartRoute(d), RecordAttempt: usecases.NewRecordAttempt(d),
		GetRoute: usecases.NewGetRoute(d), ContractPartner: usecases.NewContractPartner(d), DeactivatePartner: usecases.NewDeactivatePartner(d),
		Idempotency: inboundhttp.RequireIdempotencyKey(ad.idempotency),
		Readiness:   readiness,
	}
}

// newReadiness builds the /readyz gate: the drain gate plus a Postgres ping and
// a Kafka broker answering a topic-less metadata request (never topics: the
// broker creates them on first write), cached 3 s, bounded 2 s.
func newReadiness(ping func(context.Context) error, brokers []string) *inboundhttp.Readiness {
	return inboundhttp.NewReadiness(inboundhttp.DefaultReadinessTTL, inboundhttp.DefaultReadinessTimeout,
		inboundhttp.Dependency{Name: "postgres", Check: ping},
		inboundhttp.Dependency{Name: "kafka", Check: outboundkafka.NewBrokerCheck(brokers).Check},
	)
}

// consumerSpec builds one consumer.
type consumerSpec struct {
	name     string
	groupEnv string
	build    func(brokers []string, group string, logger *slog.Logger) *inboundkafka.Consumer
}

func consumerSpecs(ad adapters) []consumerSpec {
	d := ad.deps
	return []consumerSpec{
		{"linehaul", linehaulGroupEnv, func(brokers []string, group string, logger *slog.Logger) *inboundkafka.Consumer {
			return inboundkafka.NewLinehaulConsumer(brokers, group, usecases.NewInductOnArrival(d), logger)
		}},
		{"node-topology", topologyGroupEnv, func(brokers []string, group string, logger *slog.Logger) *inboundkafka.Consumer {
			return inboundkafka.NewTopologyConsumer(brokers, group, usecases.NewApplyNodeRegistered(d), usecases.NewApplyServiceAreaAssigned(d), logger)
		}},
		{"shipping", shippingGroupEnv, func(brokers []string, group string, logger *slog.Logger) *inboundkafka.Consumer {
			return inboundkafka.NewShippingConsumer(brokers, group, usecases.NewApplyLabelIssued(d), logger)
		}},
	}
}
