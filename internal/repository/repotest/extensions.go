package repotest

import (
	"context"
	"strings"
	"testing"

	"github.com/Alexander-D-Karpov/commeng/internal/domain"
	"github.com/Alexander-D-Karpov/commeng/internal/repository"
)

// runExtensions is executed against both repository backends. In particular,
// author permissions, event replay, tombstones, search and notification semantics
// must not diverge between the in-memory demo and PostgreSQL production mode.
func runExtensions(t *testing.T, store repository.Repository) {
	t.Helper()
	ctx := context.Background()
	owner := createTestUser(ctx, t, store, "extension-owner@example.org")
	writer := createTestUser(ctx, t, store, "extension-writer@example.org")
	outsider := createTestUser(ctx, t, store, "extension-outsider@example.org")
	post, err := store.CreatePost(ctx, owner.ID, "Gopher community", "Searchable sample")
	if err != nil {
		t.Fatal(err)
	}
	if post.EventVersion != 0 {
		t.Fatal("new post should not have an event")
	}
	if _, err := store.EditPost(ctx, post.ID, outsider.ID, "Other", "Body"); !domain.HasCode(err, domain.ErrForbidden) {
		t.Fatalf("other user modified post: %v", err)
	}
	post, err = store.EditPost(ctx, post.ID, owner.ID, "Gopher testing", "Edited searchable body")
	if err != nil || post.Title != "Gopher testing" || post.UpdatedAt.IsZero() {
		t.Fatalf("edit post: %+v, %v", post, err)
	}
	results, err := store.SearchPosts(ctx, "Gopher", nil, 5)
	if err != nil || !containsPost(results.Items, post.ID) {
		t.Fatalf("search did not find edited post: %+v %v", results, err)
	}
	writerID := writer.ID
	root, err := store.CreateComment(ctx, domain.CommentCreate{
		PostID: post.ID, UserID: &writerID, Name: "Writer", Email: writer.Email,
		AvatarHash: strings.Repeat("0", 64), Body: "Original searchable comment",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.EditComment(ctx, root.ID, outsider.ID, "Forbidden"); !domain.HasCode(err, domain.ErrForbidden) {
		t.Fatalf("other user modified comment: %v", err)
	}
	root, err = store.EditComment(ctx, root.ID, writer.ID, "Edited searchable comment")
	if err != nil || root.EditedAt == nil {
		t.Fatalf("edit comment: %+v %v", root, err)
	}
	// Batch projection must include edited_at (this regressed when the SQL
	// commentColumns list changed without the explicit LATERAL projection).
	batchEdited, err := store.CommentsBatch(
		ctx,
		[]domain.CommentPageRequest{{PostID: post.ID, Sort: domain.SortNewest, First: 5}},
	)
	if err != nil || len(batchEdited) != 1 || len(batchEdited[0].Items) != 1 ||
		batchEdited[0].Items[0].EditedAt == nil {
		t.Fatalf("batched edited timestamp: %+v %v", batchEdited, err)
	}
	found, err := store.SearchComments(ctx, "searchable", &post.ID, nil, 5)
	if err != nil || len(found.Items) != 1 || found.Items[0].ID != root.ID {
		t.Fatalf("comment search: %+v %v", found, err)
	}
	reply := checkExtensionEvents(ctx, t, store, post, root, writer, outsider)
	checkExtensionDeletion(ctx, t, store, post, root, reply, owner, writer)
}

func checkExtensionEvents(
	ctx context.Context,
	t *testing.T,
	store repository.Repository,
	post domain.Post,
	root domain.Comment,
	writer, outsider domain.User,
) domain.Comment {
	t.Helper()
	reply, err := store.CreateComment(ctx, domain.CommentCreate{
		PostID: post.ID, ParentID: &root.ID, Name: "Guest", Email: "guest@example.org",
		AvatarHash: strings.Repeat("0", 64), Body: "Anonymous reply",
	})
	if err != nil {
		t.Fatal(err)
	}
	events, err := store.CommentEvents(ctx, post.ID, 0, 1)
	if err != nil || len(events.Items) != 1 || !events.HasNext || events.Items[0].Sequence != 1 {
		t.Fatalf("first event page: %+v %v", events, err)
	}
	events, err = store.CommentEvents(ctx, post.ID, 1, 1)
	if err != nil || !events.HasPrev || len(events.Items) != 1 || events.Items[0].Sequence != 2 ||
		events.Items[0].Comment.ID != reply.ID {
		t.Fatalf("second event page: %+v %v", events, err)
	}
	head, err := store.CommentEventHead(ctx, post.ID)
	if err != nil || head != 2 {
		t.Fatalf("event head = %d, %v", head, err)
	}
	inbox, err := store.Notifications(ctx, writer.ID, nil, 10, true)
	if err != nil || len(inbox.Items) != 1 || inbox.Items[0].CommentID != reply.ID {
		t.Fatalf("reply notification: %+v %v", inbox, err)
	}
	if count, err := store.MarkNotificationsRead(ctx, outsider.ID, []int64{inbox.Items[0].ID}); err != nil ||
		count != 0 {
		t.Fatalf("other user marked notification: %d %v", count, err)
	}
	if count, err := store.MarkNotificationsRead(ctx, writer.ID, []int64{inbox.Items[0].ID}); err != nil || count != 1 {
		t.Fatalf("mark notification read: %d %v", count, err)
	}
	unread, err := store.Notifications(ctx, writer.ID, nil, 10, true)
	if err != nil || len(unread.Items) != 0 {
		t.Fatalf("read notification still unread: %+v %v", unread, err)
	}
	return reply
}

func checkExtensionDeletion(
	ctx context.Context,
	t *testing.T,
	store repository.Repository,
	post domain.Post,
	root, reply domain.Comment,
	owner, writer domain.User,
) {
	t.Helper()
	root, err := store.DeleteComment(ctx, root.ID, writer.ID)
	if err != nil || root.State != domain.StateDeleted || root.Email != "deleted@invalid.example" {
		t.Fatalf("soft-delete comment: %+v %v", root, err)
	}
	keptReply, err := store.Comment(ctx, reply.ID)
	if err != nil || keptReply.ParentID == nil || *keptReply.ParentID != root.ID {
		t.Fatalf("reply was orphaned by tombstone: %+v %v", keptReply, err)
	}
	if _, err := store.EditComment(ctx, root.ID, writer.ID, "Restore"); err == nil {
		t.Fatal("deleted comment could be edited")
	}
	post, err = store.DeletePost(ctx, post.ID, owner.ID)
	if err != nil || !post.Deleted || post.CommentsEnabled {
		t.Fatalf("post tombstone: %+v %v", post, err)
	}
	searchAfter, err := store.SearchPosts(ctx, "Gopher", nil, 10)
	if err != nil || containsPost(searchAfter.Items, post.ID) {
		t.Fatalf("deleted post still searchable: %+v %v", searchAfter, err)
	}
	if _, err := store.CreateComment(ctx, domain.CommentCreate{
		PostID: post.ID, Name: "Test", Email: "guest@example.org",
		AvatarHash: strings.Repeat("0", 64), Body: "No",
	}); !domain.HasCode(err, domain.ErrDisabled) {
		t.Fatalf("deleted post accepted new comment: %v", err)
	}
}

func createTestUser(
	ctx context.Context,
	t *testing.T,
	store repository.Repository,
	email string,
) domain.User {
	t.Helper()
	user, err := store.CreateUser(
		ctx,
		domain.User{Name: email, Email: email, PasswordHash: "test-hash", Role: domain.RoleUser},
	)
	if err != nil {
		t.Fatal(err)
	}
	return user
}
func containsPost(items []domain.Post, id int64) bool {
	for _, v := range items {
		if v.ID == id {
			return true
		}
	}
	return false
}
