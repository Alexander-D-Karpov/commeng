//go:build integration

package integration

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Alexander-D-Karpov/commeng/internal/domain"
	"github.com/Alexander-D-Karpov/commeng/internal/live"
	"github.com/Alexander-D-Karpov/commeng/internal/repository/postgres"
)

func TestPostgresFollowerAcrossReplicasAndDurableReplay(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	storeGeneric, dsn := postgresFixture(t)
	store, ok := storeGeneric.(*postgres.Store)
	if !ok {
		t.Fatal("expected PostgreSQL")
	}
	owner, err := store.CreateUser(
		ctx,
		domain.User{Name: "Owner", Email: "outbox-owner@example.org", PasswordHash: "test", Role: domain.RoleUser},
	)
	if err != nil {
		t.Fatal(err)
	}
	post, err := store.CreatePost(ctx, owner.ID, "Realtime", "Event test")
	if err != nil {
		t.Fatal(err)
	}
	// A second independent pool models a separate running API process.
	store2, err := postgres.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { cancel(); store2.Close() }()
	broker1, broker2 := live.New(), live.New()
	follower1, follower2 := postgres.NewFollower(store, broker1), postgres.NewFollower(store2, broker2)
	go follower1.Run(ctx)
	go follower2.Run(ctx)
	subCtx, stop := context.WithCancel(ctx)
	defer stop()
	head, err := store.CommentEventHead(ctx, post.ID)
	if err != nil {
		t.Fatal(err)
	}
	ch1 := broker1.Subscribe(subCtx, post.ID)
	ch2 := broker2.Subscribe(subCtx, post.ID)
	follower1.RegisterTopic(post.ID, head)
	follower2.RegisterTopic(post.ID, head)
	created, err := store.CreateComment(ctx, domain.CommentCreate{
		PostID: post.ID, Name: "Visitor", Email: "guest@example.org",
		AvatarHash: strings.Repeat("0", 64), Body: "Distributed event",
	})
	if err != nil {
		t.Fatal(err)
	}
	for number, channel := range []<-chan domain.Comment{ch1, ch2} {
		select {
		case received, ok := <-channel:
			if !ok || received.ID != created.ID {
				t.Fatalf("replica %d received %+v, ok=%v", number, received, ok)
			}
		case <-ctx.Done():
			t.Fatalf("replica %d missed committed event: %v", number, ctx.Err())
		}
	}
	replay, err := store.CommentEvents(ctx, post.ID, 0, 20)
	if err != nil || len(replay.Items) != 1 || replay.Items[0].Comment.ID != created.ID {
		t.Fatalf("durable event replay failed: %+v %v", replay, err)
	}
}
