package memory

import (
	"context"
	"time"

	"github.com/Alexander-D-Karpov/commeng/internal/domain"
)

func (s *Store) CreatePost(ctx context.Context, authorID int64, title, body string) (domain.Post, error) {
	if err := ctx.Err(); err != nil {
		return domain.Post{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.users[authorID]; !ok {
		return domain.Post{}, domain.Fault(domain.ErrAuth, "login required")
	}
	s.nextPost++
	post := domain.Post{
		ID: s.nextPost, AuthorID: authorID, Title: title, Body: body,
		CommentsEnabled: true, TreeVersion: 1, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	s.posts[post.ID] = post
	s.postIDs = append(s.postIDs, post.ID)
	return post, nil
}
func (s *Store) Post(ctx context.Context, id int64) (domain.Post, error) {
	if err := ctx.Err(); err != nil {
		return domain.Post{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	post, ok := s.posts[id]
	if !ok {
		return domain.Post{}, domain.Fault(domain.ErrNotFound, "post not found")
	}
	return post, nil
}
func (s *Store) Posts(ctx context.Context, after *int64, first int) (domain.Page[domain.Post], error) {
	if err := domain.ValidatePageSize(first); err != nil {
		return domain.Page[domain.Post]{}, err
	}
	if err := ctx.Err(); err != nil {
		return domain.Page[domain.Post]{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := domain.Page[domain.Post]{Items: make([]domain.Post, 0, first)}
	index := len(s.postIDs) - 1
	if after != nil {
		index = sortSearchGreaterEqual(s.postIDs, *after) - 1
	}
	for ; index >= 0; index-- {
		post := s.posts[s.postIDs[index]]
		if post.Deleted {
			continue
		}
		if len(result.Items) == first {
			result.HasNext = true
			break
		}
		result.Items = append(result.Items, post)
	}
	return result, nil
}
func (s *Store) SetCommentsEnabled(ctx context.Context, postID, authorID int64, enabled bool) (domain.Post, error) {
	if err := ctx.Err(); err != nil {
		return domain.Post{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	post, ok := s.posts[postID]
	if !ok {
		return domain.Post{}, domain.Fault(domain.ErrNotFound, "post not found")
	}
	if post.AuthorID != authorID {
		return domain.Post{}, domain.Fault(domain.ErrForbidden, "only post author can change comments")
	}
	if post.Deleted {
		return domain.Post{}, domain.Fault(domain.ErrConflict, "post was deleted")
	}
	if post.CommentsEnabled != enabled {
		post.CommentsEnabled = enabled
		post.TreeVersion++
	}
	s.posts[postID] = post
	return post, nil
}
