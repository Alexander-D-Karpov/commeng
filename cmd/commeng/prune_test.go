package main

import (
	"context"
	"strings"
	"testing"
)

func TestPruneEventsValidationBeforeDBConnect(t *testing.T) {
	for _, args := range [][]string{{"--days", "6"}, {"--limit", "10001"}, {"unexpected"}} {
		if err := pruneEvents(context.Background(), args); err == nil || !strings.Contains(err.Error(), "usage:") {
			t.Fatalf("pruneEvents(%q) error %v", args, err)
		}
	}
}
