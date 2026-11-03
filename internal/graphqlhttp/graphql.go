package graphqlhttp

import (
	"context"
	"errors"
	"log/slog"
	"net/url"
	"strings"
	"time"

	"github.com/99designs/gqlgen/graphql"
	"github.com/99designs/gqlgen/graphql/handler"
	"github.com/99designs/gqlgen/graphql/handler/extension"
	"github.com/99designs/gqlgen/graphql/handler/transport"
	coderws "github.com/coder/websocket"
	"github.com/vektah/gqlparser/v2/gqlerror"

	"github.com/Alexander-D-Karpov/commeng/internal/auth"
	"github.com/Alexander-D-Karpov/commeng/internal/domain"
	"github.com/Alexander-D-Karpov/commeng/internal/graph/generated"
	"github.com/Alexander-D-Karpov/commeng/internal/graph/model"
	"github.com/Alexander-D-Karpov/commeng/internal/graph/resolver"
	"github.com/Alexander-D-Karpov/commeng/internal/observability"
	"github.com/Alexander-D-Karpov/commeng/internal/pagination"
)

const maxGraphQLComplexity = 2000

func newGraphQLHandler(server *Server, cursors *pagination.Signer) *handler.Server {
	root := &resolver.Resolver{Service: server.service, Cursors: cursors}
	config := generated.Config{Resolvers: root}
	configureComplexity(&config)
	graphQL := handler.New(generated.NewExecutableSchema(config))
	payloadLimit := int64(1 << 20)
	graphQL.AddTransport(transport.Websocket{
		KeepAlivePingInterval: 15 * time.Second,
		InitTimeout:           10 * time.Second,
		PayloadReadLimit:      &payloadLimit,
		InitFunc: func(ctx context.Context, payload transport.InitPayload) (context.Context, *transport.InitPayload, error) {
			// Browser WebSocket API cannot set arbitrary HTTP headers.
			// The connection_init payload is authenticated before executing operations.
			authorization, visitorToken := payload.Authorization(), payload.GetString("X-Visitor-Token")
			if authorization == "" && visitorToken == "" {
				return ctx, &payload, nil
			}
			next, err := server.identifyTokens(ctx, authorization, visitorToken)
			if err != nil {
				return ctx, nil, err
			}
			return next, &payload, nil
		},
		Implementation: transport.CoderWebsocketImplementation{
			AcceptOptions: coderws.AcceptOptions{OriginPatterns: originHosts(server.origins)},
		},
	})
	graphQL.AddTransport(transport.Options{})
	graphQL.AddTransport(transport.POST{})
	graphQL.Use(extension.Introspection{})
	graphQL.SetParserTokenLimit(10000)
	graphQL.AroundOperations(func(ctx context.Context, next graphql.OperationHandler) graphql.ResponseHandler {
		operation := graphql.GetOperationContext(ctx)
		kind := "other"
		if operation != nil && operation.Operation != nil {
			kind = string(operation.Operation.Operation)
		}
		nextResponse := next(ctx)
		return func(responseCtx context.Context) *graphql.Response {
			start := time.Now()
			traced, finish := observability.TraceOperation(responseCtx, kind)
			result := nextResponse(traced)
			finish()
			if server.metrics != nil {
				server.metrics.ObserveGraphQL(kind, start)
			}
			return result
		}
	})
	graphQL.AroundOperations(limitGraphQLDepth)
	graphQL.AroundOperations(limitGraphQLAliases)
	graphQL.AroundOperations(server.limitWebSocketOperations)
	graphQL.Use(expandedSelectionLimit{})
	graphQL.Use(extension.FixedComplexityLimit(maxGraphQLComplexity))
	graphQL.SetErrorPresenter(presentError)
	// Response middleware is called for EVERY subscription event, not just once
	// for the WebSocket connection. Each event gets a fresh, bounded DataLoader.
	graphQL.AroundResponses(func(ctx context.Context, next graphql.ResponseHandler) *graphql.Response {
		viewer := auth.FromContext(ctx)
		return next(resolver.WithLoaders(ctx, server.service, viewer.ActorKey))
	})
	graphQL.AroundRootFields(func(ctx context.Context, next graphql.RootResolver) graphql.Marshaler {
		return server.limitRootField(ctx, next)
	})
	return graphQL
}

func configureComplexity(config *generated.Config) {
	listCost := func(child int, first *int) int {
		count := 20
		if first != nil {
			count = *first
		}
		if count > 100 {
			count = 100
		}
		if count < 1 {
			count = 1
		}
		return 1 + count*child
	}
	config.Complexity.Query.Posts = func(child int, first *int, _ *string) int { return listCost(child, first) }
	config.Complexity.Query.SearchPosts = func(child int, _ string, first *int, _ *string) int {
		return listCost(child, first)
	}
	config.Complexity.Query.SearchComments = func(child int, _ string, _ *string, first *int, _ *string) int {
		return listCost(child, first)
	}
	config.Complexity.Query.CommentEvents = func(child int, _ string, first *int, _ *string) int {
		return listCost(child, first)
	}
	config.Complexity.Query.Notifications = func(child int, first *int, _ *string, _ *bool) int {
		return listCost(child, first)
	}
	config.Complexity.Query.CommentJump = func(child int, _, _ string, pageSize *int, _ *model.CommentSort, _ *int) int {
		return listCost(child, pageSize)
	}
	config.Complexity.Post.Comments = func(child int, first *int, _, _ *string, _ *model.CommentSort) int {
		return listCost(child, first)
	}
	config.Complexity.Comment.Replies = func(child int, first *int, _, _ *string, _ *model.CommentSort) int {
		return listCost(child, first)
	}
	config.Complexity.Mutation.Login = func(child int, _, _ string) int { return child + 500 }
	config.Complexity.Mutation.Logout = func(child int) int { return child + 100 }
	config.Complexity.Query.CommentAncestors = func(child int, _ string, _ *string, first *int) int {
		return listCost(child, first)
	}
	config.Complexity.Mutation.Register = func(child int, _, _, _ string) int { return child + 500 }
	config.Complexity.Mutation.CreateComment = func(child int, _ model.CreateCommentInput) int { return child + 200 }
	config.Complexity.Mutation.CreatePost = func(child int, _ model.CreatePostInput) int { return child + 200 }
	config.Complexity.Mutation.ModerateComment = func(child int, _ model.ModerateCommentInput) int { return child + 200 }
	config.Complexity.Mutation.LikeComment = func(child int, _ string) int { return child + 100 }
	config.Complexity.Mutation.UnlikeComment = func(child int, _ string) int { return child + 100 }
	config.Complexity.Mutation.SetPostCommentsEnabled = func(child int, _ string, _ bool) int { return child + 200 }
	config.Complexity.Mutation.EditPost = func(child int, _ model.EditPostInput) int { return child + 200 }
	config.Complexity.Mutation.DeletePost = func(child int, _ string) int { return child + 200 }
	config.Complexity.Mutation.EditComment = func(child int, _, _ string) int { return child + 200 }
	config.Complexity.Mutation.DeleteComment = func(child int, _ string) int { return child + 200 }
	config.Complexity.Mutation.MarkNotificationsRead = func(child int, ids []string) int {
		return child + 50 + 10*len(ids)
	}
}

func originHosts(origins map[string]struct{}) []string {
	hosts := make([]string, 0, len(origins))
	for origin := range origins {
		parsed, err := url.Parse(origin)
		if err == nil && parsed.Host != "" {
			hosts = append(hosts, parsed.Host)
		}
	}
	return hosts
}

func presentError(ctx context.Context, err error) *gqlerror.Error {
	result := graphql.DefaultErrorPresenter(ctx, err)
	var domainError *domain.Error
	if errors.As(err, &domainError) {
		result.Message = domainError.Message
		result.Extensions = map[string]any{"code": string(domainError.Code)}
		return result
	}
	var gqlErr *gqlerror.Error
	if errors.As(err, &gqlErr) {
		return result
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		result.Message = "request cancelled"
		result.Extensions = map[string]any{"code": "CANCELLED"}
		return result
	}
	if !strings.Contains(err.Error(), "client disconnected") {
		slog.ErrorContext(ctx, "graphql error", "error", err)
	}
	result.Message = "internal server error"
	result.Extensions = map[string]any{"code": "INTERNAL"}
	return result
}
