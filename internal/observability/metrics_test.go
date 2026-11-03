package observability

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestTelemetryBoundedLabelsAndPrivateMetricsHandler(t *testing.T) {
	metrics := NewMetrics()
	metrics.BindSubscriptions(func() int { return 2 })
	metrics.ObserveGraphQL("mutation", time.Now().Add(-time.Millisecond))
	metrics.ObserveGraphQL("an-unbounded-user-provided-type", time.Now().Add(-time.Millisecond))
	metrics.ObservePostgreSQL("SELECT", time.Now().Add(-time.Millisecond), false)
	metrics.ObservePostgreSQL("SELECT * FROM private.user", time.Now(), true)

	api := metrics.Instrument(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
	}))
	response := httptest.NewRecorder()
	api.ServeHTTP(response, httptest.NewRequest("WEIRD_REQUEST", "/unmatched/unknown", nil))
	if response.Code != http.StatusCreated {
		t.Fatalf("instrumented status = %d", response.Code)
	}
	response = httptest.NewRecorder()
	metrics.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("metrics HTTP status: %d", response.Code)
	}
	text := response.Body.String()
	for _, expected := range []string{
		`commeng_http_requests_total{method="OTHER",route="other",status="201"} 1`,
		`commeng_graphql_operations_total{type="other"} 1`,
		`commeng_graphql_operations_total{type="mutation"} 1`,
		`commeng_active_subscriptions 2`,
		`commeng_db_queries_total{operation="SELECT",result="ok"} 1`,
		`commeng_db_queries_total{operation="OTHER",result="error"} 1`,
	} {
		if !strings.Contains(text, expected) {
			t.Errorf("missing metrics series %q", expected)
		}
	}
	if strings.Contains(text, "WEIRD_REQUEST") || strings.Contains(text, "an-unbounded-user-provided-type") ||
		strings.Contains(text, "private.user") {
		t.Fatal("user input leaked into metric cardinality")
	}
}
