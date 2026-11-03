package graphqlhttp

import (
	"context"

	"github.com/99designs/gqlgen/graphql"
	"github.com/vektah/gqlparser/v2/ast"
)

const maxGraphQLAliases = 80

func limitGraphQLAliases(ctx context.Context, next graphql.OperationHandler) graphql.ResponseHandler {
	op := graphql.GetOperationContext(ctx)
	if op == nil || op.Operation == nil {
		return next(ctx)
	}
	count := 0
	if !countAliases(op.Operation.SelectionSet, make(map[*ast.FragmentDefinition]bool), &count) {
		return graphql.OneShot(
			graphql.ErrorResponse(ctx, "too many GraphQL field aliases (maximum %d)", maxGraphQLAliases),
		)
	}
	return next(ctx)
}

// Count each fragment occurrence, not just each unique definition. The active
// set protects against cycles in malformed input without undercharging repeated
// fragments, which can multiply the work of a single operation.
func countAliases(set ast.SelectionSet, active map[*ast.FragmentDefinition]bool, count *int) bool {
	return countAliasesMemo(set, active, make(map[*ast.FragmentDefinition]int), count)
}

func countAliasesMemo(
	set ast.SelectionSet,
	active map[*ast.FragmentDefinition]bool,
	memo map[*ast.FragmentDefinition]int,
	count *int,
) bool {
	for _, selection := range set {
		switch node := selection.(type) {
		case *ast.Field:
			if node.Alias != "" && node.Alias != node.Name {
				(*count)++
				if *count > maxGraphQLAliases {
					return false
				}
			}
			if !countAliasesMemo(node.SelectionSet, active, memo, count) {
				return false
			}
		case *ast.InlineFragment:
			if !countAliasesMemo(node.SelectionSet, active, memo, count) {
				return false
			}
		case *ast.FragmentSpread:
			if node.Definition != nil && !active[node.Definition] {
				if cached, ok := memo[node.Definition]; ok {
					*count += cached
					if *count > maxGraphQLAliases {
						return false
					}
					continue
				}
				before := *count
				active[node.Definition] = true
				ok := countAliasesMemo(node.Definition.SelectionSet, active, memo, count)
				delete(active, node.Definition)
				if !ok {
					return false
				}
				memo[node.Definition] = *count - before
			}
		}
	}
	return true
}
