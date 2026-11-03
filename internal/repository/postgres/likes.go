package postgres

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"

	"github.com/Alexander-D-Karpov/commeng/internal/domain"
)

func (s *Store) HasLikes(ctx context.Context, actor string, ids []int64) (map[int64]bool, error) {
	result := make(map[int64]bool, len(ids))
	if actor == "" || len(ids) == 0 {
		return result, nil
	}
	rows, err := s.pool.Query(ctx,
		`SELECT comment_id FROM comment_likes WHERE actor_key=$1 AND comment_id=ANY($2::bigint[])`,
		actor, ids,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		result[id] = true
	}
	return result, rows.Err()
}

// SetLike relies on the unique (comment_id,actor_key) key for idempotency.
// Only a real insertion/removal updates the counter. In particular, it does
// not lock the comment row before the write, avoiding needless serialization.
func (s *Store) SetLike(ctx context.Context, id int64, actor string, enabled bool) (domain.Comment, error) {
	if actor == "" {
		return domain.Comment{}, domain.Fault(domain.ErrAuth, "visitor session or login required")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.Comment{}, err
	}
	defer func() { _ = tx.Rollback(context.Background()) }()

	var modifiedID int64
	if enabled {
		// Share-lock the post so deletion/moderation cannot change visibility
		// before this transaction commits. FOR SHARE permits concurrent likes.
		var deleted bool
		err = tx.QueryRow(ctx, `
			SELECT p.deleted FROM posts p
			JOIN comments c ON c.post_id=p.id
			WHERE c.id=$1 FOR SHARE OF p`, id,
		).Scan(&deleted)
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.Comment{}, domain.Fault(domain.ErrNotFound, "comment not found")
		}
		if err != nil {
			return domain.Comment{}, err
		}
		if deleted {
			return domain.Comment{}, domain.Fault(domain.ErrInput, "cannot like a deleted post")
		}
		// Do not insert on an invisible descendant. Recheck on UPDATE below.
		err = tx.QueryRow(ctx, `
			INSERT INTO comment_likes(comment_id,actor_key)
			SELECT id,$2 FROM comments
			WHERE id=$1 AND state='VISIBLE' AND NOT thread_blocked
			ON CONFLICT DO NOTHING RETURNING comment_id`, id, actor,
		).Scan(&modifiedID)
	} else {
		err = tx.QueryRow(ctx, `
			DELETE FROM comment_likes WHERE comment_id=$1 AND actor_key=$2
			RETURNING comment_id`, id, actor,
		).Scan(&modifiedID)
	}
	changed := err == nil
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return domain.Comment{}, err
	}

	var comment domain.Comment
	if changed {
		delta := int64(-1)
		condition := ""
		if enabled {
			delta = 1
			condition = " AND state='VISIBLE' AND NOT thread_blocked"
		}
		comment, err = scanComment(tx.QueryRow(ctx,
			`UPDATE comments SET likes_count=likes_count+$2 WHERE id=$1`+condition+
				` RETURNING `+commentColumns, id, delta,
		))
		if domain.HasCode(err, domain.ErrNotFound) && enabled {
			return domain.Comment{}, domain.Fault(domain.ErrInput, "cannot like hidden comment")
		}
	} else {
		comment, err = scanComment(tx.QueryRow(ctx, `SELECT `+commentColumns+` FROM comments WHERE id=$1`, id))
		if err == nil && enabled && (comment.State != domain.StateVisible || comment.ThreadBlocked) {
			return domain.Comment{}, domain.Fault(domain.ErrInput, "cannot like hidden comment")
		}
	}
	if err != nil {
		return domain.Comment{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.Comment{}, err
	}
	return comment, nil
}
