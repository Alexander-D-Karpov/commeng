// Package repository defines the persistence operations required by the service
// and GraphQL resolvers. Both memory and PostgreSQL implementations obey the
// same contracts, including pagination order and atomic writes.
package repository

import (
	"context"
	"time"

	"github.com/Alexander-D-Karpov/commeng/internal/domain"
)

type Users interface {
	CreateUser(context.Context, domain.User) (domain.User, error)
	UserByEmail(context.Context, string) (domain.User, error)
	UserByID(context.Context, int64) (domain.User, error)
	Users(context.Context, []int64) (map[int64]domain.User, error)
	SaveSession(context.Context, string, int64, time.Time) error
	DeleteSession(context.Context, string) error
	SessionUser(context.Context, string) (domain.User, error)
}

type Posts interface {
	CreatePost(context.Context, int64, string, string) (domain.Post, error)
	Post(context.Context, int64) (domain.Post, error)
	Posts(context.Context, *int64, int) (domain.Page[domain.Post], error)
	SetCommentsEnabled(context.Context, int64, int64, bool) (domain.Post, error)
	EditPost(context.Context, int64, int64, string, string) (domain.Post, error)
	DeletePost(context.Context, int64, int64) (domain.Post, error)
	SearchPosts(context.Context, string, *int64, int) (domain.Page[domain.Post], error)
}

type Comments interface {
	CreateComment(context.Context, domain.CommentCreate) (domain.Comment, error)
	Comment(context.Context, int64) (domain.Comment, error)
	Comments(context.Context, domain.CommentPageRequest) (domain.Page[domain.Comment], error)
	CommentsBatch(context.Context, []domain.CommentPageRequest) ([]domain.Page[domain.Comment], error)
	LocateComment(context.Context, int64, int, domain.Sort) (domain.LocatedComment, error)
	Ancestors(context.Context, int64, int) ([]domain.Comment, error)
	AncestorsPage(context.Context, int64, *int64, int) (domain.AncestorPage, error)
	EditComment(context.Context, int64, int64, string) (domain.Comment, error)
	DeleteComment(context.Context, int64, int64) (domain.Comment, error)
	SearchComments(context.Context, string, *int64, *int64, int) (domain.Page[domain.Comment], error)
}

type Likes interface {
	HasLikes(context.Context, string, []int64) (map[int64]bool, error)
	SetLike(context.Context, int64, string, bool) (domain.Comment, error)
}

type Moderation interface {
	Moderate(context.Context, domain.Moderation) (domain.Comment, error)
}

type Events interface {
	// Head is read before subscribing, and events after this sequence can be
	// recovered without a race when a new replica listener starts.
	CommentEventHead(context.Context, int64) (int64, error)
	CommentEvents(context.Context, int64, int64, int) (domain.Page[domain.CommentEvent], error)
}

type Notifications interface {
	Notifications(context.Context, int64, *int64, int, bool) (domain.Page[domain.Notification], error)
	MarkNotificationsRead(context.Context, int64, []int64) (int, error)
}

type Repository interface {
	Users
	Posts
	Comments
	Likes
	Moderation
	Notifications
	Events
	Ping(context.Context) error
	Close()
}
