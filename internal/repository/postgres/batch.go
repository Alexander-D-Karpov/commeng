package postgres

import (
	"context"
	"fmt"
	"math"
	"slices"

	"github.com/Alexander-D-Karpov/commeng/internal/domain"
)

// CommentsBatch issues at most eight index-backed SQL statements (2 orders,
// root/child scope, forward/backward). Every scope is a bounded LATERAL page.
// The count is independent of the number of parent comments requested.
func (s *Store) CommentsBatch(
	ctx context.Context,
	requests []domain.CommentPageRequest,
) ([]domain.Page[domain.Comment], error) {
	pages := make([]domain.Page[domain.Comment], len(requests))
	for _, request := range requests {
		if err := domain.ValidatePageSize(request.First); err != nil {
			return nil, err
		}
		if !domain.ValidSort(request.Sort) {
			return nil, domain.Fault(domain.ErrInput, "invalid comment sort")
		}
		if request.After != nil && request.Before != nil {
			return nil, domain.Fault(domain.ErrInput, "use after or before, not both")
		}
	}
	if err := s.ensureBatchPosts(ctx, requests); err != nil {
		return nil, err
	}
	for _, sortOrder := range []domain.Sort{domain.SortNewest, domain.SortOldest} {
		for _, roots := range []bool{true, false} {
			for _, backwards := range []bool{false, true} {
				group := collectCommentBatch(requests, sortOrder, roots, backwards)
				if len(group.indices) == 0 {
					continue
				}
				if err := s.runCommentBatch(ctx, group, requests, pages); err != nil {
					return nil, err
				}
			}
		}
	}
	return pages, nil
}

type commentBatchGroup struct {
	sortOrder               domain.Sort
	roots, backwards        bool
	indices                 []int
	posts, parents, anchors []int64
	limits                  []int32
}

func collectCommentBatch(
	requests []domain.CommentPageRequest,
	sortOrder domain.Sort,
	roots, backwards bool,
) commentBatchGroup {
	group := commentBatchGroup{sortOrder: sortOrder, roots: roots, backwards: backwards}
	for index, request := range requests {
		if request.Sort != sortOrder || (request.ParentID == nil) != roots || (request.Before != nil) != backwards {
			continue
		}
		group.indices = append(group.indices, index)
		group.posts = append(group.posts, request.PostID)
		parent := int64(0)
		if request.ParentID != nil {
			parent = *request.ParentID
		}
		group.parents = append(group.parents, parent)
		anchor := int64(0)
		if request.Before != nil {
			anchor = *request.Before
		} else if request.After != nil {
			anchor = *request.After
		} else if sortOrder == domain.SortNewest {
			anchor = math.MaxInt64
		}
		group.anchors = append(group.anchors, anchor)
		group.limits = append(group.limits, int32(request.First+1))
	}
	return group
}

func (s *Store) runCommentBatch(
	ctx context.Context,
	group commentBatchGroup,
	requests []domain.CommentPageRequest,
	pages []domain.Page[domain.Comment],
) error {
	predicate := "c.parent_id=req.parent_id"
	if group.roots {
		predicate = "c.parent_id IS NULL"
	}
	order, comparison, _ := orderSQL(group.sortOrder)
	if group.backwards {
		if comparison == "<" {
			comparison, order = ">", "ASC"
		} else {
			comparison, order = "<", "DESC"
		}
	}
	statement := fmt.Sprintf(`
        WITH req AS (
            SELECT * FROM unnest($1::bigint[], $2::bigint[], $3::bigint[], $4::int[])
              WITH ORDINALITY AS r(post_id,parent_id,anchor_id,page_limit,ordinal)
        )
        SELECT req.ordinal, c.id, coalesce(c.post_id,0), c.parent_id,
            coalesce(c.author_name,''), coalesce(c.author_email,''),
            coalesce(c.avatar_hash,''), c.user_id,
            coalesce(c.is_post_author,false), coalesce(c.thread_blocked,false),
            coalesce(c.body,''), coalesce(c.state,'VISIBLE'), coalesce(c.locked,false),
            coalesce(c.reply_count,0), coalesce(c.likes_count,0),
            coalesce(c.created_at,'epoch'::timestamptz), c.edited_at
        FROM req LEFT JOIN LATERAL (
            SELECT %s FROM comments c
            WHERE c.post_id=req.post_id AND %s AND c.id %s req.anchor_id
            ORDER BY c.id %s LIMIT req.page_limit
        ) c ON true ORDER BY req.ordinal, c.id %s`,
		commentColumns, predicate, comparison, order, order)
	rows, err := s.pool.Query(ctx, statement, group.posts, group.parents, group.anchors, group.limits)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var ordinal int64
		var id *int64
		var comment domain.Comment
		err := rows.Scan(&ordinal, &id, &comment.PostID, &comment.ParentID,
			&comment.Name, &comment.Email, &comment.AvatarHash, &comment.UserID,
			&comment.IsPostAuthor, &comment.ThreadBlocked, &comment.Body, &comment.State,
			&comment.Locked, &comment.ReplyCount, &comment.LikesCount, &comment.CreatedAt, &comment.EditedAt)
		if err != nil {
			return err
		}
		index := group.indices[ordinal-1]
		if id == nil {
			continue
		}
		comment.ID = *id
		page := &pages[index]
		if len(page.Items) == requests[index].First {
			if group.backwards {
				page.HasPrev = true
			} else {
				page.HasNext = true
			}
			continue
		}
		page.Items = append(page.Items, comment)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, index := range group.indices {
		if group.backwards {
			slices.Reverse(pages[index].Items)
			pages[index].HasNext = len(pages[index].Items) > 0
		} else if requests[index].After != nil {
			pages[index].HasPrev = len(pages[index].Items) > 0
		}
	}
	return nil
}

// ensureBatchPosts checks existence once for the entire set, including empty pages.
func (s *Store) ensureBatchPosts(ctx context.Context, requests []domain.CommentPageRequest) error {
	if len(requests) == 0 {
		return nil
	}
	unique := make(map[int64]struct{}, len(requests))
	for _, request := range requests {
		unique[request.PostID] = struct{}{}
	}
	ids := make([]int64, 0, len(unique))
	for id := range unique {
		ids = append(ids, id)
	}
	var count int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM posts WHERE id=ANY($1::bigint[])`, ids).Scan(&count); err != nil {
		return err
	}
	if count != len(ids) {
		return domain.Fault(domain.ErrNotFound, "post not found")
	}
	return nil
}
