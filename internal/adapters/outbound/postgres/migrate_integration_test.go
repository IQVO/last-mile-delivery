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

// TestMigrationsCreateTheC5Objects proves migration 0004 (route_attempts.outcome_detail)
// and 0005 (delivery_point_profiles) are applied, and that stepping back over
// them removes exactly those objects (ADR 0014).
func TestMigrationsCreateTheC5Objects(t *testing.T) {
	url := pgtest.URL(t)
	pool, err := adapter.NewPool(context.Background(), url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	exists := func(sql string) bool {
		var n int
		if err := pool.QueryRow(context.Background(), sql).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n == 1
	}
	const column = `SELECT count(*) FROM information_schema.columns WHERE table_name = 'route_attempts' AND column_name = 'outcome_detail'`
	const table = `SELECT count(*) FROM information_schema.tables WHERE table_name = 'delivery_point_profiles'`
	if !exists(column) || !exists(table) {
		t.Fatal("migrations 0004 and 0005 must be applied")
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
	// 0008 (the induction kind) is the ceiling now: stepping back four
	// times crosses it, 0007, 0006 and 0005 together.
	if err := m.Steps(-4); err != nil || exists(table) || !exists(column) {
		t.Fatalf("0008+0007+0006+0005 down: err=%v, the table must go and the column stay", err)
	}
	if err := m.Steps(-1); err != nil || exists(column) {
		t.Fatalf("0004 down: err=%v, the column must go", err)
	}
	if err := m.Up(); err != nil || !exists(column) || !exists(table) {
		t.Fatalf("up again: err=%v", err)
	}
}

// TestMigrationsCreateTheC4AreaMode proves migration 0006
// (rm_station_service_areas.delivery_mode) is applied and reversed (ADR 0015).
func TestMigrationsCreateTheC4AreaMode(t *testing.T) {
	url := pgtest.URL(t)
	pool, err := adapter.NewPool(context.Background(), url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	const column = `SELECT count(*) FROM information_schema.columns WHERE table_name = 'rm_station_service_areas' AND column_name = 'delivery_mode'`
	var n int
	if err := pool.QueryRow(context.Background(), column).Scan(&n); err != nil || n != 1 {
		t.Fatalf("migration 0006 must be applied: n=%d err=%v", n, err)
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
	// three steps back cross 0008 (the induction kind), 0007 (the wave stop
	// emission) and 0006 together.
	if err := m.Steps(-3); err != nil {
		t.Fatalf("0008+0007+0006 down: %v", err)
	}
	if err := pool.QueryRow(context.Background(), column).Scan(&n); err != nil || n != 0 {
		t.Fatalf("0006 down must drop the column: n=%d err=%v", n, err)
	}
	if err := m.Up(); err != nil {
		t.Fatalf("up again: %v", err)
	}
	if err := pool.QueryRow(context.Background(), column).Scan(&n); err != nil || n != 1 {
		t.Fatalf("up again must restore the column: n=%d err=%v", n, err)
	}
}

// TestMigrationsCreateTheC9WaveEmission proves migration 0007
// (delivery_waves.emission_per_stop_g) is applied and reversed (ADR 0016).
func TestMigrationsCreateTheC9WaveEmission(t *testing.T) {
	url := pgtest.URL(t)
	pool, err := adapter.NewPool(context.Background(), url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	const column = `SELECT count(*) FROM information_schema.columns WHERE table_name = 'delivery_waves' AND column_name = 'emission_per_stop_g'`
	var n int
	if err := pool.QueryRow(context.Background(), column).Scan(&n); err != nil || n != 1 {
		t.Fatalf("migration 0007 must be applied: n=%d err=%v", n, err)
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
	// two steps back cross 0008 (the induction kind) before 0007.
	if err := m.Steps(-2); err != nil {
		t.Fatalf("0008+0007 down: %v", err)
	}
	if err := pool.QueryRow(context.Background(), column).Scan(&n); err != nil || n != 0 {
		t.Fatalf("0007 down must drop the column: n=%d err=%v", n, err)
	}
	if err := m.Up(); err != nil {
		t.Fatalf("up again: %v", err)
	}
	if err := pool.QueryRow(context.Background(), column).Scan(&n); err != nil || n != 1 {
		t.Fatalf("up again must restore the column: n=%d err=%v", n, err)
	}
}

// TestMigrationsCreateTheC8InductionKind proves migration 0008
// (wave_inductions.kind, default DELIVERY) is applied and reversed (ADR 0017).
func TestMigrationsCreateTheC8InductionKind(t *testing.T) {
	url := pgtest.URL(t)
	pool, err := adapter.NewPool(context.Background(), url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	const column = `SELECT count(*) FROM information_schema.columns WHERE table_name = 'wave_inductions' AND column_name = 'kind'`
	var n int
	if err := pool.QueryRow(context.Background(), column).Scan(&n); err != nil || n != 1 {
		t.Fatalf("migration 0008 must be applied: n=%d err=%v", n, err)
	}
	// A legacy row keeps the default kind DELIVERY; only the two kinds fit.
	if _, err := pool.Exec(context.Background(), `INSERT INTO delivery_waves (wave_id, station_node_id, service_date, induct_cutoff_at, window_start, window_end, stop_capacity, assigned_stops, state, version)
		VALUES ('wave-k', 'node-ds-01', '2026-10-24', '2026-10-24 08:00:00Z', '2026-10-24 12:00:00Z', '2026-10-24 20:00:00Z', 1, 0, 'OPEN', 1)`); err != nil {
		t.Fatal(err)
	}
	var kind string
	if err := pool.QueryRow(context.Background(), `INSERT INTO wave_inductions (wave_id, tracking_number, stop_key, inducted_at, position)
		VALUES ('wave-k', 'TRK-K', 'k-a', '2026-10-24 06:00:00Z', 0) RETURNING kind`).Scan(&kind); err != nil || kind != "DELIVERY" {
		t.Fatalf("a legacy insert must default to DELIVERY: kind=%q err=%v", kind, err)
	}
	if _, err := pool.Exec(context.Background(), `INSERT INTO wave_inductions (wave_id, tracking_number, stop_key, kind, inducted_at, position)
		VALUES ('wave-k', 'TRK-X', 'k-a', 'RETURN', '2026-10-24 06:00:00Z', 1)`); err == nil {
		t.Fatal("an unknown kind must be refused by the CHECK")
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
	if err := m.Steps(-1); err != nil {
		t.Fatalf("0008 down: %v", err)
	}
	if err := pool.QueryRow(context.Background(), column).Scan(&n); err != nil || n != 0 {
		t.Fatalf("0008 down must drop the column: n=%d err=%v", n, err)
	}
	if err := m.Up(); err != nil {
		t.Fatalf("up again: %v", err)
	}
	if err := pool.QueryRow(context.Background(), column).Scan(&n); err != nil || n != 1 {
		t.Fatalf("up again must restore the column: n=%d err=%v", n, err)
	}
}
