// Package repotest provides the same behavioral contract for memory and SQL.
package repotest

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/Alexander-D-Karpov/commeng/internal/domain"
	"github.com/Alexander-D-Karpov/commeng/internal/repository"
)

func Run(t *testing.T, store repository.Repository) {
	t.Helper()
	ctx := context.Background()
	user, err := store.CreateUser(
		ctx,
		domain.User{Name: "Author", Email: "author@example.org", PasswordHash: "hash", Role: domain.RoleAdmin},
	)
	if err != nil {
		t.Fatal(err)
	}
	post, err := store.CreatePost(ctx, user.ID, "title", "body")
	if err != nil {
		t.Fatalf("create post: %v", err)
	}
	if _, err := store.Comments(ctx, domain.CommentPageRequest{
		PostID: post.ID + 100000, First: 5, Sort: domain.SortNewest,
	}); !domain.HasCode(err, domain.ErrNotFound) {
		t.Fatalf("missing post must return NOT_FOUND, got %v", err)
	}
	if _, err := store.CommentsBatch(ctx, []domain.CommentPageRequest{
		{PostID: post.ID + 100000, First: 5, Sort: domain.SortNewest},
	}); !domain.HasCode(err, domain.ErrNotFound) {
		t.Fatalf("missing batch post must return NOT_FOUND, got %v", err)
	}
	comments := make([]domain.Comment, 0, 64)
	for i := 0; i < 64; i++ {
		c, err := store.CreateComment(ctx, domain.CommentCreate{
			PostID: post.ID, Name: "Reader", Email: "reader@example.org",
			AvatarHash: fmt.Sprintf("%064d", 1), Body: "body",
		})
		if err != nil {
			t.Fatal(err)
		}
		comments = append(comments, c)
	}
	for _, sortOrder := range []domain.Sort{domain.SortNewest, domain.SortOldest} {
		target := comments[23]
		page, err := store.LocateComment(ctx, target.ID, 20, sortOrder)
		if err != nil {
			t.Fatal(err)
		}
		if page.Page.Items[page.IndexInPage].ID != target.ID {
			t.Fatalf("wrong index for %d", target.ID)
		}
		firstPage, err := store.Comments(ctx, domain.CommentPageRequest{PostID: post.ID, First: 20, Sort: sortOrder})
		if err != nil {
			t.Fatal(err)
		}
		if len(firstPage.Items) != 20 || !firstPage.HasNext {
			t.Fatal("bad first page")
		}
		// A page reached with `after` must be reversible with `before`.
		cursor := firstPage.Items[len(firstPage.Items)-1].ID
		secondPage, err := store.Comments(ctx, domain.CommentPageRequest{
			PostID: post.ID, First: 20, Sort: sortOrder, After: &cursor,
		})
		if err != nil || len(secondPage.Items) != 20 || !secondPage.HasPrev {
			t.Fatalf("forward page: %+v, %v", secondPage, err)
		}
		before := secondPage.Items[0].ID
		previousPage, err := store.Comments(ctx, domain.CommentPageRequest{
			PostID: post.ID, First: 20, Sort: sortOrder, Before: &before,
		})
		if err != nil || len(previousPage.Items) != len(firstPage.Items) || previousPage.HasPrev {
			t.Fatalf("backward page: %+v, %v", previousPage, err)
		}
		for i, item := range firstPage.Items {
			if item.ID != previousPage.Items[i].ID {
				t.Fatalf("page mismatch at index %d: %d vs %d", i, item.ID, previousPage.Items[i].ID)
			}
		}
	}
	batch, err := store.CommentsBatch(ctx, []domain.CommentPageRequest{
		{PostID: post.ID, First: 3, Sort: domain.SortNewest},
		{PostID: post.ID, First: 3, Sort: domain.SortOldest},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(batch) != 2 || len(batch[0].Items) != 3 || len(batch[1].Items) != 3 {
		t.Fatalf("bad batch: %+v", batch)
	}
	runMutationContract(ctx, t, store, user, post, comments[12])
	runExtensions(t, store)
	runLikeVisibility(t, store)
}

func runMutationContract(
	ctx context.Context,
	t *testing.T,
	store repository.Repository,
	user domain.User,
	post domain.Post,
	target domain.Comment,
) {
	t.Helper()
	var wg sync.WaitGroup
	for i := 0; i < 80; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := store.SetLike(ctx, target.ID, "visitor-one", true); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	liked, err := store.Comment(ctx, target.ID)
	if err != nil || liked.LikesCount != 1 {
		t.Fatalf("non-idempotent likes: %+v %v", liked, err)
	}
	_, err = store.SetCommentsEnabled(ctx, post.ID, user.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.CreateComment(ctx, domain.CommentCreate{PostID: post.ID, ParentID: &target.ID, Body: "blocked"})
	if !domain.HasCode(err, domain.ErrDisabled) {
		t.Fatalf("closed post accepted reply: %v", err)
	}
	_, err = store.SetCommentsEnabled(ctx, post.ID, user.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	current, err := store.Post(ctx, post.ID)
	if err != nil {
		t.Fatal(err)
	}
	versionBefore := current.TreeVersion
	child, err := store.CreateComment(ctx, domain.CommentCreate{
		PostID: post.ID, ParentID: &target.ID, Name: "Reader",
		Email: "reader@example.org", AvatarHash: fmt.Sprintf("%064d", 1), Body: "reply",
	})
	if err != nil {
		t.Fatal(err)
	}
	current, err = store.Post(ctx, post.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current.TreeVersion != versionBefore {
		t.Fatalf("regular reply changed tree version: got %d want %d", current.TreeVersion, versionBefore)
	}
	_, err = store.Moderate(ctx, domain.Moderation{
		CommentID: target.ID, ActorID: user.ID, Kind: domain.MoveComment,
		NewParentID: &child.ID, ExpectedVersion: current.TreeVersion,
	})
	if !domain.HasCode(err, domain.ErrInput) {
		t.Fatalf("cycle accepted: %v", err)
	}
	_, err = store.Moderate(ctx, domain.Moderation{
		CommentID: target.ID, ActorID: user.ID, Kind: domain.HideComment,
	})
	if err != nil {
		t.Fatalf("hide target: %v", err)
	}
	blocked, err := store.Comment(ctx, child.ID)
	if err != nil || !blocked.ThreadBlocked {
		t.Fatalf("child must inherit blocked ancestor: %+v, %v", blocked, err)
	}
	if _, err := store.CreateComment(ctx, domain.CommentCreate{
		PostID: post.ID, ParentID: &child.ID, Name: "Reader",
		Email: "reader@example.org", AvatarHash: strings.Repeat("0", 64), Body: "blocked",
	}); !domain.HasCode(err, domain.ErrDisabled) {
		t.Fatalf("hidden ancestor accepted reply: %v", err)
	}
	_, err = store.Moderate(ctx, domain.Moderation{CommentID: target.ID, ActorID: user.ID, Kind: domain.RestoreComment})
	if err != nil {
		t.Fatalf("restore target: %v", err)
	}
}

// runLikeVisibility verifies that both repositories reject likes on comments
// hidden by an ancestor, and reject new likes after the post is deleted.
func runLikeVisibility(t *testing.T, store repository.Repository) {
	t.Helper()
	ctx := context.Background()
	owner, err := store.CreateUser(ctx, domain.User{
		Name: "Visibility owner", Email: "visibility-owner@example.org", PasswordHash: "test", Role: domain.RoleUser,
	})
	if err != nil {
		t.Fatal(err)
	}
	post, err := store.CreatePost(ctx, owner.ID, "Visibility test", "Body")
	if err != nil {
		t.Fatal(err)
	}
	root, err := store.CreateComment(ctx, domain.CommentCreate{
		PostID: post.ID, Name: "Guest", Email: "guest@example.org", AvatarHash: strings.Repeat("0", 64), Body: "root",
	})
	if err != nil {
		t.Fatal(err)
	}
	child, err := store.CreateComment(ctx, domain.CommentCreate{
		PostID: post.ID, ParentID: &root.ID, Name: "Guest", Email: "guest@example.org",
		AvatarHash: strings.Repeat("0", 64), Body: "child",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Moderate(ctx, domain.Moderation{
		CommentID: root.ID, ActorID: owner.ID, Kind: domain.HideComment,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SetLike(ctx, child.ID, "visitor-visibility", true); !domain.HasCode(err, domain.ErrInput) {
		t.Fatalf("liked a comment hidden by an ancestor: %v", err)
	}
	if _, err := store.Moderate(ctx, domain.Moderation{
		CommentID: root.ID, ActorID: owner.ID, Kind: domain.RestoreComment,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DeletePost(ctx, post.ID, owner.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SetLike(ctx, child.ID, "visitor-visibility", true); !domain.HasCode(err, domain.ErrInput) {
		t.Fatalf("liked a comment of a deleted post: %v", err)
	}
}
