package graphqlhttp

import (
	"strings"
	"testing"

	"github.com/vektah/gqlparser/v2/ast"
)

func TestAliasCountIncludesRepeatedFragmentExpansions(t *testing.T) {
	fragment := &ast.FragmentDefinition{SelectionSet: ast.SelectionSet{&ast.Field{Name: "posts", Alias: "many"}}}
	set := ast.SelectionSet{}
	for range maxGraphQLAliases + 1 {
		set = append(set, &ast.FragmentSpread{Definition: fragment})
	}
	count := 0
	if countAliases(set, make(map[*ast.FragmentDefinition]bool), &count) {
		t.Fatal("repeated fragment work bypassed alias cap")
	}
	if count != maxGraphQLAliases+1 {
		t.Fatalf("alias count = %d", count)
	}
}

func TestAliasCountMemoizesFragmentsWithoutAliases(t *testing.T) {
	fragment := &ast.FragmentDefinition{SelectionSet: ast.SelectionSet{&ast.Field{Name: "id"}}}
	for range 40 {
		fragment = &ast.FragmentDefinition{SelectionSet: ast.SelectionSet{
			&ast.FragmentSpread{Definition: fragment}, &ast.FragmentSpread{Definition: fragment},
		}}
	}
	count := 0
	if !countAliases(
		ast.SelectionSet{&ast.FragmentSpread{Definition: fragment}},
		make(map[*ast.FragmentDefinition]bool),
		&count,
	) ||
		count != 0 {
		t.Fatalf("unexpected alias count: %d", count)
	}
}

func TestGraphQLRejectsExcessiveAliases(t *testing.T) {
	server := testHandler(t)
	queries := make([]string, maxGraphQLAliases+1)
	for i := range queries {
		queries[i] = "a" + strings.Repeat("x", i+1) + ": posts(first:1) { pageInfo { hasNextPage } }"
	}
	result := callGraphQL(t, server.Handler(), "query {"+strings.Join(queries, " ")+"}", "", "")
	if len(result.Errors) == 0 || !strings.Contains(result.Errors[0].Message, "too many GraphQL field aliases") {
		t.Fatalf("alias cap not applied: %+v", result.Errors)
	}
}
