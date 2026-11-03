package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Alexander-D-Karpov/commeng/migrations"
)

const migrationUsage = `commeng migrate [up|down|status|version] [options]

  up                 Apply all pending migrations (default)
  down --yes         Revert exactly one migration; may destroy data
  status             List applied/pending migrations
  version            Print current schema version

Environment: DATABASE_URL (PostgreSQL connection string).
`

func migrate(ctx context.Context, args []string) error {
	command := "up"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		command, args = args[0], args[1:]
	}
	if command == "help" || command == "-h" || command == "--help" {
		fmt.Print(migrationUsage)
		return nil
	}
	if command != "up" && command != "down" && command != "status" && command != "version" {
		return fmt.Errorf("unknown migration command %q\n%s", command, migrationUsage)
	}
	flags := flag.NewFlagSet("migrate "+command, flag.ContinueOnError)
	confirmed := flags.Bool("yes", false, "confirm destructive rollback")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected migration arguments: %v", flags.Args())
	}
	if command == "down" && !*confirmed {
		return errors.New("down can destroy data; explicitly pass --yes")
	}

	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		return errors.New("DATABASE_URL is required for PostgreSQL migrations")
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return fmt.Errorf("open PostgreSQL for migrations: %w", err)
	}
	defer pool.Close()
	if err := pool.Ping(ctx); err != nil {
		return fmt.Errorf("connect PostgreSQL for migrations: %w", err)
	}
	runner := migrations.New(pool)

	switch command {
	case "up":
		if err := runner.Up(ctx); err != nil {
			return err
		}
	case "down":
		if err := runner.Down(ctx); err != nil {
			return err
		}
	case "status":
		return printMigrationStatus(ctx, runner)
	case "version":
		version, err := runner.Version(ctx)
		if err != nil {
			return err
		}
		fmt.Printf("%d\n", version)
		return nil
	}
	version, err := runner.Version(ctx)
	if err != nil {
		return err
	}
	fmt.Printf("Migration %s complete; current schema version: %d\n", command, version)
	return nil
}

func printMigrationStatus(ctx context.Context, runner *migrations.Runner) error {
	entries, err := runner.List(ctx)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		state := "pending"
		appliedAt := "-"
		if entry.Applied {
			state = "applied"
			appliedAt = entry.AppliedAt.UTC().Format(time.RFC3339)
		}
		if entry.Legacy {
			state = "legacy (run migrate up to verify)"
		}
		fmt.Printf("%03d %-36s %-34s %s\n",
			entry.Migration.Version, entry.Migration.Name, state, appliedAt)
	}
	return nil
}
