package migrations

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
)

func (r *Runner) journal(ctx context.Context) ([]Applied, error) {
	var exists bool
	if err := r.pool.QueryRow(ctx, `SELECT to_regclass('schema_migrations') IS NOT NULL`).Scan(&exists); err != nil {
		return nil, fmt.Errorf("check schema migration journal: %w", err)
	}
	if !exists {
		return nil, nil
	}
	// Legacy databases have only the version column. Read their records without
	// altering the schema; migrate up will upgrade their journal transactionally.
	var extended bool
	if err := r.pool.QueryRow(ctx, `SELECT EXISTS (
		SELECT 1 FROM information_schema.columns
		WHERE table_schema=current_schema() AND table_name='schema_migrations'
		  AND column_name='checksum')`).Scan(&extended); err != nil {
		return nil, fmt.Errorf("read schema migration metadata: %w", err)
	}
	query := `SELECT version, '', '', now() FROM schema_migrations ORDER BY version`
	if extended {
		query = `SELECT version, coalesce(name,''), coalesce(checksum,''),
			coalesce(applied_at, now()) FROM schema_migrations ORDER BY version`
	}
	rows, err := r.pool.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("read schema migration journal: %w", err)
	}
	defer rows.Close()
	return scanJournal(rows)
}

func ensureJournal(ctx context.Context, tx pgx.Tx) error {
	statements := []string{
		`CREATE TABLE IF NOT EXISTS schema_migrations (
			version bigint PRIMARY KEY, name text, checksum text,
			applied_at timestamptz NOT NULL DEFAULT now()
		)`,
		`ALTER TABLE schema_migrations ADD COLUMN IF NOT EXISTS name text`,
		`ALTER TABLE schema_migrations ADD COLUMN IF NOT EXISTS checksum text`,
		`ALTER TABLE schema_migrations ADD COLUMN IF NOT EXISTS applied_at timestamptz NOT NULL DEFAULT now()`,
	}
	for _, query := range statements {
		if _, err := tx.Exec(ctx, query); err != nil {
			return fmt.Errorf("prepare migration journal: %w", err)
		}
	}
	return nil
}

func loadJournal(ctx context.Context, tx pgx.Tx) ([]Applied, error) {
	rows, err := tx.Query(ctx, `SELECT version, coalesce(name,''),
		coalesce(checksum,''), applied_at FROM schema_migrations ORDER BY version`)
	if err != nil {
		return nil, fmt.Errorf("read applied migrations: %w", err)
	}
	defer rows.Close()
	return scanJournal(rows)
}

func scanJournal(rows pgx.Rows) ([]Applied, error) {
	var result []Applied
	for rows.Next() {
		var entry Applied
		if err := rows.Scan(&entry.Version, &entry.Name, &entry.Checksum, &entry.AppliedAt); err != nil {
			return nil, fmt.Errorf("scan applied migration: %w", err)
		}
		result = append(result, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate applied migrations: %w", err)
	}
	return result, nil
}

func checkHistory(steps []Migration, applied []Applied) error {
	if len(applied) > len(steps) {
		return fmt.Errorf("database schema is newer than binary: %d > %d", len(applied), len(steps))
	}
	for index, entry := range applied {
		step := steps[index]
		if entry.Version != step.Version {
			return fmt.Errorf(
				"migration history has a gap or unknown version %d at position %d",
				entry.Version,
				index+1,
			)
		}
		if entry.Name != "" && entry.Name != step.Name {
			return fmt.Errorf("migration %03d name changed from %q to %q", entry.Version, entry.Name, step.Name)
		}
		if entry.Checksum != "" && entry.Checksum != step.Checksum {
			return fmt.Errorf("migration %03d checksum mismatch: applied SQL has been modified", entry.Version)
		}
	}
	return nil
}
