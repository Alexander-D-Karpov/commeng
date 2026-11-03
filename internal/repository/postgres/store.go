// Package postgres implements indexed SQL repositories with transactional writes.
package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Alexander-D-Karpov/commeng/internal/observability"
	"github.com/Alexander-D-Karpov/commeng/migrations"
)

type Store struct {
	pool   *pgxpool.Pool
	tracer *observability.PostgreSQLTracer
}

func Open(ctx context.Context, dsn string) (*Store, error) {
	config, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse DATABASE_URL: %w", err)
	}
	tracer := &observability.PostgreSQLTracer{}
	config.ConnConfig.Tracer = tracer
	config.MaxConns = 20
	config.MinConns = 1
	config.MaxConnLifetime = 30 * time.Minute

	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("create pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("postgres ping: %w", err)
	}
	return &Store{pool: pool, tracer: tracer}, nil
}

// SetMetrics wires the existing pgx tracer to a private Prometheus registry.
func (s *Store) SetMetrics(metrics *observability.Metrics) { s.tracer.BindMetrics(metrics) }

func (s *Store) Close() {
	s.pool.Close()
}

func (s *Store) Ping(ctx context.Context) error {
	return s.pool.Ping(ctx)
}

// Migrate is retained for repository integration fixtures. The normal entry
// point for users is `commeng migrate up` or `make migrate-up`.
func (s *Store) Migrate(ctx context.Context) error {
	return migrations.New(s.pool).Up(ctx)
}

// CheckSchema refuses to start a server against a stale or unverified schema.
func (s *Store) CheckSchema(ctx context.Context) error {
	return migrations.New(s.pool).Check(ctx)
}

// PoolStats is a bounded snapshot for private Prometheus gauges.
func (s *Store) PoolStats() (acquired, idle, total, maximum int32) {
	stat := s.pool.Stat()
	return stat.AcquiredConns(), stat.IdleConns(), stat.TotalConns(), stat.MaxConns()
}
