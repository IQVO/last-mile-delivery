package postgres

import (
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/claudioed/last-mile-delivery/internal/application/ports"
)

// uniqueViolation is the SQLSTATE of a unique or primary key conflict.
const uniqueViolation = "23505"

func isUniqueViolation(err error) bool {
	var pg *pgconn.PgError
	return errors.As(err, &pg) && pg.Code == uniqueViolation
}

// missOr maps a no-rows result to the port sentinel ports.ErrNotFound and
// returns any other error unchanged.
func missOr(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return ports.ErrNotFound
	}
	return err
}

// nullable returns nil for an empty string so the column stays NULL.
func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}
