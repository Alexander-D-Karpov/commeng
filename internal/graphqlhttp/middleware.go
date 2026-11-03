package graphqlhttp

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"net"
	"net/http"
	"strings"

	"github.com/Alexander-D-Karpov/commeng/internal/auth"
	"github.com/Alexander-D-Karpov/commeng/internal/domain"
)

func (s *Server) security(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestID := r.Header.Get("X-Request-ID")
		if requestID == "" || len(requestID) > 64 {
			var nonce [12]byte
			if _, err := rand.Read(nonce[:]); err == nil {
				requestID = hex.EncodeToString(nonce[:])
			}
		}
		w.Header().Set("X-Request-ID", requestID)
		defer func() {
			if recovered := recover(); recovered != nil {
				slog.ErrorContext(r.Context(), "HTTP panic", "request_id", requestID, "error", recovered)
				http.Error(w, "internal server error", http.StatusInternalServerError)
			}
		}()
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Cache-Control", "no-store")
		if !s.allowOrigin(w, r) {
			return
		}
		clientIP := s.clientIP(r)
		// Coarse IP cap protects even invalid credentials from exhausting DB lookups.
		if !s.limiter.allow("ip:"+clientIP, 5000) {
			http.Error(w, "too many requests", http.StatusTooManyRequests)
			return
		}
		ctx, err := s.identify(r)
		if err != nil {
			http.Error(w, "invalid credentials", http.StatusUnauthorized)
			return
		}
		// Authenticated clients behind the same NAT get independent rate limits.
		key := "anonymous:" + clientIP
		if viewer := auth.FromContext(ctx); viewer.ActorKey != "" {
			key = "actor:" + viewer.ActorKey
		}
		if !s.limiter.allow(key, 300) {
			http.Error(w, "too many requests", http.StatusTooManyRequests)
			return
		}
		ctx = context.WithValue(ctx, remoteIPKey{}, clientIP)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
func (s *Server) allowOrigin(w http.ResponseWriter, r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin != "" {
		if _, ok := s.origins[origin]; !ok {
			http.Error(w, "forbidden origin", http.StatusForbidden)
			return false
		}
		h := w.Header()
		h.Set("Access-Control-Allow-Origin", origin)
		h.Add("Vary", "Origin")
		h.Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		h.Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Visitor-Token")
		h.Set("Access-Control-Max-Age", "600")
	}
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return false
	}
	return true
}
func (s *Server) identify(r *http.Request) (context.Context, error) {
	return s.identifyTokens(r.Context(), r.Header.Get("Authorization"), r.Header.Get("X-Visitor-Token"))
}

func (s *Server) identifyTokens(ctx context.Context, authorization, visitorToken string) (context.Context, error) {
	viewer := auth.Viewer{}
	if authorization != "" {
		token, ok := strings.CutPrefix(authorization, "Bearer ")
		if !ok || token == "" {
			return ctx, domain.Fault(domain.ErrAuth, "invalid bearer token")
		}
		account, err := s.service.Account(ctx, token)
		if err != nil {
			return ctx, err
		}
		viewer.User = &account
		viewer.ActorKey = "user:" + domain.ID(account.ID)
		viewer.SessionHash = auth.TokenHash(token)
	} else if visitorToken != "" {
		actorKey, err := s.visitors.Verify(visitorToken)
		if err != nil {
			return ctx, err
		}
		viewer.ActorKey = actorKey
	}
	return auth.WithViewer(ctx, viewer), nil
}

func (s *Server) clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	peer := net.ParseIP(host)
	if peer != nil && s.config.TrustedProxy != nil && s.config.TrustedProxy.Contains(peer) {
		// Our reverse proxy overwrites X-Forwarded-For with the actual client.
		// Prefer the last hop if the request contains an old forwarded chain.
		forwarded := r.Header.Get("X-Forwarded-For")
		if forwarded != "" {
			addresses := strings.Split(forwarded, ",")
			last := strings.TrimSpace(addresses[len(addresses)-1])
			if parsed := net.ParseIP(last); parsed != nil {
				return parsed.String()
			}
		}
	}
	return host
}
