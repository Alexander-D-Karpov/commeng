package memory

import (
	"context"

	"github.com/Alexander-D-Karpov/commeng/internal/domain"
)

func (s *Store) CommentEventHead(ctx context.Context, postID int64) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	post, ok := s.posts[postID]
	if !ok {
		return 0, domain.Fault(domain.ErrNotFound, "post not found")
	}
	return post.EventVersion, nil
}

func (s *Store) CommentEvents(
	ctx context.Context,
	postID, after int64,
	first int,
) (domain.Page[domain.CommentEvent], error) {
	if err := domain.ValidatePageSize(first); err != nil {
		return domain.Page[domain.CommentEvent]{}, err
	}
	if err := ctx.Err(); err != nil {
		return domain.Page[domain.CommentEvent]{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if _, ok := s.posts[postID]; !ok {
		return domain.Page[domain.CommentEvent]{}, domain.Fault(domain.ErrNotFound, "post not found")
	}
	events := s.commentEvents[postID]
	page := domain.Page[domain.CommentEvent]{Items: make([]domain.CommentEvent, 0, first)}
	for _, event := range events {
		if event.Sequence <= after {
			continue
		}
		if len(page.Items) == first {
			page.HasNext = true
			break
		}
		event.Comment = cloneComment(s.comments[event.Comment.ID])
		page.Items = append(page.Items, event)
	}
	page.HasPrev = after > 0 && len(page.Items) > 0
	return page, nil
}
