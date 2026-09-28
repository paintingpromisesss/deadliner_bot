// Package repo implements the domain repository ports over PostgreSQL
// using plain SQL via pgx.
package repo

import (
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/sauron/deadliner/internal/domain"
)

// mapErr translates pgx errors into domain sentinels:
//   - pgx.ErrNoRows      → domain.ErrNotFound
//   - 23505 unique       → domain.ErrConflict
//   - 23503 foreign key  → domain.ErrConflict (with the pg message as context)
func mapErr(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("%w: %v", domain.ErrNotFound, err)
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "23505":
			return fmt.Errorf("%w: unique violation on %s: %s",
				domain.ErrConflict, pgErr.ConstraintName, pgErr.Message)
		case "23503":
			return fmt.Errorf("%w: foreign key violation on %s: %s",
				domain.ErrConflict, pgErr.ConstraintName, pgErr.Message)
		}
	}
	return err
}

// mapNotFoundErr is mapErr for UPDATE ... RETURNING statements: zero rows
// affected surfaces as pgx.ErrNoRows through QueryRow.
func mapNotFoundErr(err error, what string) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("%w: %s", domain.ErrNotFound, what)
	}
	return mapErr(err)
}
