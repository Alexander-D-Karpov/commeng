package postgres

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"

	"github.com/Alexander-D-Karpov/commeng/internal/domain"
)

const postColumns = `id,author_id,title,body,comments_enabled,comment_count,` +
	`tree_version,created_at,updated_at,deleted,event_version`

func postScanFields(post *domain.Post) []any {
	return []any{
		&post.ID, &post.AuthorID, &post.Title, &post.Body,
		&post.CommentsEnabled, &post.CommentCount,
		&post.TreeVersion, &post.CreatedAt, &post.UpdatedAt, &post.Deleted, &post.EventVersion,
	}
}

func scanPost(row pgx.Row) (domain.Post, error) {
	var post domain.Post
	err := row.Scan(postScanFields(&post)...)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Post{}, domain.Fault(domain.ErrNotFound, "post not found")
	}
	return post, mapPGError(err)
}
func (s *Store) CreatePost(ctx context.Context, authorID int64, title, body string) (domain.Post, error) {
	return scanPost(s.pool.QueryRow(ctx, `
        INSERT INTO posts(author_id,title,body) VALUES($1,$2,$3)
        RETURNING `+postColumns, authorID, title, body))
}
func (s *Store) Post(ctx context.Context, id int64) (domain.Post, error) {
	return scanPost(s.pool.QueryRow(ctx, `SELECT `+postColumns+` FROM posts WHERE id=$1`, id))
}
func (s *Store) Posts(ctx context.Context, after *int64, first int) (domain.Page[domain.Post], error) {
	if err := domain.ValidatePageSize(first); err != nil {
		return domain.Page[domain.Post]{}, err
	}
	query := `SELECT ` + postColumns + ` FROM posts WHERE NOT deleted ORDER BY id DESC LIMIT $1`
	args := []any{first + 1}
	if after != nil {
		query = `SELECT ` + postColumns + ` FROM posts WHERE NOT deleted AND id<$1 ORDER BY id DESC LIMIT $2`
		args = []any{*after, first + 1}
	}
	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return domain.Page[domain.Post]{}, err
	}
	defer rows.Close()
	result := domain.Page[domain.Post]{Items: make([]domain.Post, 0, first)}
	for rows.Next() {
		var p domain.Post
		if err := rows.Scan(postScanFields(&p)...); err != nil {
			return result, err
		}
		if len(result.Items) == first {
			result.HasNext = true
			break
		}
		result.Items = append(result.Items, p)
	}
	return result, rows.Err()
}
func (s *Store) SetCommentsEnabled(ctx context.Context, postID, authorID int64, enabled bool) (domain.Post, error) {
	// An UPDATE takes a row lock, serializing with CreateComment's FOR UPDATE.
	post, err := scanPost(s.pool.QueryRow(ctx, `
        UPDATE posts SET comments_enabled=$3,
        tree_version=tree_version+CASE WHEN comments_enabled IS DISTINCT FROM $3 THEN 1 ELSE 0 END
        WHERE id=$1 AND author_id=$2 AND NOT deleted RETURNING `+postColumns, postID, authorID, enabled))
	if !domain.HasCode(err, domain.ErrNotFound) {
		return post, err
	}
	// Determine whether the post was missing or belongs to somebody else.
	if lookup, lookupErr := s.Post(ctx, postID); lookupErr == nil {
		if lookup.AuthorID == authorID && lookup.Deleted {
			return domain.Post{}, domain.Fault(domain.ErrConflict, "post was deleted")
		}
		return domain.Post{}, domain.Fault(domain.ErrForbidden, "only the post author can change comments")
	} else if !domain.HasCode(lookupErr, domain.ErrNotFound) {
		return domain.Post{}, lookupErr
	}
	return post, err
}
