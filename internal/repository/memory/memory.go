// Package memory stores the in-process representation of posts and comments.
// All mutations are atomic under the store lock; no network I/O takes place inside it.
package memory

import (
	"context"
	"sync"
	"time"

	"github.com/Alexander-D-Karpov/commeng/internal/domain"
)

type scope struct{ postID, parentID int64 }
type session struct {
	userID  int64
	expires time.Time
}

type Store struct {
	mu                                                sync.RWMutex
	nextUser, nextPost, nextComment, nextNotification int64
	users                                             map[int64]domain.User
	usersByEmail                                      map[string]int64
	sessions                                          map[string]session
	posts                                             map[int64]domain.Post
	postIDs                                           []int64
	comments                                          map[int64]domain.Comment
	children                                          map[scope][]int64 // sorted ascending by generated ID
	votes                                             map[int64]map[string]struct{}
	notifications                                     map[int64]domain.Notification
	notificationsByUser                               map[int64][]int64
	commentEvents                                     map[int64][]domain.CommentEvent
}

func New() *Store {
	return &Store{
		users: make(map[int64]domain.User), usersByEmail: make(map[string]int64),
		sessions: make(map[string]session), posts: make(map[int64]domain.Post),
		comments: make(map[int64]domain.Comment), children: make(map[scope][]int64),
		votes: make(map[int64]map[string]struct{}),
		notifications: make(
			map[int64]domain.Notification,
		), notificationsByUser: make(map[int64][]int64), commentEvents: make(map[int64][]domain.CommentEvent),
	}
}
func (*Store) Close() {}
func (*Store) Ping(ctx context.Context) error {
	return ctx.Err()
}
