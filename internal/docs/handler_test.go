package docs

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDocumentationAssets(t *testing.T) {
	tests := []struct {
		name, path, contentType, contains string
		status                            int
	}{
		{"apollo root", "/", "text/html", "embeddable-sandbox.cdn.apollographql.com", http.StatusOK},
		{"stylesheet", "/styles.css", "text/css", "#sandbox", http.StatusOK},
		{"initialization", "/app.js", "javascript", "EmbeddedSandbox", http.StatusOK},
		{"missing", "/missing", "text/plain", "404", http.StatusNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			record := httptest.NewRecorder()
			Handler().ServeHTTP(record, httptest.NewRequest(http.MethodGet, tt.path, nil))
			if record.Code != tt.status {
				t.Fatalf("status = %d, want %d", record.Code, tt.status)
			}
			if !strings.Contains(record.Header().Get("Content-Type"), tt.contentType) {
				t.Fatalf("unexpected Content-Type: %q", record.Header().Get("Content-Type"))
			}
			if !strings.Contains(record.Body.String(), tt.contains) {
				t.Fatalf("body missing %q", tt.contains)
			}
			if !strings.Contains(
				record.Header().Get("Content-Security-Policy"),
				"frame-src https://*.apollographql.com",
			) {
				t.Fatal("Apollo embed not allowed by CSP")
			}
		})
	}
}

func TestSandboxInitialQueryIsPlainText(t *testing.T) {
	record := httptest.NewRecorder()
	Handler().ServeHTTP(record, httptest.NewRequest(http.MethodGet, "/app.js", nil))
	if record.Code != http.StatusOK || !strings.Contains(record.Body.String(), "document: 'query Posts") {
		t.Fatal("sandbox initial query must be passed as plain GraphQL text")
	}
}
