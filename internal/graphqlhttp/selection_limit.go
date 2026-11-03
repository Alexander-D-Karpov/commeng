package graphqlhttp

import (
	"context"

	"github.com/99designs/gqlgen/graphql"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/gqlerror"
)

// Bound fragment expansion before gqlgen's complexity calculation, which
// otherwise recursively traverses repeated fragment spreads.
const maxExpandedSelections = 10000

type expandedSelectionLimit struct{}

func (expandedSelectionLimit) ExtensionName() string                   { return "ExpandedSelectionLimit" }
func (expandedSelectionLimit) Validate(graphql.ExecutableSchema) error { return nil }

func (expandedSelectionLimit) MutateOperationContext(_ context.Context, op *graphql.OperationContext) *gqlerror.Error {
	if op == nil || op.Operation == nil {
		return nil
	}
	counter := selectionCounter{
		active: make(map[*ast.FragmentDefinition]bool),
		memo:   make(map[*ast.FragmentDefinition]int),
	}
	if counter.count(op.Operation.SelectionSet) > maxExpandedSelections {
		return gqlerror.Errorf("too many expanded GraphQL selections (maximum %d)", maxExpandedSelections)
	}
	return nil
}

type selectionCounter struct {
	active map[*ast.FragmentDefinition]bool
	memo   map[*ast.FragmentDefinition]int
}

func (c *selectionCounter) count(set ast.SelectionSet) int {
	total := 0
	for _, selection := range set {
		total++
		switch node := selection.(type) {
		case *ast.Field:
			total += c.count(node.SelectionSet)
		case *ast.InlineFragment:
			total += c.count(node.SelectionSet)
		case *ast.FragmentSpread:
			total += c.fragment(node.Definition)
		}
		if total > maxExpandedSelections {
			return maxExpandedSelections + 1
		}
	}
	return total
}

func (c *selectionCounter) fragment(fragment *ast.FragmentDefinition) int {
	if fragment == nil || c.active[fragment] {
		return 0
	}
	if count, ok := c.memo[fragment]; ok {
		return count
	}
	c.active[fragment] = true
	count := c.count(fragment.SelectionSet)
	delete(c.active, fragment)
	c.memo[fragment] = count
	return count
}
