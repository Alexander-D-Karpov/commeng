package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"time"

	"github.com/Alexander-D-Karpov/commeng/internal/repository/postgres"
)

// pruneEvents is deliberately an explicit CLI operation. Automatic deletion
// risks invalidating client replay cursors without an agreed retention policy.
func pruneEvents(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("prune-events", flag.ContinueOnError)
	days := flags.Int("days", 90, "delete events older than N complete days (7..3650)")
	limit := flags.Int("limit", 10000, "maximum events to remove per invocation (1..10000)")
	confirmed := flags.Bool("yes", false, "perform deletion instead of a dry-run count")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || *days < 7 || *days > 3650 || *limit < 1 || *limit > 10000 {
		return errors.New("usage: commeng prune-events [--days 90] [--limit 10000] [--yes]")
	}
	store, err := openStore(ctx, "postgres")
	if err != nil {
		return err
	}
	defer store.Close()
	pg, ok := store.(*postgres.Store)
	if !ok {
		return errors.New("prune-events requires PostgreSQL")
	}
	cutoff := time.Now().UTC().Add(-time.Duration(*days) * 24 * time.Hour)
	if !*confirmed {
		count, err := pg.CountExpiredEvents(ctx, cutoff)
		if err != nil {
			return err
		}
		fmt.Printf("dry run: %d events before %s; use --yes to delete up to %d\n",
			count, cutoff.Format(time.RFC3339), *limit)
		return nil
	}
	removed, err := pg.PruneCommentEvents(ctx, cutoff, *limit)
	if err != nil {
		return err
	}
	fmt.Printf("deleted %d old comment events; cutoff=%s\n", removed, cutoff.Format(time.RFC3339))
	return nil
}
