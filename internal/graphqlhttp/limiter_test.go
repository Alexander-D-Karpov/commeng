package graphqlhttp

import (
	"testing"
	"time"
)

func TestRateLimiterSeparatesVisitors(t *testing.T) {
	limiter := newLimiter()

	for attempt := 0; attempt < 300; attempt++ {
		if !limiter.allow("actor:visitor-one", 300) {
			t.Fatalf("visitor one was limited at request %d", attempt)
		}
	}
	if limiter.allow("actor:visitor-one", 300) {
		t.Fatal("visitor one exceeded its rate limit")
	}
	if !limiter.allow("actor:visitor-two", 300) {
		t.Fatal("visitor two inherited visitor one's limit")
	}
	if !limiter.allow("ip:192.0.2.1", 5000) {
		t.Fatal("separate coarse IP quota was unexpectedly exhausted")
	}
}

func TestRootMutationNamesCannotSkipLimiter(t *testing.T) {
	mutations := []string{
		"register", "login", "logout", "createPost", "createComment",
		"setPostCommentsEnabled", "likeComment", "unlikeComment", "moderateComment",
	}
	for _, name := range mutations {
		if !isMutationField(name) {
			t.Errorf("%q bypasses the root mutation limiter", name)
		}
	}
	for _, name := range []string{"posts", "post", "comment", "commentJump", "viewer", "commentAncestors"} {
		if isMutationField(name) {
			t.Errorf("query field %q incorrectly consumes mutation quota", name)
		}
	}
}

func TestRateLimitWindowResets(t *testing.T) {
	limit := newLimiter()
	at := time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC)
	if !limit.allowAt("test", 1, at) || limit.allowAt("test", 1, at.Add(59*time.Second)) {
		t.Fatal("limiter did not reject a request inside the same window")
	}
	if !limit.allowAt("test", 1, at.Add(time.Minute)) {
		t.Fatal("limiter did not reset after one minute")
	}
}

func TestBenchmarkModeRequiresExplicitDisable(t *testing.T) {
	limit := newLimiter()
	if !limit.allow("k", 1) || limit.allow("k", 1) {
		t.Fatal("normal limiter unexpectedly disabled")
	}
	limit.disable()
	for i := 0; i < 1000; i++ {
		if !limit.allow("k", 1) {
			t.Fatal("explicit benchmark mode still limited")
		}
	}
}
