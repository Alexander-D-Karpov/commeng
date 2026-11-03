package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestPercentile(t *testing.T) {
	values := make([]time.Duration, 100)
	for i := range values {
		values[i] = time.Duration(i+1) * time.Millisecond
	}
	for _, test := range []struct {
		p    int
		want time.Duration
	}{{50, 50 * time.Millisecond}, {95, 95 * time.Millisecond}, {99, 99 * time.Millisecond}} {
		if got := percentile(values, test.p); got != test.want {
			t.Fatalf("p%d = %v want %v", test.p, got, test.want)
		}
	}
}

func TestLoadtestValidation(t *testing.T) {
	base := options{URL: "http://127.0.0.1:8080/graphql", Profile: postsProfile, Concurrency: 4, Seconds: 2, Rate: 3}
	if err := base.validate(); err != nil {
		t.Fatal(err)
	}
	write := base
	write.Profile, write.PostID = writeProfile, "1"
	if err := write.validate(); err == nil {
		t.Fatal("write without confirmation accepted")
	}
	write.AllowWrites = true
	if err := write.validate(); err != nil {
		t.Fatal(err)
	}
	write.PostID = "-1"
	if err := write.validate(); err == nil {
		t.Fatal("bad post ID accepted")
	}
	remote := base
	remote.URL = "https://blog.example.org/graphql"
	if err := remote.validate(); err == nil {
		t.Fatal("remote test without opt-in accepted")
	}
	base.Rate = 0
	if err := base.validate(); err == nil {
		t.Fatal("unpaced load without opt-in accepted")
	}
	base.Unrestricted = true
	if err := base.validate(); err != nil {
		t.Fatal(err)
	}
}

func TestLoadtestOperationPayload(t *testing.T) {
	cfg := options{Profile: writeProfile, PostID: "42"}
	payload := string(graphQLBody(cfg, 123))
	if !strings.Contains(payload, "Benchmark write 123") || !strings.Contains(payload, "42") {
		t.Fatalf("unexpected mutation payload %q", payload)
	}
}

func TestGraphQLBodyMustContainTheRequestedResource(t *testing.T) {
	for _, item := range []struct {
		profile profile
		body    string
		ok      bool
	}{
		{postsProfile, `{"posts":{"edges":[]}}`, true},
		{threadProfile, `{"post":null}`, false},
		{threadProfile, `{"post":{"id":"1"}}`, true},
		{writeProfile, `{"createComment":null}`, false},
		{writeProfile, `{"createComment":{"id":"3"}}`, true},
		{writeProfile, `{"posts":{"edges":[]}}`, false},
		{postsProfile, `null`, false},
	} {
		if got := validGraphQLData(json.RawMessage(item.body), item.profile); got != item.ok {
			t.Errorf("profile=%s body=%q: got %t, want %t", item.profile, item.body, got, item.ok)
		}
	}
}
