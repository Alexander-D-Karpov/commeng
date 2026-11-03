package observability

import (
	"context"
	"strings"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// PostgreSQLTracer exposes DB client spans without recording raw SQL,
// arguments, emails, identifiers or other potentially personal content.
// pgx calls TraceQueryEnd with the context returned by TraceQueryStart.
type PostgreSQLTracer struct{ metrics atomic.Pointer[Metrics] }

func (t *PostgreSQLTracer) BindMetrics(m *Metrics) { t.metrics.Store(m) }

type queryTraceKey struct{}
type queryTrace struct {
	operation string
	started   time.Time
}

func dbOperation(statement string) string {
	parts := strings.Fields(statement)
	if len(parts) == 0 {
		return "OTHER"
	}
	switch strings.ToUpper(parts[0]) {
	case "SELECT", "INSERT", "UPDATE", "DELETE", "WITH", "BEGIN", "COMMIT", "ROLLBACK", "LISTEN":
		return strings.ToUpper(parts[0])
	default:
		return "OTHER"
	}
}

func (t *PostgreSQLTracer) TraceQueryStart(
	ctx context.Context,
	_ *pgx.Conn,
	data pgx.TraceQueryStartData,
) context.Context {
	op := dbOperation(data.SQL)
	ctx, _ = otel.Tracer("commeng/postgresql").Start(ctx, "postgresql."+op,
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(attribute.String("db.system.name", "postgresql"),
			attribute.String("db.operation.name", op)))
	return context.WithValue(ctx, queryTraceKey{}, queryTrace{operation: op, started: time.Now()})
}

func (t *PostgreSQLTracer) TraceQueryEnd(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryEndData) {
	span := trace.SpanFromContext(ctx)
	if data.Err != nil {
		span.SetStatus(codes.Error, "postgresql query failed")
		// Never include driver errors or SQL text in span attributes as they
		// may contain sensitive data or database connection settings.
	}
	span.End()
	if m := t.metrics.Load(); m != nil {
		if query, ok := ctx.Value(queryTraceKey{}).(queryTrace); ok {
			m.ObservePostgreSQL(query.operation, query.started, data.Err != nil)
		}
	}
}
