package graphqlhttp

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Alexander-D-Karpov/commeng/internal/auth"
	"github.com/Alexander-D-Karpov/commeng/internal/docs"
	"github.com/Alexander-D-Karpov/commeng/internal/graph"
	"github.com/Alexander-D-Karpov/commeng/internal/observability"
	"github.com/Alexander-D-Karpov/commeng/internal/pagination"
	"github.com/Alexander-D-Karpov/commeng/internal/service"
)

type Config struct {
	BaseURL             string
	AllowedOrigins      []string
	TrustedProxy        *net.IPNet
	SigningSecret       []byte
	UnsafeBenchmarkMode bool // only enabled after strict runtime validation
}

type Server struct {
	service  *service.Service
	config   Config
	visitors *auth.Visitors
	graphql  http.Handler
	origins  map[string]struct{}
	limiter  *limiter
	metrics  *observability.Metrics
	sockets  *websocketConnections
}

func New(svc *service.Service, config Config) (*Server, error) {
	base, err := url.Parse(strings.TrimRight(config.BaseURL, "/"))
	if err != nil || base.Host == "" || (base.Scheme != "http" && base.Scheme != "https") ||
		base.User != nil || base.RawQuery != "" || base.Fragment != "" || base.Path != "" {
		return nil, fmt.Errorf("PUBLIC_BASE_URL must be an HTTP(S) origin")
	}
	config.BaseURL = base.String()
	cursors, err := pagination.NewSigner(config.SigningSecret)
	if err != nil {
		return nil, err
	}
	visitors, err := auth.NewVisitors(config.SigningSecret)
	if err != nil {
		return nil, err
	}
	origins := make(map[string]struct{})
	origins[strings.TrimRight(config.BaseURL, "/")] = struct{}{}
	// The default development origin is localhost, while make run binds to
	// 127.0.0.1. Permit that *specific* loopback alias so the /docs console
	// works from either address, without accepting arbitrary Host headers.
	if base.Scheme == "http" {
		loopbackHost := ""
		switch strings.ToLower(base.Hostname()) {
		case "localhost":
			loopbackHost = "127.0.0.1"
		case "127.0.0.1":
			loopbackHost = "localhost"
		}
		if loopbackHost != "" {
			if port := base.Port(); port != "" {
				loopbackHost = net.JoinHostPort(loopbackHost, port)
			}
			origins["http://"+loopbackHost] = struct{}{}
		}
	}
	for _, origin := range config.AllowedOrigins {
		origins[strings.TrimRight(origin, "/")] = struct{}{}
	}

	server := &Server{
		service:  svc,
		config:   config,
		visitors: visitors,
		origins:  origins,
		limiter:  newLimiter(),
		sockets:  newWebsocketConnections(),
	}
	if config.UnsafeBenchmarkMode {
		server.limiter.disable()
	}
	server.graphql = newGraphQLHandler(server, cursors)
	return server, nil
}

func (s *Server) SetMetrics(metrics *observability.Metrics) { s.metrics = metrics }

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	// The {$} wildcard matches only the root path. "GET /" would also
	// match GET requests to /graphql and conflict with its all-method route.
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{
			"service": "commeng",
			"graphql": "/graphql",
			"schema":  "/schema.graphql",
			"docs":    "/docs/",
		})
	})
	// The documentation is public and contains no application frontend.
	mux.Handle("GET /docs", http.RedirectHandler("/docs/", http.StatusPermanentRedirect))
	mux.Handle("GET /docs/", http.StripPrefix("/docs/", docs.Handler()))
	mux.Handle("/graphql", http.HandlerFunc(s.graphQLRequest))
	mux.HandleFunc("GET /schema.graphql", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte(graph.Schema))
	})
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if err := s.service.Ping(ctx); err != nil ||
			(s.service.DistributedEvents && s.service.EventFollower != nil && !s.service.EventFollower.Ready()) {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "unavailable"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
	})
	mux.HandleFunc("POST /auth/visitor", func(w http.ResponseWriter, r *http.Request) {
		ip, _ := r.Context().Value(remoteIPKey{}).(string)
		if !s.limiter.allow("visitor-ip:"+ip, 20) {
			writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "visitor issuance rate limit exceeded"})
			return
		}
		token, err := s.visitors.Issue()
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusOK, map[string]string{"token": token})
	})
	mux.HandleFunc("GET /p/{post}/c/{comment}", s.permalink)
	secured := s.security(mux)
	if s.metrics != nil {
		return s.metrics.Instrument(secured)
	}
	return secured
}
func (s *Server) graphQLRequest(w http.ResponseWriter, r *http.Request) {
	upgrade := r.Method == http.MethodGet && strings.EqualFold(r.Header.Get("Upgrade"), "websocket")
	if r.Method != http.MethodPost && !upgrade {
		w.Header().Set("Allow", "POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if r.Method == http.MethodPost {
		r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	}
	if upgrade {
		ip, _ := r.Context().Value(remoteIPKey{}).(string)
		release, ok := s.sockets.acquire(ip)
		if !ok {
			http.Error(w, "too many websocket connections", http.StatusTooManyRequests)
			return
		}
		defer release()
		r = r.WithContext(context.WithValue(r.Context(), websocketLimitKey{}, &websocketBudget{}))
	}
	s.graphql.ServeHTTP(w, r)
}
