//go:build integration

package integration

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Alexander-D-Karpov/commeng/internal/domain"
	"github.com/Alexander-D-Karpov/commeng/internal/repository/postgres"
)

func TestPostgresEventRetentionAndExpiredReplay(t *testing.T) {
	ctx := context.Background()
	storeInterface, dsn := postgresFixture(t)
	store := storeInterface.(*postgres.Store)
	user, err := store.CreateUser(ctx, domain.User{
		Name: "Author", Email: "retention@example.org", PasswordHash: "test", Role: domain.RoleUser,
	})
	if err != nil {
		t.Fatal(err)
	}
	post, err := store.CreatePost(ctx, user.ID, "Retention", "Test")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if _, err := store.CreateComment(ctx, domain.CommentCreate{
			PostID: post.ID, Name: "Reader", Email: "reader@example.org",
			AvatarHash: strings.Repeat("0", 64), Body: "One more comment",
		}); err != nil {
			t.Fatal(err)
		}
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if _, err := pool.Exec(ctx, `UPDATE comment_events SET created_at=now()-interval '40 days'
		WHERE post_id=$1 AND sequence<=2`, post.ID); err != nil {
		t.Fatal(err)
	}
	cutoff := time.Now().Add(-30 * 24 * time.Hour)
	count, err := store.CountExpiredEvents(ctx, cutoff)
	if err != nil || count != 2 {
		t.Fatalf("expired event count %d err=%v", count, err)
	}
	removed, err := store.PruneCommentEvents(ctx, cutoff, 10)
	if err != nil || removed != 2 {
		t.Fatalf("pruned %d err=%v", removed, err)
	}
	if _, err := store.CommentEvents(ctx, post.ID, 1, 10); !domain.HasCode(err, domain.ErrHistoryExpired) {
		t.Fatalf("stale event cursor accepted: %v", err)
	}
	page, err := store.CommentEvents(ctx, post.ID, 2, 10)
	if err != nil || len(page.Items) != 1 || page.Items[0].Sequence != 3 {
		t.Fatalf("current event replay failed: %+v %v", page, err)
	}
}

func TestPostgresReplayDetectsGapsAfterRetention(t *testing.T) {
	for _, test := range []struct {
		name     string
		pruned   int64
		after    int64
		pageSize int
	}{
		{name: "first missing", pruned: 2, after: 1, pageSize: 10},
		{name: "gap at next page", pruned: 3, after: 1, pageSize: 1},
		{name: "missing tail", pruned: 4, after: 2, pageSize: 10},
		{name: "all remaining missing", pruned: 4, after: 3, pageSize: 10},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			storeInterface, dsn := postgresFixture(t)
			store := storeInterface.(*postgres.Store)
			owner := repositoryUser(t, store, "event-gaps@example.org", domain.RoleUser)
			post := repositoryPost(t, store, owner, "Event gaps")
			for i := 0; i < 4; i++ {
				repositoryComment(t, store, post, nil)
			}
			pool, err := pgxpool.New(ctx, dsn)
			if err != nil {
				t.Fatal(err)
			}
			defer pool.Close()
			if _, err := pool.Exec(ctx, `UPDATE comment_events SET created_at=now()-interval '40 days'
				WHERE post_id=$1 AND sequence=$2`, post.ID, test.pruned); err != nil {
				t.Fatal(err)
			}
			if removed, err := store.PruneCommentEvents(ctx, time.Now().Add(-30*24*time.Hour), 10); err != nil ||
				removed != 1 {
				t.Fatalf("prune: removed=%d, err=%v", removed, err)
			}
			if _, err := store.CommentEvents(ctx, post.ID, test.after, test.pageSize); !domain.HasCode(
				err,
				domain.ErrHistoryExpired,
			) {
				t.Fatalf("undetected sequence gap: %v", err)
			}
		})
	}
}
