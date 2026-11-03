package migrations

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// verifyHistory adopts the original version-only journal only after checking
// that the expected legacy schema actually exists. It never silently labels an
// empty or mismatched database as migrated.
func verifyHistory(ctx context.Context, tx pgx.Tx, steps []Migration, applied []Applied, adopt bool) error {
	if err := checkHistory(steps, applied); err != nil {
		return err
	}
	for index, entry := range applied {
		if entry.Checksum != "" {
			continue
		}
		if !adopt {
			return fmt.Errorf("legacy history for migration %03d needs verification", entry.Version)
		}
		if err := verifyLegacySchema(ctx, tx, entry.Version); err != nil {
			return err
		}
		step := steps[index]
		if _, err := tx.Exec(ctx, `UPDATE schema_migrations SET name=$2, checksum=$3
			WHERE version=$1 AND (checksum IS NULL OR checksum='')`,
			step.Version, step.Name, step.Checksum); err != nil {
			return fmt.Errorf("adopt legacy migration %03d: %w", entry.Version, err)
		}
	}
	return nil
}

// rejectUntrackedSchema prevents an empty/corrupted journal from adopting a
// database containing unmanaged application tables. The names are fixed,
// not derived from user input or untrusted connection parameters.
type schemaChecker interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

func rejectUntrackedSchema(ctx context.Context, db schemaChecker) error {
	var exists bool
	err := db.QueryRow(ctx, `SELECT EXISTS (
		SELECT 1 FROM information_schema.tables
		WHERE table_schema=current_schema()
		AND table_name IN ('users','sessions','posts','comments','comment_likes','moderation_audit')
	)`).Scan(&exists)
	if err != nil {
		return fmt.Errorf("inspect untracked schema: %w", err)
	}
	if exists {
		return errors.New(
			"application tables exist without a valid migration history; restore schema_migrations or migrate a new database",
		)
	}
	return nil
}

func verifyLegacySchema(ctx context.Context, tx pgx.Tx, version int64) error {
	var valid bool
	if err := tx.QueryRow(ctx, `SELECT
		to_regclass('users') IS NOT NULL AND to_regclass('posts') IS NOT NULL
		AND to_regclass('comments') IS NOT NULL AND to_regclass('sessions') IS NOT NULL
		AND to_regclass('comment_likes') IS NOT NULL
		AND to_regclass('moderation_audit') IS NOT NULL`).Scan(&valid); err != nil {
		return fmt.Errorf("verify legacy schema v%d: %w", version, err)
	}
	if !valid {
		return fmt.Errorf("legacy migration %03d exists in journal but expected tables are missing", version)
	}
	if version >= 2 {
		if err := tx.QueryRow(ctx, `SELECT count(*)=3 FROM information_schema.columns
			WHERE table_schema=current_schema() AND table_name='comments'
			AND column_name IN ('user_id','is_post_author','thread_blocked')`).Scan(&valid); err != nil {
			return fmt.Errorf("verify legacy schema v2 columns: %w", err)
		}
		if !valid {
			return errors.New("legacy migration 002 recorded, but comments columns are missing")
		}
	}
	return nil
}
