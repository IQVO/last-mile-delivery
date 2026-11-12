//go:build integration

package postgres_test

import (
	"testing"

	"github.com/claudioed/last-mile-delivery/internal/testing/pgtest"
)

// TestOutcomeDetailAndProfileConstraints proves migrations 0004 and 0005 reject
// what the domain would never produce (ADR 0014).
func TestOutcomeDetailAndProfileConstraints(t *testing.T) {
	pool := pgtest.NewPool(t)
	pgtest.Exec(t, pool, `INSERT INTO routes VALUES ('r1', 'w', 'd', 's', 'OUT_FOR_DELIVERY', 2)`)
	bad := []struct{ name, sql string }{
		{"safe place on a failure", `INSERT INTO route_attempts VALUES ('r1', 'T1', 'FAILED', 'absent', now(), 0, 'SAFE_PLACE_USED')`},
		{"not home on a delivery", `INSERT INTO route_attempts VALUES ('r1', 'T2', 'DELIVERED', NULL, now(), 1, 'NOT_HOME')`},
		{"an unknown detail", `INSERT INTO route_attempts VALUES ('r1', 'T3', 'FAILED', 'absent', now(), 2, 'LOST')`},
		{"a profile of a package stop", `INSERT INTO delivery_point_profiles VALUES ('pkg:T1', 1, 1, 0, 0, 0, 0, '[]', NULL, now(), 1)`},
		{"a profile with an empty key", `INSERT INTO delivery_point_profiles VALUES ('', 1, 1, 0, 0, 0, 0, '[]', NULL, now(), 1)`},
		{"a negative counter", `INSERT INTO delivery_point_profiles VALUES ('k', -1, 1, 0, 0, 0, 0, '[]', NULL, now(), 1)`},
		{"a profile at version zero", `INSERT INTO delivery_point_profiles VALUES ('k', 1, 1, 0, 0, 0, 0, '[]', NULL, now(), 0)`},
		{"strikes that are not an array", `INSERT INTO delivery_point_profiles VALUES ('k', 1, 1, 0, 0, 0, 0, '{}', NULL, now(), 1)`},
		{"six strikes", `INSERT INTO delivery_point_profiles VALUES ('k', 1, 1, 0, 0, 0, 0, '[1,2,3,4,5,6]', NULL, now(), 1)`},
	}
	for _, c := range bad {
		if err := pgtest.TryExec(pool, c.sql); err != nil {
			continue
		}
		t.Errorf("%s: the database accepted it", c.name)
	}
	pgtest.Exec(t, pool, `INSERT INTO route_attempts VALUES ('r1', 'T1', 'FAILED', 'absent', now(), 0, 'NOT_HOME')`)
	pgtest.Exec(t, pool, `INSERT INTO route_attempts VALUES ('r1', 'T2', 'DELIVERED', NULL, now(), 1, 'SAFE_PLACE_USED')`)
	pgtest.Exec(t, pool, `INSERT INTO route_attempts VALUES ('r1', 'T3', 'FAILED', 'absent', now(), 2)`)
	pgtest.Exec(t, pool, `INSERT INTO delivery_point_profiles VALUES ('k', 1, 1, 0, 0, 0, 0, '[]', NULL, now(), 1)`)
}
