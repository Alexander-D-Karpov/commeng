package graphqlhttp

import (
	"strings"
	"testing"

	"github.com/vektah/gqlparser/v2/ast"
)

func TestSelectionDepth(t *testing.T) {
	selection := ast.SelectionSet{
		&ast.Field{Name: "id"},
	}

	for i := 0; i < 25; i++ {
		selection = ast.SelectionSet{
			&ast.Field{
				Name:         "replies",
				SelectionSet: selection,
			},
		}
	}

	if actual := selectionDepth(selection, make(map[*ast.FragmentDefinition]int)); actual <= maxGraphQLDepth {
		t.Fatalf("depth = %d, expected above %d", actual, maxGraphQLDepth)
	}

	if actual := selectionDepth(
		ast.SelectionSet{&ast.Field{Name: "__typename"}},
		make(map[*ast.FragmentDefinition]int),
	); actual != 0 {
		t.Fatalf("introspection depth = %d, want 0", actual)
	}
}

func TestHTTPRejectsExcessiveDepth(t *testing.T) {
	query := `{ post(id:"1") { comments(first:1) { edges { node { ` +
		strings.Repeat(`replies(first:1) { edges { node { `, 8) +
		`id` +
		strings.Repeat(` } } }`, 8) +
		` } } } } }`

	result := callGraphQL(t, testHandler(t).Handler(), query, "", "")

	if len(result.Errors) == 0 ||
		!strings.Contains(strings.ToLower(result.Errors[0].Message), "depth") {
		t.Fatalf("deep query not rejected: %+v", result.Errors)
	}
}

func TestIntrospectionCannotBypassNestedDepthLimit(t *testing.T) {
	fields := ast.SelectionSet{&ast.Field{Name: "name"}}
	for i := 0; i < maxGraphQLDepth+1; i++ {
		fields = ast.SelectionSet{&ast.Field{Name: "ofType", SelectionSet: fields}}
	}
	root := ast.SelectionSet{&ast.Field{Name: "__schema", SelectionSet: fields}}
	if depth := selectionDepth(root, make(map[*ast.FragmentDefinition]int)); depth <= maxGraphQLDepth {
		t.Fatalf("introspection subtree bypassed depth limit: got %d", depth)
	}
}
