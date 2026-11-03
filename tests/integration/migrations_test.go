//go:build integration

package integration

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Alexander-D-Karpov/commeng/migrations"
)

func TestMigrationUpDownStatusAndReapply(t *testing.T) {
	_, dsn := postgresFixture(t)
	ctx := context.Background()

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("open migration test pool: %v", err)
	}
	defer pool.Close()
	runner := migrations.New(pool)

	checkVersion := func(want int64) {
		t.Helper()
		version, err := runner.Version(ctx)
		if err != nil {
			t.Fatalf("read migration version: %v", err)
		}
		if version != want {
			t.Fatalf("migration version = %d, want %d", version, want)
		}
	}

	checkVersion(4)
	if err := runner.Down(ctx); err != nil {
		t.Fatalf("revert migration 004: %v", err)
	}
	checkVersion(3)
	if err := runner.Check(ctx); err == nil {
		t.Fatal("server accepted an outdated schema")
	}
	if err := runner.Up(ctx); err != nil {
		t.Fatalf("reapply migration 004: %v", err)
	}
	checkVersion(4)
	if err := runner.Check(ctx); err != nil {
		t.Fatalf("freshly migrated schema not accepted: %v", err)
	}

	// A rollback of 001 is deliberately destructive, but this fixture owns an
	// isolated schema. Reapplying all migrations must recreate a valid schema.
	for version := 4; version >= 1; version-- {
		if err := runner.Down(ctx); err != nil {
			t.Fatalf("revert migration %03d: %v", version, err)
		}
	}
	checkVersion(0)
	if err := runner.Up(ctx); err != nil {
		t.Fatalf("reapply migrations from zero: %v", err)
	}
	checkVersion(4)
}

func TestMigrationAdoptsVerifiedLegacyJournal(t *testing.T) {
	_, dsn := postgresFixture(t)
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("open migration test pool: %v", err)
	}
	defer pool.Close()

	// Simulate the original version-only rows from the previous commeng release.
	if _, err := pool.Exec(ctx,
		`UPDATE schema_migrations SET name=NULL, checksum=NULL`); err != nil {
		t.Fatalf("clear migration metadata: %v", err)
	}
	runner := migrations.New(pool)
	if err := runner.Check(ctx); err == nil {
		t.Fatal("unverified legacy journal unexpectedly passed readiness check")
	}
	if err := runner.Up(ctx); err != nil {
		t.Fatalf("adopt legacy migration journal: %v", err)
	}
	if err := runner.Check(ctx); err != nil {
		t.Fatalf("adopted schema failed readiness check: %v", err)
	}
	entries, err := runner.List(ctx)
	if err != nil {
		t.Fatalf("list migrations: %v", err)
	}
	for _, entry := range entries {
		if !entry.Applied || entry.Legacy {
			t.Errorf("migration %03d not fully adopted", entry.Migration.Version)
		}
	}
}

func TestMigrationRejectsChecksumDrift(t *testing.T) {
	_, dsn := postgresFixture(t)
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("open migration test pool: %v", err)
	}
	defer pool.Close()
	if _, err := pool.Exec(ctx,
		`UPDATE schema_migrations SET checksum='incorrect' WHERE version=1`); err != nil {
		t.Fatalf("tamper with test migration journal: %v", err)
	}
	if err := migrations.New(pool).Up(ctx); err == nil {
		t.Fatal("modified applied migration checksum was accepted")
	}
}

func TestMigrationRejectsUntrackedSchema(t *testing.T) {
	_, dsn := postgresFixture(t)
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("open migration test pool: %v", err)
	}
	defer pool.Close()

	// Mimic accidental deletion of the migration history. A fresh `up` must
	// refuse the preexisting tables instead of silently adopting them.
	if _, err := pool.Exec(ctx, `DROP TABLE schema_migrations`); err != nil {
		t.Fatalf("remove test migration journal: %v", err)
	}
	if err := migrations.New(pool).Up(ctx); err == nil {
		t.Fatal("untracked application tables were accepted")
	}
}
