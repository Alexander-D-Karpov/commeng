// Package observability keeps telemetry isolated from the service and store.
// Fixed, low-cardinality metric labels prevent untrusted GraphQL input from
// producing unlimited time series.
package observability

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

// Metrics owns its own registry, so tests can construct multiple APIs safely.
type Metrics struct {
	registry  *prometheus.Registry
	httpCount *prometheus.CounterVec
	httpDur   *prometheus.HistogramVec
	gqlCount  *prometheus.CounterVec
	gqlDur    *prometheus.HistogramVec
	dbCount   *prometheus.CounterVec
	dbDur     *prometheus.HistogramVec
}

func NewMetrics() *Metrics {
	registry := prometheus.NewRegistry()
	m := &Metrics{registry: registry}
	m.httpCount = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "commeng_http_requests_total", Help: "HTTP requests by bounded route, method and status.",
	}, []string{"route", "method", "status"})
	m.httpDur = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name: "commeng_http_request_duration_seconds", Help: "HTTP request durations in seconds.",
		Buckets: prometheus.DefBuckets,
	}, []string{"route", "method"})
	m.gqlCount = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "commeng_graphql_operations_total", Help: "GraphQL operations by type.",
	}, []string{"type"})
	m.gqlDur = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name: "commeng_graphql_operation_duration_seconds", Help: "GraphQL operation durations in seconds.",
		Buckets: prometheus.DefBuckets,
	}, []string{"type"})
	m.dbCount = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "commeng_db_queries_total", Help: "PostgreSQL operations by bounded SQL verb and result.",
	}, []string{"operation", "result"})
	m.dbDur = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name: "commeng_db_query_duration_seconds", Help: "Database operation latency in seconds.",
		Buckets: prometheus.DefBuckets,
	}, []string{"operation"})
	registry.MustRegister(m.httpCount, m.httpDur, m.gqlCount, m.gqlDur, m.dbCount, m.dbDur)
	registry.MustRegister(collectors.NewGoCollector())
	registry.MustRegister(collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))
	return m
}

func (m *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{EnableOpenMetrics: true})
}

// BindPostgreSQLPool publishes bounded connection counts without user labels.
// Gauges read the current pgxpool snapshot on each Prometheus scrape.
func (m *Metrics) BindPostgreSQLPool(snapshot func() (acquired, idle, total, maximum int32)) {
	for _, item := range []struct {
		name, help string
		pick       func(int32, int32, int32, int32) int32
	}{
		{"commeng_db_pool_acquired_connections", "Currently acquired PostgreSQL connections.",
			func(a, _, _, _ int32) int32 { return a }},
		{"commeng_db_pool_idle_connections", "Currently idle PostgreSQL connections.",
			func(_, b, _, _ int32) int32 { return b }},
		{"commeng_db_pool_total_connections", "Total PostgreSQL connections.", func(_, _, c, _ int32) int32 { return c }},
		{"commeng_db_pool_max_connections", "Configured PostgreSQL pool limit.", func(_, _, _, d int32) int32 { return d }},
	} {
		item := item
		m.registry.MustRegister(prometheus.NewGaugeFunc(prometheus.GaugeOpts{
			Name: item.name, Help: item.help,
		}, func() float64 {
			a, b, c, d := snapshot()
			return float64(item.pick(a, b, c, d))
		}))
	}
}

func (m *Metrics) BindSubscriptions(current func() int) {
	m.registry.MustRegister(prometheus.NewGaugeFunc(prometheus.GaugeOpts{
		Name: "commeng_active_subscriptions", Help: "Currently active local GraphQL subscriptions.",
	}, func() float64 { return float64(current()) }))
}

func (m *Metrics) ObserveGraphQL(kind string, start time.Time) {
	switch kind {
	case "query", "mutation", "subscription":
	default:
		kind = "other"
	}
	m.gqlCount.WithLabelValues(kind).Inc()
	m.gqlDur.WithLabelValues(kind).Observe(time.Since(start).Seconds())
}

// ObservePostgreSQL never uses raw SQL or user-supplied text as a label.
func (m *Metrics) ObservePostgreSQL(operation string, start time.Time, failed bool) {
	if operation != "SELECT" && operation != "INSERT" && operation != "UPDATE" &&
		operation != "DELETE" && operation != "WITH" && operation != "BEGIN" &&
		operation != "COMMIT" && operation != "ROLLBACK" && operation != "LISTEN" {
		operation = "OTHER"
	}
	result := "ok"
	if failed {
		result = "error"
	}
	m.dbCount.WithLabelValues(operation, result).Inc()
	m.dbDur.WithLabelValues(operation).Observe(time.Since(start).Seconds())
}

func methodLabel(method string) string {
	switch method {
	case http.MethodGet,
		http.MethodHead,
		http.MethodPost,
		http.MethodPut,
		http.MethodPatch,
		http.MethodDelete,
		http.MethodOptions:
		return method
	default:
		return "OTHER"
	}
}

func routeLabel(path string) string {
	switch path {
	case "/", "/graphql", "/schema.graphql", "/docs", "/healthz", "/readyz", "/auth/visitor":
		return path
	}
	if strings.HasPrefix(path, "/docs/") {
		return "/docs/*"
	}
	if strings.HasPrefix(path, "/p/") {
		return "/p/{post}/c/{comment}"
	}
	return "other"
}

// Instrument wraps the whole API, including failures before GraphQL parsing.
// The response wrapper preserves WebSocket hijacking and normal streaming.
func (m *Metrics) Instrument(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		route := routeLabel(r.URL.Path)
		method := methodLabel(r.Method)
		ctx := otel.GetTextMapPropagator().Extract(r.Context(), propagation.HeaderCarrier(r.Header))
		ctx, span := otel.Tracer("commeng/http").Start(ctx, r.Method+" "+route,
			trace.WithSpanKind(trace.SpanKindServer),
			trace.WithAttributes(attribute.String("http.route", route), attribute.String("http.method", method)))
		start := time.Now()
		recorder := &statusWriter{ResponseWriter: w}
		defer func() {
			if recorder.status == 0 {
				recorder.status = http.StatusOK
			}
			span.SetAttributes(attribute.Int("http.status_code", recorder.status))
			span.End()
			m.httpCount.WithLabelValues(route, method, strconv.Itoa(recorder.status)).Inc()
			m.httpDur.WithLabelValues(route, method).Observe(time.Since(start).Seconds())
		}()
		next.ServeHTTP(recorder, r.WithContext(ctx))
	})
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	if w.status != 0 {
		return
	}
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}
func (w *statusWriter) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.ResponseWriter.Write(p)
}
func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
func (w *statusWriter) Flush()                      { _ = http.NewResponseController(w.ResponseWriter).Flush() }
func (w *statusWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return http.NewResponseController(w.ResponseWriter).Hijack()
}
func (w *statusWriter) ReadFrom(reader io.Reader) (int64, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	if rf, ok := w.ResponseWriter.(io.ReaderFrom); ok {
		return rf.ReadFrom(reader)
	}
	return io.Copy(w.ResponseWriter, reader)
}

// TraceOperation creates a nested application span; no user-supplied names are
// used for attributes or time-series labels.
func TraceOperation(ctx context.Context, operation string) (context.Context, func()) {
	ctx, span := otel.Tracer("commeng/graphql").Start(ctx, fmt.Sprintf("graphql.%s", operation))
	return ctx, func() { span.End() }
}
