package memory_test

import (
	"context"
	"testing"

	"github.com/claudioed/last-mile-delivery/internal/adapters/outbound/memory"
	"github.com/claudioed/last-mile-delivery/internal/adapters/outbound/repocontract"
)

// The doubles must keep the promises of the repository ports: the same suite the
// Postgres adapters pass.
func TestMemoryPassesTheRepositoryContract(t *testing.T) {
	repocontract.Run(t, func(t *testing.T) repocontract.Fixture {
		s := memory.NewStore()
		return repocontract.Fixture{
			Waves: s.Waves(), Routes: s.Routes(), Partners: s.Partners(), Histories: s.Histories(), Stations: s.Stations(),
			ServiceAreas: s.ServiceAreas(), PackageStops: s.PackageStops(), Inbound: s.Inbound(), Inbox: s.Inbox(),
			Outbox: s.Outbox(), UoW: s.UnitOfWork(),
			OutboxCount: func(context.Context) (int, error) { return s.Counts().OutboxEvents, nil },
		}
	})
}
