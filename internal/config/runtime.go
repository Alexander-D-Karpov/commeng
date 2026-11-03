// Package config validates runtime options before services are initialized.
// It intentionally depends only on the standard library.
package config

import (
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"
)

type Runtime struct {
	AppEnv              string
	Storage             string
	HTTPAddr            string
	MetricsAddr         string
	BaseURL             string
	DatabaseURL         string
	SigningKey          string
	AllowedOrigins      string
	TrustedProxyCIDR    string
	PermalinkTemplate   string
	Production          bool
	UnsafeBenchmarkMode bool
}

func FromEnvironment() Runtime {
	environment := get("APP_ENV", "development")
	return Runtime{
		AppEnv:              environment,
		Storage:             get("STORAGE", "memory"),
		HTTPAddr:            get("HTTP_ADDR", ":8080"),
		MetricsAddr:         os.Getenv("METRICS_ADDR"),
		BaseURL:             get("PUBLIC_BASE_URL", "http://localhost:8080"),
		DatabaseURL:         os.Getenv("DATABASE_URL"),
		SigningKey:          os.Getenv("COMMENG_SIGNING_KEY"),
		AllowedOrigins:      os.Getenv("ALLOWED_ORIGINS"),
		TrustedProxyCIDR:    os.Getenv("TRUSTED_PROXY_CIDR"),
		PermalinkTemplate:   os.Getenv("PERMALINK_TEMPLATE"),
		Production:          environment == "production",
		UnsafeBenchmarkMode: os.Getenv("BENCHMARK_DISABLE_RATELIMIT") == "true",
	}
}

func get(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func (settings Runtime) Validate() error {
	if err := settings.validateEnvironment(); err != nil {
		return err
	}
	if settings.Storage != "memory" && settings.Storage != "postgres" {
		return fmt.Errorf("STORAGE must be memory or postgres, got %q", settings.Storage)
	}
	if strings.TrimSpace(settings.HTTPAddr) == "" {
		return errors.New("HTTP_ADDR must not be empty")
	}
	if _, _, err := net.SplitHostPort(settings.HTTPAddr); err != nil {
		return fmt.Errorf("invalid HTTP_ADDR: %w", err)
	}
	if settings.Storage == "postgres" && settings.DatabaseURL == "" {
		return errors.New("DATABASE_URL is required for PostgreSQL storage")
	}
	origin, err := url.Parse(settings.BaseURL)
	if err != nil || origin.Host == "" || origin.Opaque != "" ||
		(origin.Scheme != "http" && origin.Scheme != "https") ||
		origin.User != nil || origin.Path != "" || origin.RawQuery != "" ||
		origin.Fragment != "" {
		return errors.New("PUBLIC_BASE_URL must be an HTTP(S) origin without path or credentials")
	}
	if err := settings.validateProduction(origin); err != nil {
		return err
	}
	if settings.PermalinkTemplate != "" &&
		!ValidPermalinkTemplate(settings.PermalinkTemplate, settings.Production) {
		return errors.New("PERMALINK_TEMPLATE needs {post}, {comment} and a valid HTTP(S) URL")
	}
	return nil
}

func (settings Runtime) validateEnvironment() error {
	if settings.AppEnv != "" && settings.AppEnv != "development" &&
		settings.AppEnv != "test" && settings.AppEnv != "production" {
		return fmt.Errorf("APP_ENV must be development, test or production, got %q", settings.AppEnv)
	}
	if settings.AppEnv == "production" && !settings.Production {
		return errors.New("APP_ENV=production must enable production validation")
	}
	if settings.UnsafeBenchmarkMode {
		if settings.AppEnv != "test" || settings.Production {
			return errors.New("BENCHMARK_DISABLE_RATELIMIT is allowed only with APP_ENV=test")
		}
		host, _, err := net.SplitHostPort(settings.HTTPAddr)
		if err != nil || (host != "localhost" && (net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback())) {
			return errors.New("benchmark mode requires a loopback-only HTTP_ADDR")
		}
	}
	return nil
}

func (settings Runtime) validateProduction(origin *url.URL) error {
	if settings.Production {
		if origin.Scheme != "https" {
			return errors.New("production requires HTTPS PUBLIC_BASE_URL")
		}
		if settings.SigningKey == "" {
			return errors.New("production requires COMMENG_SIGNING_KEY")
		}
		decoded, err := hex.DecodeString(settings.SigningKey)
		if err != nil || len(decoded) < 32 {
			return errors.New("production COMMENG_SIGNING_KEY must be at least 32 bytes of hex")
		}
		if os.Getenv("PGPASSWORD") == "commeng-dev-only" && settings.Storage == "postgres" {
			return errors.New("production refuses the Compose development database password")
		}
	}
	if settings.Production && (settings.TrustedProxyCIDR == "0.0.0.0/0" || settings.TrustedProxyCIDR == "::/0") {
		return errors.New("production refuses an unrestricted TRUSTED_PROXY_CIDR")
	}
	return nil
}

func ValidPermalinkTemplate(template string, production bool) bool {
	if !strings.Contains(template, "{post}") || !strings.Contains(template, "{comment}") {
		return false
	}
	value := strings.NewReplacer("{post}", "1", "{comment}", "1").Replace(template)
	parsed, err := url.Parse(value)
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.Opaque != "" {
		return false
	}
	if parsed.Scheme == "https" {
		return true
	}
	return !production && parsed.Scheme == "http"
}
