package memory

import (
	"context"
	"testing"

	"github.com/Alexander-D-Karpov/commeng/internal/domain"
)

func TestBackwardPagesReturnNearestSiblings(t *testing.T) {
	ctx := context.Background()
	store := New()
	owner := mustUser(t, store, domain.RoleUser)
	post := mustPost(t, store, owner.ID)
	for i := 0; i < 65; i++ {
		mustComment(t, store, post.ID, nil)
	}
	for _, sortOrder := range []domain.Sort{domain.SortNewest, domain.SortOldest} {
		t.Run(string(sortOrder), func(t *testing.T) {
			first, err := store.Comments(ctx, domain.CommentPageRequest{
				PostID: post.ID, First: 20, Sort: sortOrder,
			})
			if err != nil || len(first.Items) != 20 || first.HasPrev || !first.HasNext {
				t.Fatalf("first page: %+v, %v", first, err)
			}
			cursor := first.Items[len(first.Items)-1].ID
			second, err := store.Comments(ctx, domain.CommentPageRequest{
				PostID: post.ID, First: 20, Sort: sortOrder, After: &cursor,
			})
			if err != nil || len(second.Items) != 20 || !second.HasPrev {
				t.Fatalf("second page: %+v, %v", second, err)
			}
			before := second.Items[0].ID
			back, err := store.Comments(ctx, domain.CommentPageRequest{
				PostID: post.ID, First: 20, Sort: sortOrder, Before: &before,
			})
			if err != nil || len(back.Items) != len(first.Items) || back.HasPrev || !back.HasNext {
				t.Fatalf("backward page: %+v, %v", back, err)
			}
			for i := range first.Items {
				if back.Items[i].ID != first.Items[i].ID {
					t.Fatalf("index %d: backward ID=%d, first-page ID=%d", i, back.Items[i].ID, first.Items[i].ID)
				}
			}
		})
	}
}

func TestBackwardPagingRejectsMixedBounds(t *testing.T) {
	store := New()
	id := int64(1)
	_, err := store.Comments(context.Background(), domain.CommentPageRequest{
		PostID: 1, First: 10, After: &id, Before: &id, Sort: domain.SortNewest,
	})
	if !domain.HasCode(err, domain.ErrInput) {
		t.Fatalf("expected input error when both after and before are set, got %v", err)
	}
}
