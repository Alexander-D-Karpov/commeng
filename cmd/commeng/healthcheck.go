package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/Alexander-D-Karpov/commeng/internal/config"
)

// healthcheck uses the running binary; no shell or curl is needed in scratch.
// --addr allows deployments with a non-default listener to use the same binary.
func healthcheck(ctx context.Context, args ...string) error {
	flags := flag.NewFlagSet("healthcheck", flag.ContinueOnError)
	address := flags.String("addr", config.FromEnvironment().HTTPAddr, "server listener host:port")
	timeout := flags.Duration("timeout", 3*time.Second, "maximum readiness check time")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || *timeout <= 0 || *timeout > 30*time.Second {
		return errors.New("healthcheck requires a timeout > 0 and <= 30s, and no positional arguments")
	}
	host, port, err := net.SplitHostPort(*address)
	if err != nil {
		return fmt.Errorf("invalid healthcheck listener address: %w", err)
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	ctx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet,
		"http://"+net.JoinHostPort(host, port)+"/readyz", nil)
	if err != nil {
		return err
	}
	client := &http.Client{Timeout: *timeout}
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("readiness returned HTTP %d", response.StatusCode)
	}
	return nil
}
