package graphqlhttp

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"

	"github.com/Alexander-D-Karpov/commeng/internal/domain"
	"github.com/Alexander-D-Karpov/commeng/internal/live"
	"github.com/Alexander-D-Karpov/commeng/internal/repository/memory"
	"github.com/Alexander-D-Karpov/commeng/internal/service"
)

// countingStore checks batch *calls*, not incidental SQL/Go implementation
// details. The same resolver is used with both repository implementations.
type countingStore struct {
	*memory.Store
	batchCalls   atomic.Int64
	userCalls    atomic.Int64
	likeCalls    atomic.Int64
	largestBatch atomic.Int64
	largestLike  atomic.Int64
}

// recordLargest is safe even when gqlgen dispatches several batches at once.
func recordLargest(target *atomic.Int64, size int) {
	for {
		previous := target.Load()
		if int64(size) <= previous || target.CompareAndSwap(previous, int64(size)) {
			return
		}
	}
}

func (s *countingStore) CommentsBatch(
	ctx context.Context,
	reqs []domain.CommentPageRequest,
) ([]domain.Page[domain.Comment], error) {
	s.batchCalls.Add(1)
	recordLargest(&s.largestBatch, len(reqs))
	return s.Store.CommentsBatch(ctx, reqs)
}
func (s *countingStore) Users(ctx context.Context, ids []int64) (map[int64]domain.User, error) {
	s.userCalls.Add(1)
	return s.Store.Users(ctx, ids)
}
func (s *countingStore) HasLikes(ctx context.Context, actor string, ids []int64) (map[int64]bool, error) {
	s.likeCalls.Add(1)
	recordLargest(&s.largestLike, len(ids))
	return s.Store.HasLikes(ctx, actor, ids)
}

func TestGraphQLNestedFieldsUseBatchLoaders(t *testing.T) {
	ctx := context.Background()
	store := &countingStore{Store: memory.New()}
	user, err := store.CreateUser(ctx, domain.User{Name: "Author", Email: "author@example.org", Role: domain.RoleUser})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	post, err := store.CreatePost(ctx, user.ID, "Post", "Text")
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	for i := 0; i < 50; i++ {
		parent, err := store.CreateComment(ctx, domain.CommentCreate{
			PostID: post.ID, Name: "Reader", Email: "reader@example.org", AvatarHash: fmt.Sprintf("%064x", i+1), Body: "Root",
		})
		if err != nil {
			t.Fatalf("root %d: %v", i, err)
		}
		for j := 0; j < 3; j++ {
			if _, err := store.CreateComment(ctx, domain.CommentCreate{
				PostID: post.ID, ParentID: &parent.ID, Name: "Reader", Email: "reader@example.org",
				AvatarHash: fmt.Sprintf("%064x", j+1), Body: "Reply",
			}); err != nil {
				t.Fatalf("reply %d/%d: %v", i, j, err)
			}
		}
	}
	svc := service.New(store, live.New(), "https://comment.akarpov.ru")
	api, err := New(svc, Config{BaseURL: "https://comment.akarpov.ru", SigningSecret: make([]byte, 32)})
	if err != nil {
		t.Fatalf("API server: %v", err)
	}
	visitor, err := api.visitors.Issue()
	if err != nil {
		t.Fatalf("visitor: %v", err)
	}
	query := `query {
  posts(first:10) {edges {node {author {name}}}}
  post(id:"1") { comments(first:50) { edges {node {
   body viewerHasLiked replies(first:3) {edges {node {id body viewerHasLiked}}}
  }}}}
 }`
	response := callGraphQL(t, api.Handler(), query, "", visitor)
	if len(response.Errors) != 0 {
		t.Fatalf("nested GraphQL query failed: %+v", response.Errors)
	}
	// Batching uses a timer and gqlgen schedules siblings concurrently, so the
	// number of calls depends on scheduling (especially under -race). Assert a
	// substantial reduction relative to 50 parents / 200 comment likes, rather
	// than demanding exactly one batch per level. A regression to N+1 fails.
	if got := store.batchCalls.Load(); got < 2 || got > 8 {
		t.Fatalf("CommentsBatch calls = %d for 50 parents; want 2–8", got)
	}
	if got := store.largestBatch.Load(); got < 7 {
		t.Fatalf("largest comments batch = %d; expected multi-parent batching", got)
	}
	if got := store.userCalls.Load(); got > 1 {
		t.Fatalf("User batch calls = %d; want at most 1", got)
	}
	if got := store.likeCalls.Load(); got < 1 || got > 8 {
		t.Fatalf("Like batch calls = %d for 200 comments; want 1–8", got)
	}
	if got := store.largestLike.Load(); got < 20 {
		t.Fatalf("largest likes batch = %d; expected multi-comment batching", got)
	}
}
