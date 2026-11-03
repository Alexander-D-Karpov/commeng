package service

import (
	"context"
	"strings"
	"testing"

	"github.com/Alexander-D-Karpov/commeng/internal/domain"
	"github.com/Alexander-D-Karpov/commeng/internal/live"
	"github.com/Alexander-D-Karpov/commeng/internal/repository/memory"
)

func newService(t *testing.T) *Service {
	t.Helper()
	return New(memory.New(), live.New(), "http://localhost:8080")
}

func TestRegistrationAndLogin(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)
	for _, tt := range []struct {
		name, password string
		want           domain.Code
	}{
		{"short password", "short", domain.ErrInput},
		{"valid registration", "long-enough-password", ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, err := svc.Register(ctx, "Alice", "alice@example.org", tt.password)
			if tt.want == "" && err != nil {
				t.Fatalf("register failed: %v", err)
			}
			if tt.want != "" && !domain.HasCode(err, tt.want) {
				t.Fatalf("unexpected registration error: %v", err)
			}
		})
	}
	if _, err := svc.Register(ctx, "Duplicate", "alice@example.org", "another-strong-password"); !domain.HasCode(
		err,
		domain.ErrAlreadyExists,
	) {
		t.Fatalf("duplicate email accepted: %v", err)
	}
	for _, email := range []string{"missing@example.org", "invalid-address"} {
		if _, _, err := svc.Login(ctx, email, "wrong"); !domain.HasCode(err, domain.ErrAuth) {
			t.Fatalf("login %q: %v", email, err)
		}
	}
	if _, _, err := svc.Login(ctx, "alice@example.org", "wrong-password"); !domain.HasCode(err, domain.ErrAuth) {
		t.Fatalf("incorrect password accepted: %v", err)
	}
	token, user, err := svc.Login(ctx, "alice@example.org", "long-enough-password")
	if err != nil || token == "" {
		t.Fatalf("login valid: %v", err)
	}
	loaded, err := svc.Account(ctx, token)
	if err != nil || loaded.ID != user.ID {
		t.Fatalf("session user mismatch: %v", err)
	}
	if err := svc.Logout(ctx, strings.Repeat("n", 64)); err != nil {
		t.Fatalf("idempotent revoke: %v", err)
	}
}

func TestCommentValidationAndPublishOnlyAfterInsert(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)
	author, err := svc.Register(ctx, "Author", "owner@example.org", "long-enough-password")
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	post, err := svc.CreatePost(ctx, author.ID, "Post", "Text")
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	eventsCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	stream := svc.Events.Subscribe(eventsCtx, post.ID)
	for _, invalid := range []CommentCase{
		{"invalid email", "bad-mail", "valid body"},
		{"too many characters", "visitor@example.org", strings.Repeat("a", 2001)},
	} {
		if _, err := svc.CreateComment(ctx, domain.CommentCreate{
			PostID: post.ID, Name: "Reader", Email: invalid.email, Body: invalid.body,
		}); !domain.HasCode(err, domain.ErrInput) {
			t.Fatalf("%s: want input error, got %v", invalid.name, err)
		}
	}
	if len(stream) != 0 {
		t.Fatal("failed inserts unexpectedly published events")
	}
	for _, body := range []string{strings.Repeat("😀", 2000), "hello"} {
		result, err := svc.CreateComment(
			ctx,
			domain.CommentCreate{PostID: post.ID, Name: "Reader", Email: "visitor@example.org", Body: body},
		)
		if err != nil {
			t.Fatalf("create valid comment: %v", err)
		}
		if received := <-stream; received.ID != result.ID {
			t.Fatal("new comment event missing")
		}
	}
	forged := author.ID
	guest, err := svc.CreateComment(ctx, domain.CommentCreate{
		PostID: post.ID, UserID: &forged, Name: "Reader", Email: "visitor@example.org", Body: "Anonymous",
	})
	if err != nil || guest.UserID != nil || guest.IsPostAuthor {
		t.Fatalf("anonymous input forged account ownership: %+v, %v", guest, err)
	}
	signed, err := svc.CreateCommentAs(ctx, &author, domain.CommentCreate{
		PostID: post.ID, Name: "Fake", Email: "fake@example.org", Body: "Signed comment",
	})
	if err != nil || signed.Name != author.Name || signed.Email != author.Email || !signed.IsPostAuthor {
		t.Fatalf("account identity not authoritative: %+v, %v", signed, err)
	}
}

type CommentCase struct{ name, email, body string }
