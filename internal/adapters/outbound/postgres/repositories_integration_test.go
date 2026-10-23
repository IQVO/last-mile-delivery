//go:build integration

package postgres_test

import (
	"testing"

	"github.com/claudioed/last-mile-delivery/internal/adapters/outbound/postgres"
	"github.com/claudioed/last-mile-delivery/internal/adapters/outbound/repocontract"
	"github.com/claudioed/last-mile-delivery/internal/application/outbox"
	"github.com/claudioed/last-mile-delivery/internal/domain/shared"
	"github.com/claudioed/last-mile-delivery/internal/testing/pgtest"
)

// stubEncoder stores each event as a minimal outbox row: the contract only needs
// the outbox to count rows atomically with the aggregates.
type stubEncoder struct{ n int }

func (e *stubEncoder) Encode(events ...shared.Event) ([]outbox.Message, error) {
	out := make([]outbox.Message, 0, len(events))
	for _, ev := range events {
		e.n++
		out = append(out, outbox.Message{
			EventID: string(rune('a'+e.n%26)) + ev.SubjectID() + ev.EventName() + string(rune('0'+e.n%10)), Topic: "test.integration",
			EventType: ev.EventName(), Subject: ev.SubjectID(), Key: []byte(ev.SubjectID()), DataSchema: "urn:test", Value: []byte(`{"stub":true}`),
		})
	}
	return out, nil
}

// The same behavioural contract the in-memory doubles pass, run against a real
// Postgres: every contract case gets its own database.
func TestPostgresPassesTheRepositoryContract(t *testing.T) {
	repocontract.Run(t, func(t *testing.T) repocontract.Fixture {
		pool := pgtest.NewPool(t)
		store := postgres.NewOutboxRepo(pool)
		return repocontract.Fixture{
			Waves: postgres.NewWaveRepository(pool), Routes: postgres.NewRouteRepository(pool), Partners: postgres.NewPartnerRepository(pool),
			Histories: postgres.NewHistoryRepository(pool), Stations: postgres.NewStationRepository(pool),
			ServiceAreas: postgres.NewServiceAreaRepository(pool), PackageStops: postgres.NewPackageStopRepository(pool),
			Inbound: postgres.NewInboundPackageRepository(pool), Inbox: postgres.NewInbox(pool),
			Outbox: postgres.NewOutbox(pool, &stubEncoder{}), UoW: postgres.NewUnitOfWork(pool),
			OutboxCount: store.Count,
		}
	})
}

// TestDatabaseConstraintsBackTheInvariants proves the CHECKs and unique indexes
// reject what the domain would never produce, so a bug can not store it.
func TestDatabaseConstraintsBackTheInvariants(t *testing.T) {
	pool := pgtest.NewPool(t)
	bad := []struct{ name, sql string }{
		{"wave times out of order", `INSERT INTO delivery_waves VALUES ('w', 's', '2026-10-24', now(), now(), now(), 1, 0, 'OPEN', NULL, 1)`},
		{"wave over capacity", `INSERT INTO delivery_waves VALUES ('w', 's', '2026-10-24', now(), now() + interval '1 hour', now() + interval '2 hours', 1, 2, 'OPEN', NULL, 1)`},
		{"wave with an unknown state", `INSERT INTO delivery_waves VALUES ('w', 's', '2026-10-24', now(), now() + interval '1 hour', now() + interval '2 hours', 1, 0, 'DONE', NULL, 1)`},
		{"cancelled wave without a reason", `INSERT INTO delivery_waves VALUES ('w', 's', '2026-10-24', now(), now() + interval '1 hour', now() + interval '2 hours', 1, 0, 'CANCELLED', NULL, 1)`},
		{"partner without a van", `INSERT INTO delivery_service_partners VALUES ('d', 's', 0, 'ACTIVE', 1)`},
		{"package with four failures", `INSERT INTO package_histories VALUES ('t', 's', NULL, 4, 'INDUCTED', now(), 1)`},
	}
	for _, c := range bad {
		if err := pgtest.TryExec(pool, c.sql); err == nil {
			t.Errorf("%s: the database accepted it", c.name)
		}
	}
	pgtest.Exec(t, pool, `INSERT INTO routes VALUES ('r1', 'w', 'd', 's', 'OUT_FOR_DELIVERY', 2), ('r2', 'w', 'd', 's', 'OUT_FOR_DELIVERY', 2)`)
	pgtest.Exec(t, pool, `INSERT INTO route_attempts VALUES ('r1', 'T1', 'DELIVERED', NULL, now(), 0)`)
	if err := pgtest.TryExec(pool, `INSERT INTO route_attempts VALUES ('r2', 'T1', 'DELIVERED', NULL, now(), 0)`); err == nil {
		t.Error("a package delivered twice across routes was accepted")
	}
	pgtest.Exec(t, pool, `INSERT INTO route_attempts VALUES ('r2', 'T1', 'FAILED', 'absent', now(), 0)`)
}
