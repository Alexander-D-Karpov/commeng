package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHealthcheckExplicitAddress(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/readyz" {
			t.Errorf("requested %q", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	if err := healthcheck(context.Background(), "--addr", strings.TrimPrefix(server.URL, "http://")); err != nil {
		t.Fatalf("healthy server rejected: %v", err)
	}
	address := strings.TrimPrefix(server.URL, "http://")
	if err := run(context.Background(), []string{"healthcheck", "--addr", address}); err != nil {
		t.Fatalf("healthcheck command rejected healthy server: %v", err)
	}
	if err := healthcheck(context.Background(), "--timeout", "0"); err == nil {
		t.Fatal("accepted zero timeout")
	}
}
