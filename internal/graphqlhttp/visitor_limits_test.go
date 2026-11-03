package graphqlhttp

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestVisitorTokenIssueLimitByClientIP(t *testing.T) {
	handler := testHandler(t).Handler()
	for attempt := 0; attempt < 21; attempt++ {
		request := httptest.NewRequest(http.MethodPost, "/auth/visitor", nil)
		request.RemoteAddr = "203.0.113.8:51777"
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		expected := http.StatusOK
		if attempt == 20 {
			expected = http.StatusTooManyRequests
		}
		if response.Code != expected {
			t.Fatalf("attempt %d status %d, expected %d", attempt+1, response.Code, expected)
		}
	}
}
