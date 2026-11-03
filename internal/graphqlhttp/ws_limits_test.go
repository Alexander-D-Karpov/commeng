package graphqlhttp

import (
	"testing"
	"time"
)

func TestWebSocketPerConnectionBudgets(t *testing.T) {
	budget := &websocketBudget{}
	now := time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC)
	releases := make([]func(), 0, maxWebSocketSubscriptions)
	for i := 0; i < maxWebSocketSubscriptions; i++ {
		release, ok := budget.acquire(true, now)
		if !ok {
			t.Fatalf("subscription %d rejected", i)
		}
		releases = append(releases, release)
	}
	if _, ok := budget.acquire(true, now); ok {
		t.Fatal("accepted too many subscriptions")
	}
	releases[0]()
	releases[0]() // idempotent
	if release, ok := budget.acquire(true, now); !ok {
		t.Fatal("did not release subscription slot")
	} else {
		release()
	}
	for _, release := range releases {
		release()
	}
	for i := budget.operations; i < maxWebSocketOperationsMinute; i++ {
		release, ok := budget.acquire(false, now)
		if !ok {
			t.Fatalf("operation %d rejected before window cap", i)
		}
		release()
	}
	if _, ok := budget.acquire(false, now); ok {
		t.Fatal("operation rate limit not applied")
	}
	if release, ok := budget.acquire(false, now.Add(time.Minute)); !ok {
		t.Fatal("operation window did not reset")
	} else {
		release()
	}
}

func TestWebSocketConnectionPerIPCap(t *testing.T) {
	tracker := newWebsocketConnections()
	var releases []func()
	for i := 0; i < maxWebSocketsPerIP; i++ {
		release, ok := tracker.acquire("192.0.2.1")
		if !ok {
			t.Fatalf("connection %d rejected", i)
		}
		releases = append(releases, release)
	}
	if _, ok := tracker.acquire("192.0.2.1"); ok {
		t.Fatal("accepted too many sockets for one IP")
	}
	if release, ok := tracker.acquire("192.0.2.2"); !ok {
		t.Fatal("different IP was limited")
	} else {
		release()
	}
	for _, release := range releases {
		release()
	}
	if len(tracker.counts) != 0 {
		t.Fatalf("connection slots leaked: %+v", tracker.counts)
	}
}
