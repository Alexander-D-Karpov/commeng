package postgres

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/jackc/pgx/v5"

	"github.com/Alexander-D-Karpov/commeng/internal/domain"
)

const commentColumns = `id,post_id,parent_id,author_name,author_email,avatar_hash,` +
	`user_id,is_post_author,thread_blocked,body,state,locked,reply_count,likes_count,created_at,edited_at`

func commentScanFields(item *domain.Comment) []any {
	return []any{
		&item.ID, &item.PostID, &item.ParentID,
		&item.Name, &item.Email, &item.AvatarHash,
		&item.UserID, &item.IsPostAuthor, &item.ThreadBlocked, &item.Body,
		&item.State, &item.Locked, &item.ReplyCount,
		&item.LikesCount, &item.CreatedAt, &item.EditedAt,
	}
}

func scanComment(row pgx.Row) (domain.Comment, error) {
	var c domain.Comment
	err := row.Scan(commentScanFields(&c)...)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Comment{}, domain.Fault(domain.ErrNotFound, "comment not found")
	}
	return c, mapPGError(err)
}
func (s *Store) Comment(ctx context.Context, id int64) (domain.Comment, error) {
	return scanComment(s.pool.QueryRow(ctx, `SELECT `+commentColumns+` FROM comments WHERE id=$1`, id))
}
func (s *Store) CreateComment(ctx context.Context, input domain.CommentCreate) (domain.Comment, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.Comment{}, err
	}
	defer func() {
		_ = tx.Rollback(context.Background())
	}()
	var enabled bool
	var postAuthorID, eventVersion int64
	var deleted bool
	if err := tx.QueryRow(ctx, `SELECT comments_enabled,author_id,deleted,event_version
		FROM posts WHERE id=$1 FOR UPDATE`, input.PostID).
		Scan(&enabled, &postAuthorID, &deleted, &eventVersion); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.Comment{}, domain.Fault(domain.ErrNotFound, "post not found")
		}
		return domain.Comment{}, err
	}
	if !enabled || deleted {
		return domain.Comment{}, domain.Fault(domain.ErrDisabled, "comments disabled")
	}
	if input.ParentID != nil {
		// Inherited policy is maintained on rare moderation, not recomputed on each reply.
		var postID int64
		var state domain.State
		var locked, blocked bool
		err := tx.QueryRow(ctx, `SELECT post_id,state,locked,thread_blocked FROM comments WHERE id=$1`, *input.ParentID).
			Scan(&postID, &state, &locked, &blocked)
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.Comment{}, domain.Fault(domain.ErrInput, "parent not found")
		}
		if err != nil {
			return domain.Comment{}, err
		}
		if postID != input.PostID {
			return domain.Comment{}, domain.Fault(domain.ErrInput, "parent belongs to another post")
		}
		if locked || blocked || state != domain.StateVisible {
			return domain.Comment{}, domain.Fault(domain.ErrDisabled, "thread locked or hidden")
		}
	}

	comment, err := scanComment(tx.QueryRow(ctx, `
        INSERT INTO comments(
         post_id,parent_id,author_name,author_email,avatar_hash,body,user_id,is_post_author
        ) VALUES ($1,$2,$3,$4,$5,$6,$7,$8) RETURNING `+commentColumns,
		input.PostID, input.ParentID, input.Name, input.Email, input.AvatarHash, input.Body,
		input.UserID, input.UserID != nil && *input.UserID == postAuthorID))
	if err != nil {
		return domain.Comment{}, err
	}
	if input.ParentID != nil {
		if _, err := tx.Exec(ctx, `UPDATE comments SET reply_count=reply_count+1 WHERE id=$1`, *input.ParentID); err != nil {
			return domain.Comment{}, err
		}
	}
	// All write effects, including the durable event and reply notification,
	// commit atomically or roll back together.
	if _, err := tx.Exec(ctx, `INSERT INTO comment_events(post_id,sequence,comment_id) VALUES($1,$2,$3)`,
		input.PostID, eventVersion+1, comment.ID); err != nil {
		return domain.Comment{}, err
	}
	if input.ParentID != nil {
		if _, err := tx.Exec(ctx, `INSERT INTO notifications(user_id,post_id,comment_id,actor_user_id,kind)
          SELECT c.user_id,$1,$2,$3,'REPLY' FROM comments c WHERE c.id=$4 AND c.user_id IS NOT NULL
            AND ($3::bigint IS NULL OR c.user_id<>$3) ON CONFLICT DO NOTHING`,
			input.PostID, comment.ID, input.UserID, *input.ParentID); err != nil {
			return domain.Comment{}, err
		}
	}
	if _, err := tx.Exec(ctx, `UPDATE posts SET comment_count=comment_count+1,
		event_version=event_version+1 WHERE id=$1`, input.PostID); err != nil {
		return domain.Comment{}, err
	}
	return comment, tx.Commit(ctx)
}

// scopeWhere uses separate root/child predicates. PostgreSQL can use
// comments_roots / comments_replies without a large OFFSET or OR condition.
func scopeWhere(parent *int64) (string, []any) {
	if parent == nil {
		return "post_id=$1 AND parent_id IS NULL", nil
	}
	return "post_id=$1 AND parent_id=$2", []any{*parent}
}
func orderSQL(order domain.Sort) (string, string, error) {
	switch order {
	case domain.SortOldest:
		return "ASC", ">", nil
	case domain.SortNewest:
		return "DESC", "<", nil
	default:
		return "", "", domain.Fault(domain.ErrInput, "invalid sort")
	}
}

type queryRows interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
}

func queryComments(
	ctx context.Context,
	db queryRows,
	req domain.CommentPageRequest,
) (domain.Page[domain.Comment], error) {
	if err := domain.ValidatePageSize(req.First); err != nil {
		return domain.Page[domain.Comment]{}, err
	}
	if req.After != nil && req.Before != nil {
		return domain.Page[domain.Comment]{}, domain.Fault(domain.ErrInput, "use after or before, not both")
	}
	order, cmp, err := orderSQL(req.Sort)
	if err != nil {
		return domain.Page[domain.Comment]{}, err
	}
	predicate, extra := scopeWhere(req.ParentID)
	args := append([]any{req.PostID}, extra...)
	reversed := req.Before != nil
	if req.After != nil {
		args = append(args, *req.After)
		predicate += fmt.Sprintf(" AND id %s $%d", cmp, len(args))
	} else if req.Before != nil {
		bound := "<"
		if req.Sort == domain.SortNewest {
			bound = ">"
		}
		args = append(args, *req.Before)
		predicate += fmt.Sprintf(" AND id %s $%d", bound, len(args))
		if order == "ASC" {
			order = "DESC"
		} else {
			order = "ASC"
		}
	}
	args = append(args, req.First+1)
	query := fmt.Sprintf("SELECT %s FROM comments WHERE %s ORDER BY id %s LIMIT $%d",
		commentColumns, predicate, order, len(args))
	rows, err := db.Query(ctx, query, args...)
	if err != nil {
		return domain.Page[domain.Comment]{}, err
	}
	defer rows.Close()
	page := domain.Page[domain.Comment]{Items: make([]domain.Comment, 0, req.First)}
	for rows.Next() {
		var comment domain.Comment
		if err := rows.Scan(commentScanFields(&comment)...); err != nil {
			return page, err
		}
		if len(page.Items) == req.First {
			if reversed {
				page.HasPrev = true
			} else {
				page.HasNext = true
			}
			break
		}
		page.Items = append(page.Items, comment)
	}
	if err := rows.Err(); err != nil {
		return page, err
	}
	if reversed {
		slices.Reverse(page.Items)
		// A before cursor means a later page exists after the returned page,
		// even if this page is empty because the cursor was stale.
		page.HasNext = req.Before != nil && len(page.Items) > 0
	} else if req.After != nil {
		page.HasPrev = len(page.Items) > 0
	}
	return page, nil
}
func (s *Store) Comments(ctx context.Context, req domain.CommentPageRequest) (domain.Page[domain.Comment], error) {
	if _, err := s.Post(ctx, req.PostID); err != nil {
		return domain.Page[domain.Comment]{}, err
	}
	return queryComments(ctx, s.pool, req)
}

// CommentsBatch runs up to four SQL statements, regardless of the number of
// parent collections. Every LATERAL subquery independently uses LIMIT first+1.

func (s *Store) Ancestors(ctx context.Context, id int64, limit int) ([]domain.Comment, error) {
	if limit < 0 || limit > 100 {
		return nil, domain.Fault(domain.ErrInput, "ancestor limit must be 0–100")
	}
	if limit == 0 {
		return []domain.Comment{}, nil
	}
	rows, err := s.pool.Query(ctx, `
        WITH RECURSIVE ancestor_chain AS (
            SELECT p.*, 1 AS depth FROM comments target JOIN comments p ON p.id=target.parent_id
            WHERE target.id=$1
            UNION ALL
            SELECT p.*, child.depth+1 FROM comments p
            JOIN ancestor_chain child ON p.id=child.parent_id
            WHERE child.depth<$2
        )
        SELECT `+commentColumns+` FROM ancestor_chain ORDER BY depth DESC`, id, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ancestors := make([]domain.Comment, 0)
	for rows.Next() {
		var c domain.Comment
		if err := rows.Scan(commentScanFields(&c)...); err != nil {
			return nil, err
		}
		ancestors = append(ancestors, c)
	}
	return ancestors, rows.Err()
}

// AncestorsPage avoids unbounded GraphQL payloads for extremely deep threads.
func (s *Store) AncestorsPage(
	ctx context.Context,
	targetID int64,
	before *int64,
	first int,
) (domain.AncestorPage, error) {
	if err := domain.ValidatePageSize(first); err != nil {
		return domain.AncestorPage{}, err
	}
	if _, err := s.Comment(ctx, targetID); err != nil {
		return domain.AncestorPage{}, err
	}
	anchorID := targetID
	if before != nil {
		anchorID = *before
		var valid bool
		err := s.pool.QueryRow(ctx, `
   WITH RECURSIVE path(id,parent_id) AS (
    SELECT id,parent_id FROM comments WHERE id=$1
    UNION ALL
    SELECT p.id,p.parent_id FROM comments p JOIN path c ON p.id=c.parent_id
   )
   SELECT EXISTS(SELECT 1 FROM path WHERE id=$2 AND id<>$1)`, targetID, anchorID).Scan(&valid)
		if err != nil {
			return domain.AncestorPage{}, err
		}
		if !valid {
			return domain.AncestorPage{}, domain.Fault(domain.ErrInput, "before must be an ancestor of comment")
		}
	}
	rows, err := s.pool.Query(ctx, `
  WITH RECURSIVE ancestors AS (
   SELECT p.*, 1 AS level FROM comments c JOIN comments p ON p.id=c.parent_id
   WHERE c.id=$1
   UNION ALL
   SELECT p.*, child.level+1 FROM comments p JOIN ancestors child ON p.id=child.parent_id
   WHERE child.level < $2
  )
  SELECT `+commentColumns+`,level FROM ancestors ORDER BY level ASC`, anchorID, first+1)
	if err != nil {
		return domain.AncestorPage{}, err
	}
	defer rows.Close()
	page := domain.AncestorPage{Items: make([]domain.Comment, 0, first)}
	for rows.Next() {
		var c domain.Comment
		var level int
		fields := append(commentScanFields(&c), &level)
		if err := rows.Scan(fields...); err != nil {
			return domain.AncestorPage{}, err
		}
		if len(page.Items) == first {
			lastID := page.Items[len(page.Items)-1].ID
			page.NextBefore = &lastID
			break
		}
		page.Items = append(page.Items, c)
	}
	return page, rows.Err()
}
