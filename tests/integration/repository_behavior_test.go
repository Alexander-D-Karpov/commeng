//go:build integration

package integration

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Alexander-D-Karpov/commeng/internal/domain"
	"github.com/Alexander-D-Karpov/commeng/internal/repository"
	"github.com/Alexander-D-Karpov/commeng/internal/repository/memory"
)

func forEachRepository(t *testing.T, check func(*testing.T, repository.Repository)) {
	t.Helper()
	for name, factory := range map[string]func(*testing.T) repository.Repository{
		"memory":   func(*testing.T) repository.Repository { return memory.New() },
		"postgres": freshPostgres,
	} {
		t.Run(name, func(t *testing.T) { check(t, factory(t)) })
	}
}

func repositoryUser(t *testing.T, store repository.Repository, email string, role domain.Role) domain.User {
	t.Helper()
	user, err := store.CreateUser(context.Background(), domain.User{
		Name: email, Email: email, PasswordHash: "test-hash", Role: role,
	})
	if err != nil {
		t.Fatal(err)
	}
	return user
}

func repositoryPost(t *testing.T, store repository.Repository, owner domain.User, title string) domain.Post {
	t.Helper()
	post, err := store.CreatePost(context.Background(), owner.ID, title, "Post body")
	if err != nil {
		t.Fatal(err)
	}
	return post
}

func repositoryComment(t *testing.T, store repository.Repository, post domain.Post, parent *int64) domain.Comment {
	t.Helper()
	comment, err := store.CreateComment(context.Background(), domain.CommentCreate{
		PostID: post.ID, ParentID: parent, Name: "Reader", Email: "reader@example.org",
		AvatarHash: strings.Repeat("0", 64), Body: "Comment body",
	})
	if err != nil {
		t.Fatal(err)
	}
	return comment
}

func TestRepositoryUsersAndSessions(t *testing.T) {
	forEachRepository(t, checkUsersAndSessions)
}

func checkUsersAndSessions(t *testing.T, store repository.Repository) {
	t.Helper()
	ctx := context.Background()
	owner := repositoryUser(t, store, "owner@example.org", domain.RoleUser)
	other := repositoryUser(t, store, "other@example.org", domain.RoleUser)
	if _, err := store.CreateUser(ctx, owner); !domain.HasCode(err, domain.ErrAlreadyExists) {
		t.Fatalf("duplicate email: %v", err)
	}
	byEmail, err := store.UserByEmail(ctx, owner.Email)
	if err != nil || byEmail.ID != owner.ID {
		t.Fatalf("user by email: %+v, %v", byEmail, err)
	}
	byID, err := store.UserByID(ctx, other.ID)
	if err != nil || byID.Email != other.Email {
		t.Fatalf("user by ID: %+v, %v", byID, err)
	}
	if _, err := store.UserByID(ctx, other.ID+1000); !domain.HasCode(err, domain.ErrNotFound) {
		t.Fatalf("missing user: %v", err)
	}
	users, err := store.Users(ctx, []int64{owner.ID, other.ID, other.ID + 1000})
	if err != nil || len(users) != 2 || users[owner.ID].Email != owner.Email {
		t.Fatalf("batch users: %+v, %v", users, err)
	}
	empty, err := store.Users(ctx, nil)
	if err != nil || len(empty) != 0 {
		t.Fatalf("empty users: %+v, %v", empty, err)
	}
	checkSessions(t, store, owner)
}

func checkSessions(t *testing.T, store repository.Repository, owner domain.User) {
	t.Helper()
	ctx := context.Background()
	if err := store.SaveSession(ctx, "valid-token", owner.ID, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	loggedIn, err := store.SessionUser(ctx, "valid-token")
	if err != nil || loggedIn.ID != owner.ID {
		t.Fatalf("valid session: %+v, %v", loggedIn, err)
	}
	if err := store.SaveSession(ctx, "expired-token", owner.ID, time.Now().Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SessionUser(ctx, "expired-token"); err == nil {
		t.Fatal("expired session accepted")
	}
	if err := store.DeleteSession(ctx, "valid-token"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SessionUser(ctx, "valid-token"); err == nil {
		t.Fatal("revoked session accepted")
	}
}

func TestRepositoryPostPaginationAndPermissions(t *testing.T) {
	forEachRepository(t, checkPostPaginationAndPermissions)
}

func checkPostPaginationAndPermissions(t *testing.T, store repository.Repository) {
	t.Helper()
	ctx := context.Background()
	owner := repositoryUser(t, store, "post-owner@example.org", domain.RoleUser)
	other := repositoryUser(t, store, "post-other@example.org", domain.RoleUser)
	posts := make([]domain.Post, 0, 4)
	for i := 0; i < 4; i++ {
		posts = append(posts, repositoryPost(t, store, owner, fmt.Sprintf("Post %d", i)))
	}
	if _, err := store.DeletePost(ctx, posts[1].ID, owner.ID); err != nil {
		t.Fatal(err)
	}
	first, err := store.Posts(ctx, nil, 2)
	if err != nil || len(first.Items) != 2 || !first.HasNext || first.Items[0].ID != posts[3].ID ||
		first.Items[1].ID != posts[2].ID {
		t.Fatalf("first post page: %+v, %v", first, err)
	}
	last, err := store.Posts(ctx, &first.Items[1].ID, 2)
	if err != nil || len(last.Items) != 1 || last.HasNext || last.Items[0].ID != posts[0].ID {
		t.Fatalf("last post page: %+v, %v", last, err)
	}
	if _, err := store.Posts(ctx, nil, 0); !domain.HasCode(err, domain.ErrInput) {
		t.Fatalf("invalid page size: %v", err)
	}
	if _, err := store.SetCommentsEnabled(ctx, posts[0].ID, other.ID, false); !domain.HasCode(
		err,
		domain.ErrForbidden,
	) {
		t.Fatalf("other user changed comments: %v", err)
	}
	if _, err := store.SetCommentsEnabled(ctx, posts[1].ID, owner.ID, false); !domain.HasCode(err, domain.ErrConflict) {
		t.Fatalf("deleted post changed comments: %v", err)
	}
}

func TestRepositoryAncestorsAndLikes(t *testing.T) {
	forEachRepository(t, checkAncestorsAndLikes)
}

func checkAncestorsAndLikes(t *testing.T, store repository.Repository) {
	t.Helper()
	ctx := context.Background()
	owner := repositoryUser(t, store, "thread-owner@example.org", domain.RoleUser)
	post := repositoryPost(t, store, owner, "Thread")
	root := repositoryComment(t, store, post, nil)
	child := repositoryComment(t, store, post, &root.ID)
	grandchild := repositoryComment(t, store, post, &child.ID)
	target := repositoryComment(t, store, post, &grandchild.ID)
	first, err := store.AncestorsPage(ctx, target.ID, nil, 2)
	if err != nil || len(first.Items) != 2 || first.Items[0].ID != grandchild.ID || first.Items[1].ID != child.ID ||
		first.NextBefore == nil ||
		*first.NextBefore != child.ID {
		t.Fatalf("first ancestor page: %+v, %v", first, err)
	}
	last, err := store.AncestorsPage(ctx, target.ID, first.NextBefore, 2)
	if err != nil || len(last.Items) != 1 || last.Items[0].ID != root.ID || last.NextBefore != nil {
		t.Fatalf("last ancestor page: %+v, %v", last, err)
	}
	if _, err := store.AncestorsPage(ctx, target.ID, &target.ID, 2); !domain.HasCode(err, domain.ErrInput) {
		t.Fatalf("non-ancestor cursor: %v", err)
	}
	if _, err := store.AncestorsPage(ctx, target.ID, nil, 0); !domain.HasCode(err, domain.ErrInput) {
		t.Fatalf("invalid ancestor page size: %v", err)
	}
	if _, err := store.AncestorsPage(ctx, target.ID+1000, nil, 2); !domain.HasCode(err, domain.ErrNotFound) {
		t.Fatalf("missing target: %v", err)
	}
	checkLikes(t, store, root, child)
}

func checkLikes(t *testing.T, store repository.Repository, root, child domain.Comment) {
	t.Helper()
	ctx := context.Background()
	for i := 0; i < 2; i++ {
		liked, err := store.SetLike(ctx, root.ID, "reader", true)
		if err != nil || liked.LikesCount != 1 {
			t.Fatalf("idempotent like: %+v, %v", liked, err)
		}
	}
	liked, err := store.HasLikes(ctx, "reader", []int64{root.ID, child.ID})
	if err != nil || !liked[root.ID] || liked[child.ID] {
		t.Fatalf("batch likes: %+v, %v", liked, err)
	}
	if got, err := store.HasLikes(ctx, "", []int64{root.ID}); err != nil || got[root.ID] {
		t.Fatalf("anonymous likes: %+v, %v", got, err)
	}
	for i := 0; i < 2; i++ {
		unliked, err := store.SetLike(ctx, root.ID, "reader", false)
		if err != nil || unliked.LikesCount != 0 {
			t.Fatalf("idempotent unlike: %+v, %v", unliked, err)
		}
	}
	if _, err := store.SetLike(ctx, root.ID, "", true); !domain.HasCode(err, domain.ErrAuth) {
		t.Fatalf("anonymous like: %v", err)
	}
}

func TestRepositoryModerationMove(t *testing.T) {
	forEachRepository(t, checkModerationMove)
}

func checkModerationMove(t *testing.T, store repository.Repository) {
	t.Helper()
	ctx := context.Background()
	owner := repositoryUser(t, store, "move-owner@example.org", domain.RoleUser)
	moderator := repositoryUser(t, store, "moderator@example.org", domain.RoleModerator)
	post := repositoryPost(t, store, owner, "Moderation")
	source := repositoryComment(t, store, post, nil)
	destination := repositoryComment(t, store, post, nil)
	child := repositoryComment(t, store, post, &source.ID)
	move := domain.Moderation{
		CommentID:       child.ID,
		ActorID:         owner.ID,
		Kind:            domain.MoveComment,
		NewParentID:     &destination.ID,
		ExpectedVersion: post.TreeVersion,
	}
	if _, err := store.Moderate(ctx, move); !domain.HasCode(err, domain.ErrForbidden) {
		t.Fatalf("non-moderator moved comment: %v", err)
	}
	move.ActorID = moderator.ID
	move.ExpectedVersion++
	if _, err := store.Moderate(ctx, move); !domain.HasCode(err, domain.ErrConflict) {
		t.Fatalf("stale move accepted: %v", err)
	}
	move.ExpectedVersion = post.TreeVersion
	moved, err := store.Moderate(ctx, move)
	if err != nil || moved.ParentID == nil || *moved.ParentID != destination.ID {
		t.Fatalf("move: %+v, %v", moved, err)
	}
	oldParent, err := store.Comment(ctx, source.ID)
	if err != nil || oldParent.ReplyCount != 0 {
		t.Fatalf("old parent count: %+v, %v", oldParent, err)
	}
	newParent, err := store.Comment(ctx, destination.ID)
	if err != nil || newParent.ReplyCount != 1 {
		t.Fatalf("new parent count: %+v, %v", newParent, err)
	}
	checkModerationLocks(t, store, owner, destination, child)
}

func checkModerationLocks(t *testing.T, store repository.Repository, owner domain.User, parent, child domain.Comment) {
	t.Helper()
	ctx := context.Background()
	command := domain.Moderation{CommentID: parent.ID, ActorID: owner.ID, Kind: domain.LockComment}
	locked, err := store.Moderate(ctx, command)
	if err != nil || !locked.Locked {
		t.Fatalf("lock: %+v, %v", locked, err)
	}
	blocked, err := store.Comment(ctx, child.ID)
	if err != nil || !blocked.ThreadBlocked {
		t.Fatalf("child not blocked: %+v, %v", blocked, err)
	}
	command.Kind = domain.UnlockComment
	if _, err := store.Moderate(ctx, command); err != nil {
		t.Fatal(err)
	}
	unblocked, err := store.Comment(ctx, child.ID)
	if err != nil || unblocked.ThreadBlocked {
		t.Fatalf("child still blocked: %+v, %v", unblocked, err)
	}
}
