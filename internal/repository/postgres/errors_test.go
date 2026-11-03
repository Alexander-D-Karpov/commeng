package postgres

import (
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/Alexander-D-Karpov/commeng/internal/domain"
)

func TestConstraintErrorsBecomePublicInputErrors(t *testing.T) {
	tests := []struct {
		code string
		want domain.Code
	}{
		{"23505", domain.ErrAlreadyExists},
		{"23503", domain.ErrInput},
		{"23514", domain.ErrInput},
		{"22001", domain.ErrInput},
	}
	for _, tt := range tests {
		t.Run(tt.code, func(t *testing.T) {
			if err := mapPGError(&pgconn.PgError{Code: tt.code}); !domain.HasCode(err, tt.want) {
				t.Fatalf("SQLSTATE %s produced %v", tt.code, err)
			}
		})
	}
	original := errors.New("transport unavailable")
	if got := mapPGError(original); got != original {
		t.Fatalf("transport failure was altered: %v", got)
	}
}
