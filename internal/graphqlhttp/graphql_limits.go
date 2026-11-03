package graphqlhttp

import (
	"context"
	"strings"

	"github.com/99designs/gqlgen/graphql"

	"github.com/Alexander-D-Karpov/commeng/internal/auth"
	"github.com/Alexander-D-Karpov/commeng/internal/domain"
)

type remoteIPKey struct{}

// limitRootField counts aliases individually rather than trusting a per-HTTP
// request limit. Password verification is particularly expensive.
func (s *Server) limitRootField(ctx context.Context, next graphql.RootResolver) graphql.Marshaler {
	root := graphql.GetRootFieldContext(ctx)
	if root == nil {
		return next(ctx)
	}
	// gqlgen stores the root field name in RootFieldContext.Object, not the
	// GraphQL operation type. Checking Object == "Mutation" silently disables
	// this limiter. Match the public mutations explicitly instead.
	name := root.Field.Name
	if !isMutationField(name) {
		return next(ctx)
	}
	actor := auth.FromContext(ctx).ActorKey
	ip, _ := ctx.Value(remoteIPKey{}).(string)
	if actor == "" {
		actor = "ip:" + ip
	}
	key, max := "write:"+actor, 60
	switch name {
	case "login", "register":
		key, max = "auth:"+ip, 10
		if op := graphql.GetOperationContext(ctx); op != nil {
			args := root.Field.ArgumentMap(op.Variables)
			if email, ok := args["email"].(string); ok {
				key += ":" + strings.ToLower(strings.TrimSpace(email))
			}
		}
		// Also constrain all login/register attempts from the same source.
		if !s.limiter.allow("auth-ip:"+ip, 30) {
			graphql.AddError(ctx, domain.Fault(domain.ErrResource, "too many authentication attempts"))
			return graphql.Null
		}
	case "likeComment", "unlikeComment":
		max = 120
		if !s.limiter.allow("like-ip:"+ip, 300) {
			graphql.AddError(ctx, domain.Fault(domain.ErrResource, "like IP rate limit exceeded"))
			return graphql.Null
		}
	}
	if !s.limiter.allow(key, max) {
		graphql.AddError(ctx, domain.Fault(domain.ErrResource, "mutation rate limit exceeded"))
		return graphql.Null
	}
	return next(ctx)
}

func isMutationField(name string) bool {
	switch name {
	case "register", "login", "logout", "createPost", "createComment",
		"setPostCommentsEnabled", "likeComment", "unlikeComment", "moderateComment",
		"editPost", "deletePost", "editComment", "deleteComment", "markNotificationsRead":
		return true
	default:
		return false
	}
}
