package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/Alexander-D-Karpov/commeng/internal/config"
	"github.com/Alexander-D-Karpov/commeng/internal/graphqlhttp"
	"github.com/Alexander-D-Karpov/commeng/internal/live"
	"github.com/Alexander-D-Karpov/commeng/internal/observability"
	"github.com/Alexander-D-Karpov/commeng/internal/repository/postgres"
	"github.com/Alexander-D-Karpov/commeng/internal/service"
)

func serve(ctx context.Context, args []string) error {
	stopTracing, err := observability.ConfigureTracing(ctx)
	if err != nil {
		return err
	}
	defer func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = stopTracing(stopCtx)
	}()
	settings := config.FromEnvironment()
	flags := flag.NewFlagSet("serve", flag.ContinueOnError)
	flags.StringVar(&settings.Storage, "storage", settings.Storage, "memory or postgres")
	flags.StringVar(&settings.HTTPAddr, "addr", settings.HTTPAddr, "listen address")
	flags.StringVar(
		&settings.MetricsAddr,
		"metrics-addr",
		settings.MetricsAddr,
		"private Prometheus endpoint, disabled by default",
	)
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected serve arguments: %v", flags.Args())
	}
	if err := settings.Validate(); err != nil {
		return err
	}
	signingKey, err := graphqlhttp.ParseSigningKey(settings.SigningKey)
	if err != nil {
		return err
	}
	origins, err := graphqlhttp.ParseOrigins(settings.AllowedOrigins)
	if err != nil {
		return err
	}
	trustedProxy, err := graphqlhttp.ParseTrustedProxy(settings.TrustedProxyCIDR)
	if err != nil {
		return err
	}
	store, err := openStore(ctx, settings.Storage)
	if err != nil {
		return err
	}
	defer store.Close()
	svc := service.New(store, live.New(), settings.BaseURL)
	if pg, ok := store.(*postgres.Store); ok {
		follower := postgres.NewFollower(pg, svc.Events)
		svc.EventFollower = follower
		svc.DistributedEvents = true
		go follower.Run(ctx)
		// Expired sessions are purged separately from the login write path.
		go pg.RunSessionJanitor(ctx, time.Hour)
	}
	if settings.Storage == "memory" && !settings.Production {
		if email := os.Getenv("DEV_ADMIN_EMAIL"); email != "" {
			if _, err := svc.BootstrapAdmin(ctx, "Dev admin", email, os.Getenv("DEV_ADMIN_PASSWORD")); err != nil {
				return fmt.Errorf("DEV_ADMIN_*: %w", err)
			}
		}
	}
	if settings.Production && settings.TrustedProxyCIDR == "" {
		slog.Warn("TRUSTED_PROXY_CIDR is empty: clients behind a proxy share a rate-limit bucket")
	}
	if settings.PermalinkTemplate != "" {
		svc.PermalinkTemplate = settings.PermalinkTemplate
	}
	handler, err := graphqlhttp.New(svc, graphqlhttp.Config{
		BaseURL:             settings.BaseURL,
		AllowedOrigins:      origins,
		TrustedProxy:        trustedProxy,
		SigningSecret:       signingKey,
		UnsafeBenchmarkMode: settings.UnsafeBenchmarkMode,
	})
	if err != nil {
		return err
	}
	metrics := observability.NewMetrics()
	metrics.BindSubscriptions(svc.Events.TotalSubscribers)
	if pg, ok := store.(*postgres.Store); ok {
		metrics.BindPostgreSQLPool(pg.PoolStats)
		pg.SetMetrics(metrics)
	}
	handler.SetMetrics(metrics)
	var metricsServer *http.Server
	if settings.MetricsAddr != "" {
		listener, err := net.Listen("tcp", settings.MetricsAddr)
		if err != nil {
			return fmt.Errorf("metrics listener: %w", err)
		}
		metricsServer = &http.Server{Handler: metrics.Handler(), ReadHeaderTimeout: 5 * time.Second}
		go func() {
			if err := metricsServer.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
				slog.Error("metrics server failed", "error", err)
			}
		}()
		defer func() {
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = metricsServer.Shutdown(shutdownCtx)
		}()
	}
	httpServer := &http.Server{
		Addr: settings.HTTPAddr, Handler: handler.Handler(),
		ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second,
		WriteTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second,
		MaxHeaderBytes: 32 << 10,
	}
	failures := make(chan error, 1)
	go func() {
		failures <- httpServer.ListenAndServe()
	}()
	slog.Info("commeng listening", "addr", settings.HTTPAddr, "storage", settings.Storage, "origin", settings.BaseURL)
	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return httpServer.Shutdown(shutdownCtx)
	case err := <-failures:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}
