//go:build integration

package postgres_test

import (
	"context"
	"testing"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	"github.com/golang-migrate/migrate/v4/source/iofs"

	adapter "github.com/claudioed/last-mile-delivery/internal/adapters/outbound/postgres"
	"github.com/claudioed/last-mile-delivery/internal/testing/pgtest"
)

// TestMigrationsGoUpDownUp proves every embedded migration applies, reverses to
// an empty schema and applies again.
func TestMigrationsGoUpDownUp(t *testing.T) {
	url := pgtest.URL(t) // a fresh database with the migrations already applied (twice: the second run is a no-op)
	pool, err := adapter.NewPool(context.Background(), url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	full := pgtest.TableCount(t, pool)
	if full < 15 {
		t.Fatalf("tables after up = %d, want at least 15", full)
	}
	src, err := iofs.New(adapter.MigrationsFS(), "migrations")
	if err != nil {
		t.Fatal(err)
	}
	m, err := migrate.NewWithSourceInstance("iofs", src, url)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = m.Close() }()
	if err := m.Down(); err != nil {
		t.Fatalf("down: %v", err)
	}
	if n := pgtest.TableCount(t, pool); n != 0 {
		t.Fatalf("tables after down = %d, want 0", n)
	}
	if err := m.Up(); err != nil {
		t.Fatalf("up again: %v", err)
	}
	if n := pgtest.TableCount(t, pool); n != full {
		t.Fatalf("tables after up again = %d, want %d", n, full)
	}
}
