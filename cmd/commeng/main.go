package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
)

var version = "dev"

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:]); err != nil {
		slog.Error("commeng failed", "error", err)
		os.Exit(1)
	}
}
func run(ctx context.Context, args []string) error {
	// Running `commeng` with no arguments is a convenient local API server.
	if len(args) == 0 {
		return serve(ctx, nil)
	}
	switch args[0] {
	case "serve", "server":
		return serve(ctx, args[1:])
	case "migrate":
		return migrate(ctx, args[1:])
	case "bootstrap-admin":
		return bootstrapAdmin(ctx, args[1:])
	case "seed":
		return seed(ctx, args[1:])
	case "prune-events":
		return pruneEvents(ctx, args[1:])
	case "healthcheck":
		return healthcheck(ctx, args[1:]...)
	case "version":
		fmt.Println(version)
		return nil
	case "help", "--help", "-h":
		fmt.Print(`commeng serve [--storage memory|postgres] [--addr :8080]
commeng migrate [up|down|status|version]
commeng seed --posts 10 --comments 50 (dev PostgreSQL only)
commeng bootstrap-admin --name NAME --email EMAIL --password-stdin
commeng prune-events [--days 90] [--limit 10000] [--yes]
commeng healthcheck [--addr :8080] [--timeout 3s] (GET /readyz)
commeng version
Run without arguments to start a memory-backed API on :8080.
`)
		return nil
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}
