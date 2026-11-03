package graphqlhttp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Alexander-D-Karpov/commeng/internal/auth"
	"github.com/Alexander-D-Karpov/commeng/internal/domain"
)

func discussionFixture(t *testing.T) (*Server, string, domain.Post, []domain.Comment) {
	t.Helper()
	server := testHandler(t)
	ctx := context.Background()
	owner, err := server.service.Store.CreateUser(ctx, domain.User{
		Name: "Owner", Email: "discussion@example.org", PasswordHash: "hash", Role: domain.RoleUser,
	})
	if err != nil {
		t.Fatal(err)
	}
	const token = "test-discussion-session"
	expires := time.Now().Add(time.Hour)
	if err := server.service.Store.SaveSession(ctx, auth.TokenHash(token), owner.ID, expires); err != nil {
		t.Fatal(err)
	}
	post, err := server.service.Store.CreatePost(ctx, owner.ID, "Discussion", "Body")
	if err != nil {
		t.Fatal(err)
	}
	comments := make([]domain.Comment, 0, 4)
	for i := 0; i < 4; i++ {
		input := domain.CommentCreate{
			PostID: post.ID, Name: "Reader", Email: "reader@example.org",
			AvatarHash: strings.Repeat("0", 64), Body: "Comment body",
		}
		if i > 0 {
			input.ParentID = &comments[i-1].ID
		}
		comment, err := server.service.Store.CreateComment(ctx, input)
		if err != nil {
			t.Fatal(err)
		}
		comments = append(comments, comment)
	}
	return server, token, post, comments
}

func TestGraphQLVotingModerationAndAncestors(t *testing.T) {
	server, token, _, comments := discussionFixture(t)
	handler := server.Handler()
	viewer := expectObject(t, callGraphQL(t, handler, `{viewer{id name}}`, token, ""), "viewer")
	if viewer["name"] != "Owner" {
		t.Fatalf("viewer: %+v", viewer)
	}
	checkGraphQLVoting(t, handler, token, comments[0].ID)
	checkGraphQLAncestors(t, handler, comments)
	checkGraphQLModeration(t, handler, token, comments[0].ID)
}

func checkGraphQLVoting(t *testing.T, handler http.Handler, token string, id int64) {
	t.Helper()
	query := fmt.Sprintf(`mutation {likeComment(id:%q){id likesCount viewerHasLiked}}`, domain.ID(id))
	if response := callGraphQL(t, handler, query, "", ""); len(response.Errors) == 0 {
		t.Fatal("anonymous like accepted")
	}
	for i := 0; i < 2; i++ {
		liked := expectObject(t, callGraphQL(t, handler, query, token, ""), "likeComment")
		if liked["likesCount"] != float64(1) || liked["viewerHasLiked"] != true {
			t.Fatalf("like: %+v", liked)
		}
	}
	commentQuery := fmt.Sprintf(`{comment(id:%q){id likesCount viewerHasLiked}}`, domain.ID(id))
	comment := expectObject(t, callGraphQL(t, handler, commentQuery, token, ""), "comment")
	if comment["viewerHasLiked"] != true {
		t.Fatalf("viewer like missing: %+v", comment)
	}
	unlike := fmt.Sprintf(`mutation {unlikeComment(id:%q){likesCount viewerHasLiked}}`, domain.ID(id))
	unliked := expectObject(t, callGraphQL(t, handler, unlike, token, ""), "unlikeComment")
	if unliked["likesCount"] != float64(0) || unliked["viewerHasLiked"] != false {
		t.Fatalf("unlike: %+v", unliked)
	}
}

func checkGraphQLAncestors(t *testing.T, handler http.Handler, comments []domain.Comment) {
	t.Helper()
	query := fmt.Sprintf(`{commentAncestors(commentId:%q,first:2){nodes{id} nextBefore}}`, domain.ID(comments[3].ID))
	first := expectObject(t, callGraphQL(t, handler, query, "", ""), "commentAncestors")
	nodes := first["nodes"].([]any)
	if len(nodes) != 2 || nodes[0].(map[string]any)["id"] != domain.ID(comments[2].ID) ||
		first["nextBefore"] != domain.ID(comments[1].ID) {
		t.Fatalf("first ancestor page: %+v", first)
	}
	query = fmt.Sprintf(`{commentAncestors(commentId:%q,first:2,before:%q){nodes{id} nextBefore}}`,
		domain.ID(comments[3].ID), first["nextBefore"])
	last := expectObject(t, callGraphQL(t, handler, query, "", ""), "commentAncestors")
	nodes = last["nodes"].([]any)
	if len(nodes) != 1 || nodes[0].(map[string]any)["id"] != domain.ID(comments[0].ID) || last["nextBefore"] != nil {
		t.Fatalf("last ancestor page: %+v", last)
	}
	query = fmt.Sprintf(`{commentAncestors(commentId:%q,before:%q){nodes{id}}}`,
		domain.ID(comments[3].ID), domain.ID(comments[3].ID))
	if response := callGraphQL(t, handler, query, "", ""); len(response.Errors) == 0 {
		t.Fatal("non-ancestor cursor accepted")
	}
}

func checkGraphQLModeration(t *testing.T, handler http.Handler, token string, id int64) {
	t.Helper()
	query := fmt.Sprintf(`mutation {moderateComment(input:{commentId:%q,action:HIDE}){state body}}`, domain.ID(id))
	if response := callGraphQL(t, handler, query, "", ""); len(response.Errors) == 0 {
		t.Fatal("anonymous moderation accepted")
	}
	hidden := expectObject(t, callGraphQL(t, handler, query, token, ""), "moderateComment")
	if hidden["state"] != "HIDDEN" || hidden["body"] != nil {
		t.Fatalf("hidden comment: %+v", hidden)
	}
	query = fmt.Sprintf(`mutation {moderateComment(input:{commentId:%q,action:RESTORE}){state body}}`, domain.ID(id))
	restored := expectObject(t, callGraphQL(t, handler, query, token, ""), "moderateComment")
	if restored["state"] != "VISIBLE" || restored["body"] != "Comment body" {
		t.Fatalf("restored comment: %+v", restored)
	}
}

func TestPermalinkRedactionAndNotFound(t *testing.T) {
	server, _, post, comments := discussionFixture(t)
	handler := server.Handler()
	path := fmt.Sprintf("/p/%d/c/%d", post.ID, comments[0].ID)
	readTarget := func() map[string]any {
		t.Helper()
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != http.StatusOK {
			t.Fatalf("permalink status %d: %s", response.Code, response.Body.String())
		}
		var body map[string]any
		if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		return body["target"].(map[string]any)
	}
	visible := readTarget()
	if visible["body"] != "Comment body" || visible["name"] != "Reader" {
		t.Fatalf("visible target: %+v", visible)
	}
	_, err := server.service.Store.Moderate(context.Background(), domain.Moderation{
		CommentID: comments[0].ID, ActorID: post.AuthorID, Kind: domain.HideComment,
	})
	if err != nil {
		t.Fatal(err)
	}
	hidden := readTarget()
	if hidden["body"] != nil || hidden["name"] != "Hidden comment" || hidden["state"] != "HIDDEN" {
		t.Fatalf("hidden target leaked data: %+v", hidden)
	}
	for _, invalid := range []string{
		"/p/invalid/c/1", "/p/1/c/invalid", "/p/999/c/1", "/p/1/c/999",
	} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, invalid, nil))
		if response.Code != http.StatusNotFound {
			t.Fatalf("%s: status %d, want 404", invalid, response.Code)
		}
	}
}
