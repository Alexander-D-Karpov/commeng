//go:build integration

package integration

import (
	"context"
	"testing"
	"time"

	"github.com/Alexander-D-Karpov/commeng/internal/domain"
	"github.com/Alexander-D-Karpov/commeng/internal/repository/postgres"
)

func TestPostgresSessionCleanupDoesNotRunOnLogin(t *testing.T) {
	ctx := context.Background()
	store := freshPostgres(t).(*postgres.Store)
	user, err := store.CreateUser(
		ctx,
		domain.User{Name: "Session", Email: "session@example.org", PasswordHash: "test", Role: domain.RoleUser},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SaveSession(ctx, "expired", user.ID, time.Now().Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveSession(ctx, "live", user.ID, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SessionUser(ctx, "expired"); err == nil {
		t.Fatal("expired session was accepted")
	}
	removed, err := store.PurgeExpiredSessions(ctx, 1)
	if err != nil || removed != 1 {
		t.Fatalf("removed %d err=%v", removed, err)
	}
	if _, err := store.SessionUser(ctx, "live"); err != nil {
		t.Fatalf("valid session lost: %v", err)
	}
}
