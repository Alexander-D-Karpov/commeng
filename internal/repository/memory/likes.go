package memory

import (
	"context"

	"github.com/Alexander-D-Karpov/commeng/internal/domain"
)

func (s *Store) HasLikes(ctx context.Context, actor string, ids []int64) (map[int64]bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make(map[int64]bool, len(ids))
	for _, id := range ids {
		_, result[id] = s.votes[id][actor]
	}
	return result, nil
}
func (s *Store) SetLike(ctx context.Context, id int64, actor string, enabled bool) (domain.Comment, error) {
	if actor == "" {
		return domain.Comment{}, domain.Fault(domain.ErrAuth, "visitor session or login required")
	}
	if err := ctx.Err(); err != nil {
		return domain.Comment{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	item, ok := s.comments[id]
	if !ok {
		return domain.Comment{}, domain.Fault(domain.ErrNotFound, "comment not found")
	}
	if enabled && (item.State != domain.StateVisible || item.ThreadBlocked || s.posts[item.PostID].Deleted) {
		return domain.Comment{}, domain.Fault(domain.ErrInput, "cannot like a hidden comment or a deleted post")
	}
	if s.votes[id] == nil {
		s.votes[id] = make(map[string]struct{})
	}
	_, exists := s.votes[id][actor]
	if enabled && !exists {
		s.votes[id][actor] = struct{}{}
		item.LikesCount++
	}
	if !enabled && exists {
		delete(s.votes[id], actor)
		item.LikesCount--
	}
	s.comments[id] = item
	return cloneComment(item), nil
}
