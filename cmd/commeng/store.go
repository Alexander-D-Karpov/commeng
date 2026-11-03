package main

import (
	"context"
	"fmt"
	"os"

	"github.com/Alexander-D-Karpov/commeng/internal/repository"
	"github.com/Alexander-D-Karpov/commeng/internal/repository/memory"
	"github.com/Alexander-D-Karpov/commeng/internal/repository/postgres"
)

func environment(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}
func openStore(ctx context.Context, storage string) (repository.Repository, error) {
	switch storage {
	case "memory":
		return memory.New(), nil
	case "postgres":
		dsn := os.Getenv("DATABASE_URL")
		if dsn == "" {
			return nil, fmt.Errorf("DATABASE_URL is required for postgres storage")
		}
		store, err := postgres.Open(ctx, dsn)
		if err != nil {
			return nil, err
		}
		if err := store.CheckSchema(ctx); err != nil {
			store.Close()
			return nil, err
		}
		return store, nil
	default:
		return nil, fmt.Errorf("STORAGE must be memory or postgres, got %q", storage)
	}
}
