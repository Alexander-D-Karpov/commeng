package observability

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// ConfigureTracing installs W3C propagation and an OTLP HTTP exporter if an
// endpoint was explicitly configured. No telemetry is sent anywhere by default.
func ConfigureTracing(ctx context.Context) (func(context.Context) error, error) {
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{}, propagation.Baggage{},
	))
	if strings.TrimSpace(os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")) == "" &&
		strings.TrimSpace(os.Getenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT")) == "" {
		return func(context.Context) error { return nil }, nil
	}
	rate := 0.1
	if raw := os.Getenv("OTEL_SAMPLE_RATE"); raw != "" {
		parsed, err := strconv.ParseFloat(raw, 64)
		if err != nil || parsed < 0 || parsed > 1 {
			return nil, fmt.Errorf("OTEL_SAMPLE_RATE must be between 0 and 1")
		}
		rate = parsed
	}
	exporter, err := otlptracehttp.New(ctx)
	if err != nil {
		return nil, fmt.Errorf("initialize OTLP HTTP exporter: %w", err)
	}
	res, err := resource.New(ctx, resource.WithAttributes(attribute.String("service.name", "commeng")))
	if err != nil {
		return nil, fmt.Errorf("initialize trace resource: %w", err)
	}
	provider := sdktrace.NewTracerProvider(sdktrace.WithBatcher(exporter),
		sdktrace.WithSampler(sdktrace.ParentBased(sdktrace.TraceIDRatioBased(rate))),
		sdktrace.WithResource(res))
	otel.SetTracerProvider(provider)
	return provider.Shutdown, nil
}
