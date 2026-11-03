package graphqlhttp

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/Alexander-D-Karpov/commeng/internal/domain"
)

// Checks that the public GraphQL cursor contract is consistent in both directions.
func TestGraphQLBackwardPaginationAndJump(t *testing.T) {
	api := testHandler(t)
	ctx := context.Background()
	owner, err := api.service.Store.CreateUser(ctx, domain.User{
		Name: "Owner", Email: "pagination@example.org", Role: domain.RoleUser,
	})
	if err != nil {
		t.Fatal(err)
	}
	post, err := api.service.Store.CreatePost(ctx, owner.ID, "Paging", "Body")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 13; i++ {
		_, err := api.service.Store.CreateComment(ctx, domain.CommentCreate{
			PostID: post.ID, Name: "Reader", Email: "reader@example.org",
			Body: fmt.Sprintf("Reply %d", i),
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, order := range []string{"NEWEST", "OLDEST"} {
		t.Run(order, func(t *testing.T) {
			read := func(cursorKind, cursor string) map[string]any {
				t.Helper()
				arg := ""
				if cursorKind != "" {
					arg = fmt.Sprintf(`,%s:%q`, cursorKind, cursor)
				}
				query := fmt.Sprintf(
					`query {post(id:%q){comments(first:4,sort:%s%s){edges{node{id}}
					pageInfo{startCursor endCursor hasNextPage hasPreviousPage}}}}`,
					domain.ID(post.ID),
					order,
					arg,
				)
				result := callGraphQL(t, api.Handler(), query, "", "")
				if len(result.Errors) != 0 {
					t.Fatalf("query: %+v", result.Errors)
				}
				var postResult map[string]any
				if err := json.Unmarshal(result.Data["post"], &postResult); err != nil {
					t.Fatal(err)
				}
				return postResult["comments"].(map[string]any)
			}
			first := read("", "")
			start := first["pageInfo"].(map[string]any)
			if start["hasPreviousPage"] != false || start["hasNextPage"] != true {
				t.Fatalf("first-page flags: %+v", start)
			}
			second := read("after", start["endCursor"].(string))
			secondInfo := second["pageInfo"].(map[string]any)
			if secondInfo["hasPreviousPage"] != true {
				t.Fatalf("second page cannot go back: %+v", secondInfo)
			}
			previous := read("before", secondInfo["startCursor"].(string))
			left := first["edges"].([]any)
			right := previous["edges"].([]any)
			if len(left) != 4 || len(right) != 4 {
				t.Fatalf("page size: %d vs %d", len(left), len(right))
			}
			for i := range left {
				a := left[i].(map[string]any)["node"].(map[string]any)["id"]
				b := right[i].(map[string]any)["node"].(map[string]any)["id"]
				if a != b {
					t.Fatalf("index %d: first ID %v, before ID %v", i, a, b)
				}
			}
		})
	}
	jump := callGraphQL(
		t,
		api.Handler(),
		fmt.Sprintf(`{commentJump(postId:%q,commentId:%q,pageSize:4){indexInPage target{id} page{edges{node{id}}}}}`,
			domain.ID(post.ID), domain.ID(7)),
		"",
		"",
	)
	if len(jump.Errors) != 0 {
		t.Fatalf("jump failed: %+v", jump.Errors)
	}
	location := expectObject(t, jump, "commentJump")
	edges := location["page"].(map[string]any)["edges"].([]any)
	position := int(location["indexInPage"].(float64))
	if edges[position].(map[string]any)["node"].(map[string]any)["id"] != "7" {
		t.Fatalf("jump returned wrong page: %+v", location)
	}
}
