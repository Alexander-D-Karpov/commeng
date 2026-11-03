package memory

import (
	"context"
	"time"

	"github.com/Alexander-D-Karpov/commeng/internal/domain"
)

func (s *Store) Notifications(
	ctx context.Context,
	userID int64,
	after *int64,
	first int,
	unreadOnly bool,
) (domain.Page[domain.Notification], error) {
	if err := domain.ValidatePageSize(first); err != nil {
		return domain.Page[domain.Notification]{}, err
	}
	if err := ctx.Err(); err != nil {
		return domain.Page[domain.Notification]{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	ids := s.notificationsByUser[userID]
	page := domain.Page[domain.Notification]{Items: make([]domain.Notification, 0, first)}
	start := len(ids) - 1
	if after != nil {
		start = sortSearchGreaterEqual(ids, *after) - 1
	}
	for i := start; i >= 0; i-- {
		n := s.notifications[ids[i]]
		if unreadOnly && n.ReadAt != nil {
			continue
		}
		if len(page.Items) == first {
			page.HasNext = true
			break
		}
		if n.ActorUserID != nil {
			actor := *n.ActorUserID
			n.ActorUserID = &actor
		}
		if n.ReadAt != nil {
			date := *n.ReadAt
			n.ReadAt = &date
		}
		page.Items = append(page.Items, n)
	}
	page.HasPrev = after != nil && len(page.Items) > 0
	return page, nil
}

func (s *Store) MarkNotificationsRead(ctx context.Context, userID int64, ids []int64) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	changed := 0
	now := time.Now().UTC()
	for _, id := range ids {
		n, ok := s.notifications[id]
		if !ok || n.UserID != userID || n.ReadAt != nil {
			continue
		}
		n.ReadAt = &now
		s.notifications[id] = n
		changed++
	}
	return changed, nil
}
