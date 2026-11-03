package postgres

import (
	"context"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/Alexander-D-Karpov/commeng/internal/domain"
)

func (s *Store) CommentEventHead(ctx context.Context, postID int64) (int64, error) {
	var seq int64
	err := s.pool.QueryRow(ctx, `SELECT event_version FROM posts WHERE id=$1`, postID).Scan(&seq)
	if err == pgx.ErrNoRows {
		return 0, domain.Fault(domain.ErrNotFound, "post not found")
	}
	return seq, err
}

func (s *Store) CommentEvents(
	ctx context.Context,
	postID, after int64,
	first int,
) (domain.Page[domain.CommentEvent], error) {
	if err := domain.ValidatePageSize(first); err != nil {
		return domain.Page[domain.CommentEvent]{}, err
	}
	head, err := s.CommentEventHead(ctx, postID)
	if err != nil {
		return domain.Page[domain.CommentEvent]{}, err
	}
	rows, err := s.pool.Query(
		ctx,
		`SELECT e.sequence,e.created_at,`+strings.ReplaceAll("c."+commentColumns, ",", ",c.")+`
      FROM comment_events e JOIN comments c ON c.id=e.comment_id
      WHERE e.post_id=$1 AND e.sequence>$2 ORDER BY e.sequence ASC LIMIT $3`,
		postID,
		after,
		first+1,
	)
	if err != nil {
		return domain.Page[domain.CommentEvent]{}, err
	}
	defer rows.Close()
	page := domain.Page[domain.CommentEvent]{Items: make([]domain.CommentEvent, 0, first)}
	last := after
	for rows.Next() {
		var event domain.CommentEvent
		event.PostID = postID
		fields := append([]any{&event.Sequence, &event.CreatedAt}, commentScanFields(&event.Comment)...)
		if err := rows.Scan(fields...); err != nil {
			return page, err
		}
		// Retention may remove events out of sequence (or between the head
		// lookup and this query), so checking only the oldest event is unsafe.
		// An initial request may start at the first retained event; once a
		// cursor or the first event is present, every sequence must be adjacent.
		if (after > 0 || len(page.Items) > 0) && event.Sequence != last+1 {
			return domain.Page[domain.CommentEvent]{}, eventHistoryExpired()
		}
		if len(page.Items) == first {
			page.HasNext = true
			break
		}
		page.Items = append(page.Items, event)
		last = event.Sequence
	}
	if err := rows.Err(); err != nil {
		return page, err
	}
	if !page.HasNext && (after > 0 || len(page.Items) > 0) && last < head {
		return domain.Page[domain.CommentEvent]{}, eventHistoryExpired()
	}
	page.HasPrev = after > 0 && len(page.Items) > 0
	return page, nil
}

func eventHistoryExpired() error {
	return domain.Fault(domain.ErrHistoryExpired,
		"comment event history expired; reload the post and reset the event cursor")
}
