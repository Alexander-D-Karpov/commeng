package postgres

import (
	"errors"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/Alexander-D-Karpov/commeng/internal/domain"
)

// mapPGError exposes safe semantic errors instead of internal SQL details.
func mapPGError(err error) error {
	var postgresError *pgconn.PgError
	if !errors.As(err, &postgresError) {
		return err
	}
	switch postgresError.Code {
	case "23505":
		return domain.Fault(domain.ErrAlreadyExists, "already exists")
	case "23503":
		return domain.Fault(domain.ErrInput, "referenced object does not exist")
	case "23514", "22001", "23502":
		return domain.Fault(domain.ErrInput, "value violates a database constraint")
	default:
		return err
	}
}
