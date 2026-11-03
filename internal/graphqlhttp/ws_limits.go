package graphqlhttp

import (
	"context"
	"sync"
	"time"

	"github.com/99designs/gqlgen/graphql"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/gqlerror"

	"github.com/Alexander-D-Karpov/commeng/internal/auth"
	"github.com/Alexander-D-Karpov/commeng/internal/domain"
)

// These are per-process safeguards, not a distributed anti-abuse system.
const (
	maxWebSocketsPerIP           = 20
	maxWebSocketOperationsMinute = 120
	maxWebSocketOperations       = 16
	maxWebSocketSubscriptions    = 8
)

type websocketLimitKey struct{}

type websocketBudget struct {
	mu            sync.Mutex
	windowStart   time.Time
	operations    int
	active        int
	subscriptions int
}

// acquire is safe across concurrently running GraphQL operations in one socket.
// The release callback is idempotent because both context cancellation and the
// final GraphQL response can release the same reservation.
func (b *websocketBudget) acquire(subscription bool, now time.Time) (func(), bool) {
	b.mu.Lock()
	if b.windowStart.IsZero() || now.Sub(b.windowStart) >= time.Minute {
		b.windowStart = now
		b.operations = 0
	}
	if b.operations >= maxWebSocketOperationsMinute || b.active >= maxWebSocketOperations ||
		(subscription && b.subscriptions >= maxWebSocketSubscriptions) {
		b.mu.Unlock()
		return nil, false
	}
	b.operations++
	b.active++
	if subscription {
		b.subscriptions++
	}
	b.mu.Unlock()

	var once sync.Once
	return func() {
		once.Do(func() {
			b.mu.Lock()
			b.active--
			if subscription {
				b.subscriptions--
			}
			b.mu.Unlock()
		})
	}, true
}

// limitWebSocketOperations is executed for EVERY GraphQL operation over an
// established WebSocket; a rate limit at the HTTP handshake is insufficient.
func (s *Server) limitWebSocketOperations(ctx context.Context, next graphql.OperationHandler) graphql.ResponseHandler {
	budget, ok := ctx.Value(websocketLimitKey{}).(*websocketBudget)
	if !ok {
		return next(ctx)
	}
	op := graphql.GetOperationContext(ctx)
	isSubscription := op != nil && op.Operation != nil && op.Operation.Operation == ast.Subscription
	release, accepted := budget.acquire(isSubscription, time.Now())
	if !accepted {
		return graphql.OneShot(graphql.ErrorResponse(ctx, "websocket operation limit exceeded"))
	}
	viewer := auth.FromContext(ctx)
	if viewer.SessionHash != "" {
		user, err := s.service.Store.SessionUser(ctx, viewer.SessionHash)
		if err != nil {
			release()
			if domain.HasCode(err, domain.ErrAuth) || domain.HasCode(err, domain.ErrNotFound) {
				err = domain.Fault(domain.ErrAuth, "session expired or revoked")
			}
			return graphql.OneShot(&graphql.Response{Errors: gqlerror.List{presentError(ctx, err)}})
		}
		viewer.User = &user
		viewer.ActorKey = "user:" + domain.ID(user.ID)
		ctx = auth.WithViewer(ctx, viewer)
	}
	stop := context.AfterFunc(ctx, release)
	response := next(ctx)
	return func(responseCtx context.Context) *graphql.Response {
		result := response(responseCtx)
		if result == nil || (result.HasNext != nil && !*result.HasNext) ||
			(!isSubscription && result.HasNext == nil) {
			stop()
			release()
		}
		return result
	}
}

type websocketConnections struct {
	mu     sync.Mutex
	counts map[string]int
}

func newWebsocketConnections() *websocketConnections {
	return &websocketConnections{counts: make(map[string]int)}
}

func (c *websocketConnections) acquire(ip string) (func(), bool) {
	c.mu.Lock()
	if c.counts[ip] >= maxWebSocketsPerIP {
		c.mu.Unlock()
		return nil, false
	}
	c.counts[ip]++
	c.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			c.mu.Lock()
			c.counts[ip]--
			if c.counts[ip] == 0 {
				delete(c.counts, ip)
			}
			c.mu.Unlock()
		})
	}, true
}
