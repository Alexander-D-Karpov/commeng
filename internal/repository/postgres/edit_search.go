package postgres

import (
	"context"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/Alexander-D-Karpov/commeng/internal/domain"
)

func (s *Store) EditPost(ctx context.Context, postID, actorID int64, title, body string) (domain.Post, error) {
	post, err := scanPost(s.pool.QueryRow(ctx, `
        UPDATE posts SET title=$3,body=$4,updated_at=now()
        WHERE id=$1 AND author_id=$2 AND NOT deleted RETURNING `+postColumns,
		postID, actorID, title, body))
	if domain.HasCode(err, domain.ErrNotFound) {
		return domain.Post{}, s.postAccessError(ctx, postID, actorID)
	}
	return post, err
}
func (s *Store) DeletePost(ctx context.Context, postID, actorID int64) (domain.Post, error) {
	post, err := scanPost(s.pool.QueryRow(ctx, `
        UPDATE posts SET deleted=true,comments_enabled=false,title='[deleted]',body='[deleted]',
           updated_at=now(),tree_version=tree_version+1
        WHERE id=$1 AND author_id=$2 AND NOT deleted RETURNING `+postColumns, postID, actorID))
	if domain.HasCode(err, domain.ErrNotFound) {
		return domain.Post{}, s.postAccessError(ctx, postID, actorID)
	}
	return post, err
}
func (s *Store) postAccessError(ctx context.Context, id, actorID int64) error {
	post, err := s.Post(ctx, id)
	if err != nil {
		return err
	}
	if post.AuthorID != actorID {
		return domain.Fault(domain.ErrForbidden, "only author can modify post")
	}
	return domain.Fault(domain.ErrConflict, "post was deleted")
}

func (s *Store) SearchPosts(
	ctx context.Context,
	query string,
	after *int64,
	first int,
) (domain.Page[domain.Post], error) {
	if err := domain.ValidatePageSize(first); err != nil {
		return domain.Page[domain.Post]{}, err
	}
	rows, err := s.pool.Query(ctx, `SELECT `+postColumns+` FROM posts
        WHERE NOT deleted AND to_tsvector('simple',coalesce(title,'') || ' ' || coalesce(body,''))
          @@ plainto_tsquery('simple',$1)
          AND ($2::bigint IS NULL OR id<$2) ORDER BY id DESC LIMIT $3`, query, after, first+1)
	if err != nil {
		return domain.Page[domain.Post]{}, err
	}
	defer rows.Close()
	result := domain.Page[domain.Post]{Items: make([]domain.Post, 0, first)}
	for rows.Next() {
		var item domain.Post
		if err := rows.Scan(postScanFields(&item)...); err != nil {
			return result, err
		}
		if len(result.Items) == first {
			result.HasNext = true
			break
		}
		result.Items = append(result.Items, item)
	}
	result.HasPrev = after != nil && len(result.Items) > 0
	return result, rows.Err()
}

func (s *Store) SearchComments(
	ctx context.Context,
	query string,
	postID, after *int64,
	first int,
) (domain.Page[domain.Comment], error) {
	if err := domain.ValidatePageSize(first); err != nil {
		return domain.Page[domain.Comment]{}, err
	}
	rows, err := s.pool.Query(ctx, `SELECT `+commentColumns+` FROM comments c
        WHERE state='VISIBLE' AND NOT thread_blocked AND
          ($2::bigint IS NULL OR post_id=$2) AND ($3::bigint IS NULL OR id<$3)
          AND EXISTS (SELECT 1 FROM posts p WHERE p.id=c.post_id AND NOT p.deleted)
          AND to_tsvector('simple',coalesce(body,'')) @@ plainto_tsquery('simple',$1)
        ORDER BY id DESC LIMIT $4`, query, postID, after, first+1)
	if err != nil {
		return domain.Page[domain.Comment]{}, err
	}
	defer rows.Close()
	result := domain.Page[domain.Comment]{Items: make([]domain.Comment, 0, first)}
	for rows.Next() {
		var item domain.Comment
		if err := rows.Scan(commentScanFields(&item)...); err != nil {
			return result, err
		}
		if len(result.Items) == first {
			result.HasNext = true
			break
		}
		result.Items = append(result.Items, item)
	}
	result.HasPrev = after != nil && len(result.Items) > 0
	return result, rows.Err()
}
func (s *Store) EditComment(ctx context.Context, commentID, actorID int64, body string) (domain.Comment, error) {
	comment, err := scanComment(s.pool.QueryRow(ctx, `UPDATE comments SET body=$3,edited_at=now()
      WHERE id=$1 AND user_id=$2 AND state='VISIBLE'
        AND EXISTS (SELECT 1 FROM posts p WHERE p.id=comments.post_id AND NOT p.deleted)
        RETURNING `+commentColumns, commentID, actorID, body))
	if domain.HasCode(err, domain.ErrNotFound) {
		return domain.Comment{}, s.commentAccessError(ctx, commentID, actorID)
	}
	return comment, err
}
func (s *Store) DeleteComment(ctx context.Context, commentID, actorID int64) (domain.Comment, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.Comment{}, err
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	var postID int64
	err = tx.QueryRow(ctx, `SELECT post_id FROM comments WHERE id=$1`, commentID).Scan(&postID)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Comment{}, domain.Fault(domain.ErrNotFound, "comment not found")
	}
	if err != nil {
		return domain.Comment{}, err
	}
	var lockedID int64
	if err = tx.QueryRow(ctx, `SELECT id FROM posts WHERE id=$1 FOR UPDATE`, postID).Scan(&lockedID); err != nil {
		return domain.Comment{}, err
	}
	comment, err := scanComment(tx.QueryRow(ctx, `UPDATE comments SET
       state='DELETED',body='[deleted]',author_name='[deleted]',
       author_email='deleted@invalid.example',avatar_hash=$3,user_id=NULL,edited_at=now()
       WHERE id=$1 AND user_id=$2 AND state<>'DELETED' RETURNING `+commentColumns,
		commentID, actorID, strings.Repeat("0", 64)))
	if err != nil {
		if domain.HasCode(err, domain.ErrNotFound) {
			return domain.Comment{}, s.commentAccessError(ctx, commentID, actorID)
		}
		return domain.Comment{}, err
	}
	if _, err = tx.Exec(ctx, `UPDATE posts SET tree_version=tree_version+1 WHERE id=$1`, postID); err != nil {
		return domain.Comment{}, err
	}
	if err = refreshThreadFlags(ctx, tx, commentID); err != nil {
		return domain.Comment{}, err
	}
	return comment, tx.Commit(ctx)
}
func (s *Store) commentAccessError(ctx context.Context, id, actorID int64) error {
	c, err := s.Comment(ctx, id)
	if err != nil {
		return err
	}
	if c.UserID == nil || *c.UserID != actorID {
		return domain.Fault(domain.ErrForbidden, "only authenticated comment author can modify")
	}
	return domain.Fault(domain.ErrConflict, "comment was deleted or hidden")
}
