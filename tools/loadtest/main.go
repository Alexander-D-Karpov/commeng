// Command loadtest measures read/write or WebSocket setup latency against a
// running commeng API. It defaults to a low request rate so the application
// rate limiter is not mistaken for database throughput.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	coderws "github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

type profile string

const (
	postsProfile  profile = "posts"
	threadProfile profile = "thread"
	writeProfile  profile = "write"
	wsProfile     profile = "ws"
)

type options struct {
	URL          string
	Profile      profile
	PostID       string
	Concurrency  int
	Seconds      int
	Rate         int
	AllowRemote  bool
	AllowWrites  bool
	Unrestricted bool
}

type outcome struct {
	Status  int
	Failure string
	Latency time.Duration
}

type report struct {
	mu        sync.Mutex
	latencies []time.Duration
	reasons   map[string]int64
	requests  atomic.Int64
	passed    atomic.Int64
	dropped   atomic.Int64
}

func main() {
	cfg := options{}
	flag.StringVar(&cfg.URL, "url", "http://127.0.0.1:8080/graphql", "GraphQL POST URL")
	flag.StringVar((*string)(&cfg.Profile), "profile", "posts", "posts|thread|write|ws")
	flag.StringVar(&cfg.PostID, "post-id", "", "existing post ID (required for thread/write/ws)")
	flag.IntVar(&cfg.Concurrency, "concurrency", 16, "parallel workers (1..500)")
	flag.IntVar(&cfg.Seconds, "seconds", 30, "duration in seconds (1..600)")
	flag.IntVar(&cfg.Rate, "rate", 3, "global requests/second; 0 disables pacing on loopback only")
	flag.BoolVar(&cfg.AllowRemote, "allow-remote", false, "allow non-loopback targets")
	flag.BoolVar(&cfg.AllowWrites, "allow-writes", false, "allow mutations that persist benchmark comments")
	flag.BoolVar(&cfg.Unrestricted, "unrestricted", false, "explicitly allow unpaced loopback requests")
	flag.Parse()
	if flag.NArg() > 0 {
		fatal("unexpected positional arguments")
	}
	if err := cfg.validate(); err != nil {
		fatal(err.Error())
	}
	result := run(cfg)
	result.print(cfg)
	if result.requests.Load() == 0 || result.requests.Load() != result.passed.Load() {
		os.Exit(1)
	}
}

func (cfg options) validate() error {
	if cfg.Concurrency < 1 || cfg.Concurrency > 500 || cfg.Seconds < 1 || cfg.Seconds > 600 || cfg.Rate < 0 ||
		cfg.Rate > 1000 {
		return errors.New("concurrency=1..500 seconds=1..600 rate=0..1000")
	}
	parsed, err := url.Parse(cfg.URL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Hostname() == "" ||
		parsed.Path != "/graphql" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return errors.New("target must be http(s)://host/graphql without credentials or query")
	}
	local := isLoopback(parsed.Hostname())
	if !local && !cfg.AllowRemote {
		return errors.New("non-loopback target requires --allow-remote")
	}
	if cfg.Profile != postsProfile && cfg.Profile != threadProfile && cfg.Profile != writeProfile &&
		cfg.Profile != wsProfile {
		return errors.New("profile must be posts, thread, write or ws")
	}
	if cfg.Profile != postsProfile {
		if _, err := parsePositiveID(cfg.PostID); err != nil {
			return errors.New("--post-id must be a positive integer for thread/write/ws")
		}
	}
	if cfg.Profile == writeProfile && !cfg.AllowWrites {
		return errors.New("write profile creates persistent comments; explicitly pass --allow-writes")
	}
	if cfg.Rate == 0 && (!cfg.Unrestricted || !local) {
		return errors.New("unpaced load requires loopback target and --unrestricted")
	}
	return nil
}

func isLoopback(host string) bool {
	return strings.EqualFold(host, "localhost") || (net.ParseIP(host) != nil && net.ParseIP(host).IsLoopback())
}

func parsePositiveID(raw string) (int64, error) {
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id < 1 || strconv.FormatInt(id, 10) != raw {
		return 0, errors.New("invalid ID")
	}
	return id, nil
}

func graphQLBody(cfg options, sequence int64) []byte {
	var query string
	switch cfg.Profile {
	case threadProfile:
		query = fmt.Sprintf(
			`query BenchmarkThread { post(id:%q) { id comments(first:20) {
				edges { node { id body replyCount } } pageInfo { endCursor hasNextPage }
			} } }`,
			cfg.PostID,
		)
	case writeProfile:
		query = fmt.Sprintf(
			`mutation BenchmarkWrite { createComment(input:{postId:%q,name:"Benchmark",
				email:"bench@example.invalid",body:%q}) { id } }`,
			cfg.PostID,
			fmt.Sprintf("Benchmark write %d", sequence),
		)
	default:
		query = `query BenchmarkPosts { posts(first:20) {
			edges { node { id title commentCount } } pageInfo { endCursor hasNextPage }
		} }`
	}
	body, _ := json.Marshal(map[string]string{"query": query})
	return body
}

func run(cfg options) *report {
	result := &report{reasons: make(map[string]int64)}
	result.latencies = make([]time.Duration, 0, 262144)
	client := &http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{
		MaxIdleConns: cfg.Concurrency * 2, MaxIdleConnsPerHost: cfg.Concurrency * 2,
	}}
	defer client.CloseIdleConnections()
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(cfg.Seconds)*time.Second)
	defer cancel()
	var ticks <-chan time.Time
	if cfg.Rate > 0 {
		ticker := time.NewTicker(time.Second / time.Duration(cfg.Rate))
		defer ticker.Stop()
		ticks = ticker.C
	}
	var sequence atomic.Int64
	var workers sync.WaitGroup
	for i := 0; i < cfg.Concurrency; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for ctx.Err() == nil {
				if ticks != nil {
					select {
					case <-ctx.Done():
						return
					case <-ticks:
					}
				}
				var sample outcome
				if cfg.Profile == wsProfile {
					sample = websocketSetup(ctx, cfg)
				} else {
					sample = httpOperation(ctx, client, cfg, sequence.Add(1))
				}
				if ctx.Err() != nil && sample.Failure == "cancelled" {
					return
				}
				result.record(sample)
			}
		}()
	}
	workers.Wait()
	return result
}

func httpOperation(ctx context.Context, client *http.Client, cfg options, sequence int64) outcome {
	request, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		cfg.URL,
		bytes.NewReader(graphQLBody(cfg, sequence)),
	)
	if err != nil {
		return outcome{Failure: "request"}
	}
	request.Header.Set("Content-Type", "application/json")
	started := time.Now()
	response, err := client.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return outcome{Failure: "cancelled"}
		}
		return outcome{Failure: "network", Latency: time.Since(started)}
	}
	defer func() { _ = response.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	// Record the complete HTTP response, not just time-to-headers.
	latency := time.Since(started)
	if err != nil {
		return outcome{Status: response.StatusCode, Failure: "body", Latency: latency}
	}
	if response.StatusCode != http.StatusOK {
		return outcome{
			Status:  response.StatusCode,
			Failure: fmt.Sprintf("HTTP_%d", response.StatusCode),
			Latency: latency,
		}
	}
	var decoded struct {
		Errors []json.RawMessage `json:"errors"`
		Data   json.RawMessage   `json:"data"`
	}
	if err := json.Unmarshal(body, &decoded); err != nil {
		return outcome{Status: response.StatusCode, Failure: "invalid_json", Latency: latency}
	}
	if len(decoded.Errors) > 0 || !validGraphQLData(decoded.Data, cfg.Profile) {
		return outcome{Status: response.StatusCode, Failure: "graphql_error", Latency: latency}
	}
	return outcome{Status: http.StatusOK, Latency: latency}
}

// A successful HTTP 200 with `data.post: null` is not a successful thread
// benchmark, nor is a rejected mutation with no created comment.
func validGraphQLData(data json.RawMessage, kind profile) bool {
	var fields map[string]json.RawMessage
	if len(data) == 0 || json.Unmarshal(data, &fields) != nil {
		return false
	}
	field := "posts"
	switch kind {
	case threadProfile:
		field = "post"
	case writeProfile:
		field = "createComment"
	}
	value := bytes.TrimSpace(fields[field])
	return len(value) > 0 && !bytes.Equal(value, []byte("null"))
}

// WS profile measures connection_init/ack and subscribe submission, NOT the
// latency of event delivery. Event delivery requires a separate producer.
func websocketSetup(ctx context.Context, cfg options) outcome {
	parsed, _ := url.Parse(cfg.URL)
	if parsed.Scheme == "https" {
		parsed.Scheme = "wss"
	} else {
		parsed.Scheme = "ws"
	}
	operationCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	start := time.Now()
	conn, _, err := coderws.Dial(operationCtx, parsed.String(), &coderws.DialOptions{
		Subprotocols: []string{"graphql-transport-ws"},
	})
	if err != nil {
		return outcome{Failure: failureFromContext(ctx, "ws_dial"), Latency: time.Since(start)}
	}
	defer func() { _ = conn.Close(coderws.StatusNormalClosure, "benchmark complete") }()
	if err := wsjson.Write(operationCtx, conn, map[string]any{"type": "connection_init"}); err != nil {
		return outcome{Failure: failureFromContext(ctx, "ws_init"), Latency: time.Since(start)}
	}
	var ack struct {
		Type string `json:"type"`
	}
	if err := wsjson.Read(operationCtx, conn, &ack); err != nil || ack.Type != "connection_ack" {
		return outcome{Failure: failureFromContext(ctx, "ws_ack"), Latency: time.Since(start)}
	}
	query := fmt.Sprintf(`subscription {commentAdded(postId:%q){id}}`, cfg.PostID)
	if err := wsjson.Write(operationCtx, conn, map[string]any{
		"id": "bench", "type": "subscribe", "payload": map[string]string{"query": query},
	}); err != nil {
		return outcome{Failure: failureFromContext(ctx, "ws_subscribe"), Latency: time.Since(start)}
	}
	return outcome{Status: http.StatusOK, Latency: time.Since(start)}
}

func failureFromContext(ctx context.Context, fallback string) string {
	if ctx.Err() != nil {
		return "cancelled"
	}
	return fallback
}

func (r *report) record(o outcome) {
	r.requests.Add(1)
	if o.Failure == "" {
		r.passed.Add(1)
	}
	r.mu.Lock()
	if o.Failure != "" {
		r.reasons[o.Failure]++
	}
	if len(r.latencies) < 262144 && o.Failure == "" {
		r.latencies = append(r.latencies, o.Latency)
	} else if o.Failure == "" {
		r.dropped.Add(1)
	}
	r.mu.Unlock()
}

func (r *report) print(cfg options) {
	requests := r.requests.Load()
	succeeded := r.passed.Load()
	fmt.Printf("Target: %s\nProfile: %s\nRequests: %d; passed: %d; failures: %d\n",
		cfg.URL, cfg.Profile, requests, succeeded, requests-succeeded)
	fmt.Printf("Duration: %ds; throughput: %.1f requests/s; configured rate: %d/s\n",
		cfg.Seconds, float64(requests)/float64(cfg.Seconds), cfg.Rate)
	r.mu.Lock()
	defer r.mu.Unlock()
	keys := make([]string, 0, len(r.reasons))
	for reason := range r.reasons {
		keys = append(keys, reason)
	}
	sort.Strings(keys)
	for _, reason := range keys {
		fmt.Printf("  %s: %d\n", reason, r.reasons[reason])
	}
	sort.Slice(r.latencies, func(i, j int) bool { return r.latencies[i] < r.latencies[j] })
	fmt.Printf("Successful latency samples: %d (dropped: %d)\n", len(r.latencies), r.dropped.Load())
	if len(r.latencies) > 0 {
		fmt.Printf(
			"p50: %s; p95: %s; p99: %s; max: %s\n",
			percentile(
				r.latencies,
				50,
			),
			percentile(r.latencies, 95),
			percentile(r.latencies, 99),
			r.latencies[len(r.latencies)-1],
		)
	}
	if cfg.Profile == wsProfile {
		fmt.Println("WS timing covers connect + connection_ack + subscribe submission, NOT event delivery.")
	}
}

func percentile(sorted []time.Duration, p int) time.Duration {
	if len(sorted) == 0 {
		return 0
	}
	index := (len(sorted)*p+99)/100 - 1
	if index < 0 {
		index = 0
	}
	if index >= len(sorted) {
		index = len(sorted) - 1
	}
	return sorted[index]
}
func fatal(message string) { fmt.Fprintln(os.Stderr, message); os.Exit(2) }
