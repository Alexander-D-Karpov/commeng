package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/Alexander-D-Karpov/commeng/internal/domain"
	"github.com/Alexander-D-Karpov/commeng/internal/live"
	"github.com/Alexander-D-Karpov/commeng/internal/service"
)

// seed intentionally supports only PostgreSQL; an in-memory seed command would
// vanish as soon as the short-lived CLI process exits.
func seed(ctx context.Context, args []string) error {
	if os.Getenv("APP_ENV") == "production" {
		return fmt.Errorf("seeding is forbidden in production")
	}
	flags := flag.NewFlagSet("seed", flag.ContinueOnError)
	postCount := flags.Int("posts", 10, "number of demo posts")
	commentsPerPost := flags.Int("comments", 50, "comments per post")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *postCount < 1 || *postCount > 100 || *commentsPerPost < 0 || *commentsPerPost > 1000 {
		return fmt.Errorf("seed limits: posts=1..100 comments=0..1000")
	}
	store, err := openStore(ctx, "postgres")
	if err != nil {
		return err
	}
	defer store.Close()
	svc := service.New(store, live.New(), environment("PUBLIC_BASE_URL", "http://localhost:8080"))
	user, err := svc.Register(
		ctx,
		"Demo author",
		fmt.Sprintf("demo-%d@invalid.example", os.Getpid()),
		"unused-demo-password-123",
	)
	if err != nil {
		return fmt.Errorf("create demo user: %w", err)
	}
	for postIndex := 0; postIndex < *postCount; postIndex++ {
		post, err := svc.CreatePost(ctx, user.ID, fmt.Sprintf("Demo post %d", postIndex+1), "Seeded test discussion")
		if err != nil {
			return fmt.Errorf("create demo post: %w", err)
		}
		ids := make([]int64, 0, *commentsPerPost)
		for commentIndex := 0; commentIndex < *commentsPerPost; commentIndex++ {
			var parentID *int64
			if len(ids) > 0 && commentIndex%3 != 0 {
				// Repeatable branching fixtures are easier to debug than random seed data.
				id := ids[(commentIndex*7)%len(ids)]
				parentID = &id
			}
			comment, err := svc.CreateComment(ctx, domain.CommentCreate{
				PostID: post.ID, ParentID: parentID,
				Name: "Demo reader", Email: "reader@invalid.example",
				Body: fmt.Sprintf("Seed comment %d", commentIndex+1),
			})
			if err != nil {
				return fmt.Errorf("create demo comment: %w", err)
			}
			ids = append(ids, comment.ID)
		}
	}
	fmt.Printf("seeded %d posts and %d comments\n", *postCount, (*postCount)*(*commentsPerPost))
	return nil
}
