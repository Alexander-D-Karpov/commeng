package graphqlhttp

import (
	"context"
	"strings"

	"github.com/99designs/gqlgen/graphql"
	"github.com/vektah/gqlparser/v2/ast"
)

// A deep chain with first:1 is cheap by complexity but expensive in sequential
// database roundtrips. Depth is an independent, enforced resource limit.
const maxGraphQLDepth = 20

func limitGraphQLDepth(ctx context.Context, next graphql.OperationHandler) graphql.ResponseHandler {
	operation := graphql.GetOperationContext(ctx)
	if operation == nil || operation.Operation == nil {
		return next(ctx)
	}
	depth := selectionDepth(operation.Operation.SelectionSet, make(map[*ast.FragmentDefinition]int))
	if depth > maxGraphQLDepth {
		return graphql.OneShot(graphql.ErrorResponse(
			ctx, "query depth %d exceeds the limit of %d", depth, maxGraphQLDepth,
		))
	}
	return next(ctx)
}

// selectionDepth memoizes fragment references and marks them before descent
// to avoid exponential revisits and infinite loops in malformed documents.
func selectionDepth(set ast.SelectionSet, memo map[*ast.FragmentDefinition]int) int {
	deepest := 0
	for _, selection := range set {
		depth := 0
		switch node := selection.(type) {
		case *ast.Field:
			// Introspection fields are not charged a level themselves, but
			// their descendants must still be traversed. Skipping the whole
			// __schema subtree would bypass the depth limit entirely.
			depth = selectionDepth(node.SelectionSet, memo)
			if !strings.HasPrefix(node.Name, "__") {
				depth++
			}
		case *ast.InlineFragment:
			depth = selectionDepth(node.SelectionSet, memo)
		case *ast.FragmentSpread:
			if node.Definition == nil {
				continue
			}
			if prior, ok := memo[node.Definition]; ok {
				depth = prior
				break
			}
			memo[node.Definition] = 0
			depth = selectionDepth(node.Definition.SelectionSet, memo)
			memo[node.Definition] = depth
		}
		if depth > deepest {
			deepest = depth
		}
	}
	return deepest
}
