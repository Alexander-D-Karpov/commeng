package migrations

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const advisoryLockID int64 = 184629508

// Applied records the migration journal; legacy entries have an empty checksum
// until a successful migrate up/down verifies their schema and adopts them.
type Applied struct {
	Version   int64
	Name      string
	Checksum  string
	AppliedAt time.Time
}

type Status struct {
	Migration Migration
	Applied   bool
	Legacy    bool
	AppliedAt time.Time
}

type Runner struct {
	pool *pgxpool.Pool
}

func New(pool *pgxpool.Pool) *Runner {
	return &Runner{pool: pool}
}

// Up executes unapplied migrations under a transaction-scoped advisory lock.
// All SQL and journal writes commit or roll back together.
func (r *Runner) Up(ctx context.Context) error {
	steps, err := All()
	if err != nil {
		return err
	}
	return r.withTransaction(ctx, func(tx pgx.Tx) error {
		if err := ensureJournal(ctx, tx); err != nil {
			return err
		}
		applied, err := loadJournal(ctx, tx)
		if err != nil {
			return err
		}
		if err := verifyHistory(ctx, tx, steps, applied, true); err != nil {
			return err
		}
		if len(applied) == 0 {
			if err := rejectUntrackedSchema(ctx, tx); err != nil {
				return err
			}
		}
		for _, step := range steps[len(applied):] {
			if _, err := tx.Exec(ctx, step.UpSQL, pgx.QueryExecModeSimpleProtocol); err != nil {
				return fmt.Errorf("migration %03d_%s up: %w", step.Version, step.Name, err)
			}
			if _, err := tx.Exec(ctx, `INSERT INTO schema_migrations(version, name, checksum, applied_at)
				VALUES ($1, $2, $3, now())`, step.Version, step.Name, step.Checksum); err != nil {
				return fmt.Errorf("record migration %03d: %w", step.Version, err)
			}
		}
		return nil
	})
}

// Down reverts exactly one migration by default. Reversing migrations may
// destroy data; the CLI requires --yes before invoking this operation.
func (r *Runner) Down(ctx context.Context) error {
	steps, err := All()
	if err != nil {
		return err
	}
	return r.withTransaction(ctx, func(tx pgx.Tx) error {
		if err := ensureJournal(ctx, tx); err != nil {
			return err
		}
		applied, err := loadJournal(ctx, tx)
		if err != nil {
			return err
		}
		if err := verifyHistory(ctx, tx, steps, applied, true); err != nil {
			return err
		}
		if len(applied) == 0 {
			return errors.New("no applied migrations to revert")
		}
		step := steps[len(applied)-1]
		if _, err := tx.Exec(ctx, step.DownSQL, pgx.QueryExecModeSimpleProtocol); err != nil {
			return fmt.Errorf("migration %03d_%s down: %w", step.Version, step.Name, err)
		}
		if _, err := tx.Exec(ctx, `DELETE FROM schema_migrations WHERE version=$1`, step.Version); err != nil {
			return fmt.Errorf("remove migration %03d from journal: %w", step.Version, err)
		}
		return nil
	})
}

// List reports applied and pending migrations without changing the database.
// An uninitialized database reports every migration as pending.
func (r *Runner) List(ctx context.Context) ([]Status, error) {
	steps, err := All()
	if err != nil {
		return nil, err
	}
	applied, err := r.journal(ctx)
	if err != nil {
		return nil, err
	}
	if err := checkHistory(steps, applied); err != nil {
		return nil, err
	}
	if len(applied) == 0 {
		if err := rejectUntrackedSchema(ctx, r.pool); err != nil {
			return nil, err
		}
	}
	result := make([]Status, 0, len(steps))
	for index, step := range steps {
		entry := Status{Migration: step}
		if index < len(applied) {
			entry.Applied = true
			entry.Legacy = applied[index].Checksum == ""
			entry.AppliedAt = applied[index].AppliedAt
		}
		result = append(result, entry)
	}
	return result, nil
}

func (r *Runner) Version(ctx context.Context) (int64, error) {
	entries, err := r.List(ctx)
	if err != nil {
		return 0, err
	}
	var version int64
	for _, entry := range entries {
		if entry.Applied {
			version = entry.Migration.Version
		}
	}
	return version, nil
}

// Check refuses to serve with an uninitialized, stale, or unknown schema.
func (r *Runner) Check(ctx context.Context) error {
	entries, err := r.List(ctx)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !entry.Applied {
			return fmt.Errorf(
				"schema is not up to date: pending %03d_%s; run commeng migrate up",
				entry.Migration.Version,
				entry.Migration.Name,
			)
		}
		if entry.Legacy {
			return errors.New("legacy migration history not yet verified: run commeng migrate up")
		}
	}
	return nil
}

func (r *Runner) withTransaction(ctx context.Context, action func(pgx.Tx) error) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin migration: %w", err)
	}
	defer func() {
		rollbackCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = tx.Rollback(rollbackCtx)
	}()
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, advisoryLockID); err != nil {
		return fmt.Errorf("lock migrations: %w", err)
	}
	if err := action(tx); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit migrations: %w", err)
	}
	return nil
}
