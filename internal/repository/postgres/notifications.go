package postgres

import (
	"context"

	"github.com/Alexander-D-Karpov/commeng/internal/domain"
)

const notificationColumns = `id,user_id,post_id,comment_id,actor_user_id,kind,created_at,read_at`

func notificationFields(n *domain.Notification) []any {
	return []any{&n.ID, &n.UserID, &n.PostID, &n.CommentID, &n.ActorUserID, &n.Kind, &n.CreatedAt, &n.ReadAt}
}

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
	rows, err := s.pool.Query(ctx, `SELECT `+notificationColumns+` FROM notifications
      WHERE user_id=$1 AND ($2::bigint IS NULL OR id<$2)
       AND (NOT $3::boolean OR read_at IS NULL)
      ORDER BY id DESC LIMIT $4`, userID, after, unreadOnly, first+1)
	if err != nil {
		return domain.Page[domain.Notification]{}, err
	}
	defer rows.Close()
	page := domain.Page[domain.Notification]{Items: make([]domain.Notification, 0, first)}
	for rows.Next() {
		var n domain.Notification
		if err := rows.Scan(notificationFields(&n)...); err != nil {
			return page, err
		}
		if len(page.Items) == first {
			page.HasNext = true
			break
		}
		page.Items = append(page.Items, n)
	}
	page.HasPrev = after != nil && len(page.Items) > 0
	return page, rows.Err()
}
func (s *Store) MarkNotificationsRead(ctx context.Context, userID int64, ids []int64) (int, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	tag, err := s.pool.Exec(ctx, `UPDATE notifications SET read_at=now()
       WHERE user_id=$1 AND id=ANY($2::bigint[]) AND read_at IS NULL`, userID, ids)
	if err != nil {
		return 0, err
	}
	return int(tag.RowsAffected()), nil
}
