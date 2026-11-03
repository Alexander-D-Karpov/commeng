//go:build integration

package integration

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Alexander-D-Karpov/commeng/internal/domain"
	"github.com/Alexander-D-Karpov/commeng/internal/repository"
	"github.com/Alexander-D-Karpov/commeng/internal/repository/memory"
	"github.com/Alexander-D-Karpov/commeng/internal/repository/postgres"
	"github.com/Alexander-D-Karpov/commeng/internal/repository/repotest"
)

// postgresFixture creates an isolated schema for every test. No shared tables
// are truncated, so an incorrect test DSN cannot destroy application data.
func postgresFixture(t *testing.T) (repository.Repository, string) {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Fatal("TEST_DATABASE_URL is required; use a disposable PostgreSQL instance")
	}
	parsed, err := url.Parse(dsn)
	if err != nil || (parsed.Scheme != "postgres" && parsed.Scheme != "postgresql") {
		t.Fatalf("TEST_DATABASE_URL must be a PostgreSQL connection URL: %v", err)
	}
	var random [8]byte
	if _, err := rand.Read(random[:]); err != nil {
		t.Fatalf("random test schema: %v", err)
	}
	schema := "commeng_test_" + hex.EncodeToString(random[:])
	administrator, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	ctx := context.Background()
	if _, err := administrator.Exec(ctx, `CREATE SCHEMA `+schema); err != nil {
		administrator.Close()
		t.Fatalf("create isolated schema: %v", err)
	}
	params := parsed.Query()
	params.Set("search_path", schema)
	parsed.RawQuery = params.Encode()
	scopedURL := parsed.String()
	store, err := postgres.Open(ctx, scopedURL)
	if err != nil {
		_, _ = administrator.Exec(ctx, `DROP SCHEMA `+schema+` CASCADE`)
		administrator.Close()
		t.Fatalf("open scoped PostgreSQL store: %v", err)
	}
	t.Cleanup(func() {
		store.Close()
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if _, err := administrator.Exec(cleanupCtx, `DROP SCHEMA `+schema+` CASCADE`); err != nil {
			t.Errorf("drop isolated test schema: %v", err)
		}
		administrator.Close()
	})
	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("apply isolated migrations: %v", err)
	}
	return store, scopedURL
}

func freshPostgres(t *testing.T) repository.Repository {
	store, _ := postgresFixture(t)
	return store
}

func TestRepositoryContractBothBackends(t *testing.T) {
	factories := map[string]func(*testing.T) repository.Repository{
		"memory": func(t *testing.T) repository.Repository {
			return memory.New()
		},
		"postgres": freshPostgres,
	}
	for name, factory := range factories {
		t.Run(name, func(t *testing.T) {
			repotest.Run(t, factory(t))
		})
	}
}

// A cancelled request must only fail that transaction, not poison subsequent
// PostgreSQL writes. The old implementation permanently fenced the writer.
func TestPostgresCancellationDoesNotFenceWriter(t *testing.T) {
	ctx := context.Background()
	store := freshPostgres(t)
	owner, err := store.CreateUser(ctx, domain.User{
		Name: "Writer", Email: "writer@example.org", PasswordHash: "test-hash", Role: domain.RoleUser,
	})
	if err != nil {
		t.Fatal(err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := store.CreatePost(canceled, owner.ID, "Canceled", "No commit"); err == nil {
		t.Fatal("a cancelled transaction unexpectedly succeeded")
	}
	created, err := store.CreatePost(ctx, owner.ID, "Next", "Must work")
	if err != nil {
		t.Fatalf("healthy request after cancellation failed: %v", err)
	}
	if created.ID == 0 {
		t.Fatal("new post does not have an ID")
	}
}

func TestPostgresRejectsCrossPostParent(t *testing.T) {
	ctx := context.Background()
	store := freshPostgres(t)
	owner, err := store.CreateUser(ctx, domain.User{
		Name: "Owner", Email: "owner@example.org", PasswordHash: "test-hash", Role: domain.RoleUser,
	})
	if err != nil {
		t.Fatal(err)
	}
	sourcePost, err := store.CreatePost(ctx, owner.ID, "Source", "Post")
	if err != nil {
		t.Fatal(err)
	}
	otherPost, err := store.CreatePost(ctx, owner.ID, "Other", "Post")
	if err != nil {
		t.Fatal(err)
	}
	parent, err := store.CreateComment(ctx, domain.CommentCreate{
		PostID: sourcePost.ID, Name: "Reader", Email: "reader@example.org",
		AvatarHash: strings.Repeat("0", 64), Body: "Parent",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateComment(ctx, domain.CommentCreate{
		PostID: otherPost.ID, ParentID: &parent.ID, Name: "Reader",
		Email: "reader@example.org", AvatarHash: strings.Repeat("0", 64),
		Body: "Cross-post reply",
	}); err == nil {
		t.Fatal("accepted reply whose parent belongs to another post")
	}
}

// This exercises the real SQL hierarchy without an O(depth²) sequence of
// independent application transactions just to construct the fixture.
func TestPostgresDeepHierarchy(t *testing.T) {
	ctx := context.Background()
	store, scopedURL := postgresFixture(t)
	owner, err := store.CreateUser(ctx, domain.User{
		Name: "Deep Author", Email: "deep@example.org", PasswordHash: "test", Role: domain.RoleUser,
	})
	if err != nil {
		t.Fatalf("create owner: %v", err)
	}
	post, err := store.CreatePost(ctx, owner.ID, "Deep", "Discussion")
	if err != nil {
		t.Fatalf("create post: %v", err)
	}
	pool, err := pgxpool.New(ctx, scopedURL)
	if err != nil {
		t.Fatalf("open SQL fixture: %v", err)
	}
	defer pool.Close()

	// The schema is created exclusively for this test. Only the generated post
	// ID is interpolated into this trusted SQL fixture, never client input.
	statement := fmt.Sprintf(`
        DO $do$
        DECLARE parent bigint := NULL;
        BEGIN
          FOR i IN 1..10000 LOOP
            INSERT INTO comments(
              post_id, parent_id, author_name, author_email, avatar_hash, body
            ) VALUES (
              %d, parent, 'Reader', 'reader@example.org', repeat('0', 64), 'Deep reply'
            ) RETURNING id INTO parent;
          END LOOP;
        END $do$;`, post.ID)
	if _, err := pool.Exec(ctx, statement); err != nil {
		t.Fatalf("create 10k deep comments: %v", err)
	}
	var leaf int64
	if err := pool.QueryRow(ctx, `SELECT MAX(id) FROM comments WHERE post_id=$1`, post.ID).Scan(&leaf); err != nil {
		t.Fatalf("read deepest comment: %v", err)
	}
	ancestors, err := store.Ancestors(ctx, leaf, 100)
	if err != nil || len(ancestors) != 100 {
		t.Fatalf("deep ancestors: count=%d, error=%v", len(ancestors), err)
	}
	if _, err := store.CreateComment(ctx, domain.CommentCreate{
		PostID: post.ID, ParentID: &leaf, Name: "Reader", Email: "reader@example.org",
		AvatarHash: strings.Repeat("0", 64), Body: "One more level",
	}); err != nil {
		t.Fatalf("reply at depth 10000: %v", err)
	}
}

func TestPostgresConstraintErrorDoesNotFenceWrites(t *testing.T) {
	ctx := context.Background()
	store := freshPostgres(t)
	author, err := store.CreateUser(ctx, domain.User{
		Name: "Author", Email: "constraint@example.org", PasswordHash: "test", Role: domain.RoleUser,
	})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	post, err := store.CreatePost(ctx, author.ID, "Valid", "Body")
	if err != nil {
		t.Fatalf("create post: %v", err)
	}
	_, err = store.CreateComment(ctx, domain.CommentCreate{
		PostID: post.ID, Name: "Reader", Email: "reader@example.org",
		AvatarHash: strings.Repeat("0", 64), Body: "",
	})
	if err == nil {
		t.Fatal("SQL CHECK accepted an empty comment")
	}
	_, err = store.CreateComment(ctx, domain.CommentCreate{
		PostID: post.ID, Name: "Reader", Email: "reader@example.org",
		AvatarHash: strings.Repeat("0", 64), Body: "Valid comment",
	})
	if err != nil {
		t.Fatalf("write after CHECK rejection failed: %v", err)
	}
}
