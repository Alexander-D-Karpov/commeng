package memory

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/Alexander-D-Karpov/commeng/internal/domain"
)

func mustUser(t *testing.T, s *Store, role domain.Role) domain.User {
	t.Helper()
	name := fmt.Sprintf("user-%d", len(s.users)+1)
	user, err := s.CreateUser(
		context.Background(),
		domain.User{Name: name, Email: name + "@example.com", PasswordHash: "hash", Role: role},
	)
	if err != nil {
		t.Fatal(err)
	}
	return user
}
func mustPost(t *testing.T, s *Store, owner int64) domain.Post {
	t.Helper()
	p, err := s.CreatePost(context.Background(), owner, "title", "body")
	if err != nil {
		t.Fatal(err)
	}
	return p
}
func mustComment(t *testing.T, s *Store, post int64, parent *int64) domain.Comment {
	t.Helper()
	c, err := s.CreateComment(context.Background(), domain.CommentCreate{
		PostID: post, ParentID: parent, Name: "reader",
		Email: "r@example.com", Body: "test", AvatarHash: "0",
	})
	if err != nil {
		t.Fatal(err)
	}
	return c
}
func TestBothPaginationDirectionsAndJump(t *testing.T) {
	ctx := context.Background()
	s := New()
	owner := mustUser(t, s, domain.RoleUser)
	post := mustPost(t, s, owner.ID)
	for i := 0; i < 67; i++ {
		mustComment(t, s, post.ID, nil)
	}
	for _, sort := range []domain.Sort{domain.SortNewest, domain.SortOldest} {
		seen := make(map[int64]bool)
		var after *int64
		for {
			page, err := s.Comments(
				ctx,
				domain.CommentPageRequest{PostID: post.ID, First: 20, After: after, Sort: sort},
			)
			if err != nil {
				t.Fatal(err)
			}
			for _, item := range page.Items {
				if seen[item.ID] {
					t.Fatalf("duplicate ID %d in %s", item.ID, sort)
				}
				seen[item.ID] = true
				location, err := s.LocateComment(ctx, item.ID, 20, sort)
				if err != nil {
					t.Fatal(err)
				}
				if location.Page.Items[location.IndexInPage].ID != item.ID {
					t.Fatalf("wrong page location for %d (%s)", item.ID, sort)
				}
				wantPage := (len(seen)-1)/20 + 1
				if location.PageNumber != int64(wantPage) {
					t.Fatalf("item %d page=%d want=%d", item.ID, location.PageNumber, wantPage)
				}
			}
			if !page.HasNext {
				break
			}
			last := page.Items[len(page.Items)-1].ID
			after = &last
		}
		if len(seen) != 67 {
			t.Fatalf("%s: %d comments read, expected 67", sort, len(seen))
		}
	}
}
func TestPostLockAndCrossPostParent(t *testing.T) {
	ctx := context.Background()
	s := New()
	user := mustUser(t, s, domain.RoleUser)
	a := mustPost(t, s, user.ID)
	b := mustPost(t, s, user.ID)
	parent := mustComment(t, s, a.ID, nil)
	if _, err := s.CreateComment(ctx, domain.CommentCreate{PostID: b.ID, ParentID: &parent.ID}); !domain.HasCode(
		err,
		domain.ErrInput,
	) {
		t.Fatalf("expected invalid parent, got %v", err)
	}
	if _, err := s.SetCommentsEnabled(ctx, a.ID, user.ID, false); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateComment(ctx, domain.CommentCreate{PostID: a.ID, ParentID: &parent.ID}); !domain.HasCode(
		err,
		domain.ErrDisabled,
	) {
		t.Fatalf("closed post should reject reply: %v", err)
	}
}
func TestConcurrentLikesAndUnlikes(t *testing.T) {
	ctx := context.Background()
	s := New()
	owner := mustUser(t, s, domain.RoleUser)
	p := mustPost(t, s, owner.ID)
	c := mustComment(t, s, p.ID, nil)
	var group sync.WaitGroup
	for i := 0; i < 120; i++ {
		group.Add(1)
		go func(i int) {
			defer group.Done()
			key := fmt.Sprintf("visitor-%d", i%10)
			if _, err := s.SetLike(ctx, c.ID, key, true); err != nil {
				t.Error(err)
			}
		}(i)
	}
	group.Wait()
	comment, err := s.Comment(ctx, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if comment.LikesCount != 10 {
		t.Fatalf("expected 10 unique likes, got %d", comment.LikesCount)
	}
	for i := 0; i < 60; i++ {
		group.Add(1)
		go func() {
			defer group.Done()
			_, err := s.SetLike(ctx, c.ID, "visitor-0", false)
			if err != nil {
				t.Error(err)
			}
		}()
	}
	group.Wait()
	comment, err = s.Comment(ctx, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if comment.LikesCount != 9 {
		t.Fatalf("expected 9 likes after repeated unlike, got %d", comment.LikesCount)
	}
}
func TestMoveCycleAndVersion(t *testing.T) {
	ctx := context.Background()
	s := New()
	moderator := mustUser(t, s, domain.RoleModerator)
	post := mustPost(t, s, moderator.ID)
	root := mustComment(t, s, post.ID, nil)
	child := mustComment(t, s, post.ID, &root.ID)
	version := s.posts[post.ID].TreeVersion
	if _, err := s.Moderate(ctx, domain.Moderation{
		CommentID: root.ID, ActorID: moderator.ID, Kind: domain.MoveComment, NewParentID: &child.ID, ExpectedVersion: version,
	}); !domain.HasCode(err, domain.ErrInput) {
		t.Fatalf("expected cycle rejection, got %v", err)
	}
	other := mustComment(t, s, post.ID, nil)
	version = s.posts[post.ID].TreeVersion
	updated, err := s.Moderate(ctx, domain.Moderation{
		CommentID: child.ID, ActorID: moderator.ID, Kind: domain.MoveComment,
		NewParentID: &other.ID, ExpectedVersion: version,
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.ParentID == nil || *updated.ParentID != other.ID {
		t.Fatal("wrong moved parent")
	}
	_, err = s.Moderate(ctx, domain.Moderation{
		CommentID: root.ID, ActorID: moderator.ID, Kind: domain.HideComment,
		ExpectedVersion: version,
	})
	if err != nil {
		t.Fatalf("idempotent hide must not reject stale version: %v", err)
	}
	_, err = s.Moderate(ctx, domain.Moderation{
		CommentID: child.ID, ActorID: moderator.ID, Kind: domain.MoveComment,
		ExpectedVersion: version,
	})
	if !domain.HasCode(err, domain.ErrConflict) {
		t.Fatalf("MOVE accepted a stale tree version: %v", err)
	}
}
func TestSessionExpiry(t *testing.T) {
	ctx := context.Background()
	s := New()
	u := mustUser(t, s, domain.RoleUser)
	if err := s.SaveSession(ctx, "token", u.ID, time.Now().Add(-time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SessionUser(ctx, "token"); !domain.HasCode(err, domain.ErrAuth) {
		t.Fatalf("expired session accepted: %v", err)
	}
}
func TestDeepChain(t *testing.T) {
	ctx := context.Background()
	s := New()
	u := mustUser(t, s, domain.RoleUser)
	post := mustPost(t, s, u.ID)
	root := mustComment(t, s, post.ID, nil)
	parent := root
	for i := 0; i < 1000; i++ {
		parent = mustComment(t, s, post.ID, &parent.ID)
	}
	path, err := s.Ancestors(ctx, parent.ID, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(path) != 50 {
		t.Fatalf("expected 50 ancestors, got %d", len(path))
	}
	if _, err := s.LocateComment(ctx, parent.ID, 20, domain.SortNewest); err != nil {
		t.Fatal(err)
	}
}

func TestInvalidPageParametersReturnErrors(t *testing.T) {
	ctx := context.Background()
	s := New()
	user := mustUser(t, s, domain.RoleUser)
	post := mustPost(t, s, user.ID)
	comment := mustComment(t, s, post.ID, nil)

	if _, err := s.Posts(ctx, nil, 0); !domain.HasCode(err, domain.ErrInput) {
		t.Fatalf("Posts(first=0): want validation error, got %v", err)
	}
	request := domain.CommentPageRequest{PostID: post.ID, First: 20, Sort: "UNKNOWN"}
	if _, err := s.Comments(ctx, request); !domain.HasCode(err, domain.ErrInput) {
		t.Fatalf("Comments with unknown sort: want error, got %v", err)
	}
	if _, err := s.CommentsBatch(ctx, []domain.CommentPageRequest{request}); !domain.HasCode(err, domain.ErrInput) {
		t.Fatalf("CommentsBatch with unknown sort: want error, got %v", err)
	}
	if _, err := s.LocateComment(ctx, comment.ID, 20, "UNKNOWN"); !domain.HasCode(err, domain.ErrInput) {
		t.Fatalf("LocateComment with unknown sort: want error, got %v", err)
	}
	if _, err := s.Ancestors(ctx, comment.ID, -1); !domain.HasCode(err, domain.ErrInput) {
		t.Fatalf("Ancestors negative limit: want error, got %v", err)
	}
}

func TestPostKeysetPagination(t *testing.T) {
	ctx := context.Background()
	store := New()
	owner := mustUser(t, store, domain.RoleUser)
	for i := 0; i < 109; i++ {
		mustPost(t, store, owner.ID)
	}
	var after *int64
	seen := make(map[int64]bool)
	for {
		page, err := store.Posts(ctx, after, 13)
		if err != nil {
			t.Fatal(err)
		}
		for _, post := range page.Items {
			if seen[post.ID] {
				t.Fatalf("duplicate post %d", post.ID)
			}
			seen[post.ID] = true
		}
		if !page.HasNext {
			break
		}
		id := page.Items[len(page.Items)-1].ID
		after = &id
	}
	if len(seen) != 109 {
		t.Fatalf("got %d posts, want 109", len(seen))
	}
}

func TestTreeVersionChangesOnlyOnModeration(t *testing.T) {
	ctx := context.Background()
	store := New()
	author := mustUser(t, store, domain.RoleUser)
	post := mustPost(t, store, author.ID)
	version := post.TreeVersion
	root := mustComment(t, store, post.ID, nil)
	child := mustComment(t, store, post.ID, &root.ID)
	current, err := store.Post(ctx, post.ID)
	if err != nil {
		t.Fatalf("read post: %v", err)
	}
	if current.TreeVersion != version {
		t.Fatalf("comment creation changed tree version: got %d, want %d", current.TreeVersion, version)
	}
	_, err = store.Moderate(ctx, domain.Moderation{
		CommentID: root.ID, ActorID: author.ID, Kind: domain.HideComment,
	})
	if err != nil {
		t.Fatalf("hide without version: %v", err)
	}
	current, err = store.Post(ctx, post.ID)
	if err != nil || current.TreeVersion != version+1 {
		t.Fatalf("moderation must advance version: %+v, %v", current, err)
	}
	blocked, err := store.Comment(ctx, child.ID)
	if err != nil || !blocked.ThreadBlocked {
		t.Fatalf("child should inherit hidden policy: %+v, %v", blocked, err)
	}
	if _, err := store.CreateComment(ctx, domain.CommentCreate{PostID: post.ID, ParentID: &child.ID}); !domain.HasCode(
		err,
		domain.ErrDisabled,
	) {
		t.Fatalf("hidden ancestors must block replies: %v", err)
	}
	_, err = store.Moderate(ctx, domain.Moderation{
		CommentID: root.ID, ActorID: author.ID, Kind: domain.RestoreComment,
	})
	if err != nil {
		t.Fatalf("restore: %v", err)
	}
	restored, _ := store.Comment(ctx, child.ID)
	if restored.ThreadBlocked {
		t.Fatal("restoring root must clear inherited blocking")
	}
}

func TestRegisteredCommentAuthorAndSessionRevocation(t *testing.T) {
	ctx := context.Background()
	store := New()
	author := mustUser(t, store, domain.RoleUser)
	post := mustPost(t, store, author.ID)
	comment, err := store.CreateComment(ctx, domain.CommentCreate{
		PostID: post.ID, UserID: &author.ID, Name: author.Name, Email: author.Email,
	})
	if err != nil {
		t.Fatalf("create signed-in comment: %v", err)
	}
	if !comment.IsPostAuthor || comment.UserID == nil || *comment.UserID != author.ID {
		t.Fatalf("missing user ownership: %+v", comment)
	}
	if err := store.SaveSession(ctx, "secret-token-hash", author.ID, time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("create session: %v", err)
	}
	if err := store.DeleteSession(ctx, "secret-token-hash"); err != nil {
		t.Fatalf("logout: %v", err)
	}
	if _, err := store.SessionUser(ctx, "secret-token-hash"); !domain.HasCode(err, domain.ErrAuth) {
		t.Fatalf("revoked session remains active: %v", err)
	}
}

// A no-op move should not invalidate an administrator's structural revision
// or duplicate the node in the parent's child index.
func TestMoveToSameParentIsNoop(t *testing.T) {
	ctx := context.Background()
	store := New()
	moderator := mustUser(t, store, domain.RoleModerator)
	post := mustPost(t, store, moderator.ID)
	parent := mustComment(t, store, post.ID, nil)
	child := mustComment(t, store, post.ID, &parent.ID)

	before := store.posts[post.ID].TreeVersion
	moved, err := store.Moderate(ctx, domain.Moderation{
		CommentID:       child.ID,
		ActorID:         moderator.ID,
		Kind:            domain.MoveComment,
		NewParentID:     &parent.ID,
		ExpectedVersion: before,
	})
	if err != nil {
		t.Fatalf("repeat move to current parent: %v", err)
	}
	if moved.ParentID == nil || *moved.ParentID != parent.ID {
		t.Fatalf("moved parent = %v; want %d", moved.ParentID, parent.ID)
	}
	if got := store.posts[post.ID].TreeVersion; got != before {
		t.Fatalf("tree version after no-op move = %d; want %d", got, before)
	}
	page, err := store.Comments(ctx, domain.CommentPageRequest{
		PostID:   post.ID,
		ParentID: &parent.ID,
		First:    20,
		Sort:     domain.SortNewest,
	})
	if err != nil {
		t.Fatalf("load children after no-op move: %v", err)
	}
	if len(page.Items) != 1 || page.Items[0].ID != child.ID {
		t.Fatalf("child index after no-op move = %+v", page.Items)
	}
}
