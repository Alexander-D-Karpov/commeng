package resolver

import (
	"context"

	"github.com/Alexander-D-Karpov/commeng/internal/domain"
	"github.com/Alexander-D-Karpov/commeng/internal/graph/model"
	"github.com/Alexander-D-Karpov/commeng/internal/pagination"
)

func asUser(value domain.User) *model.User {
	return &model.User{ID: domain.ID(value.ID), Name: value.Name}
}
func (r *Resolver) asPost(value domain.Post) *model.Post {
	return &model.Post{
		ID: domain.ID(value.ID), AuthorID: domain.ID(value.AuthorID), Title: value.Title, Body: value.Body,
		CreatedAt: value.CreatedAt.UTC().Format("2006-01-02T15:04:05.999999999Z07:00"),
		UpdatedAt: value.UpdatedAt.UTC().Format("2006-01-02T15:04:05.999999999Z07:00"), Deleted: value.Deleted,
		CommentsEnabled: value.CommentsEnabled, CommentCount: int(value.CommentCount),
		TreeVersion: domain.ID(value.TreeVersion),
	}
}
func (r *Resolver) asComment(value domain.Comment) *model.Comment {
	name := value.Name
	var body *string
	avatar := ""
	if value.State == domain.StateVisible {
		body = &value.Body
		avatar = r.Service.AvatarURL(value.AvatarHash, 80)
	} else {
		name = "Hidden comment"
	}
	var parent *string
	if value.ParentID != nil {
		id := domain.ID(*value.ParentID)
		parent = &id
	}
	var editedAt *string
	if value.EditedAt != nil {
		stamp := value.EditedAt.UTC().Format("2006-01-02T15:04:05.999999999Z07:00")
		editedAt = &stamp
	}
	return &model.Comment{
		ID: domain.ID(value.ID), PostID: domain.ID(value.PostID), ParentID: parent,
		Author: &model.CommentAuthor{Name: name, AvatarURL: avatar, IsPostAuthor: value.IsPostAuthor}, Body: body,
		State: model.CommentState(value.State), Locked: value.Locked,
		CreatedAt: value.CreatedAt.UTC().Format("2006-01-02T15:04:05.999999999Z07:00"),
		Permalink: r.Service.Permalink(
			value,
		), LikesCount: int(value.LikesCount), ReplyCount: int(value.ReplyCount), EditedAt: editedAt,
	}
}

func (r *Resolver) commentConnection(
	page domain.Page[domain.Comment],
	scope pagination.Cursor,
) *model.CommentConnection {
	edges := make([]*model.CommentEdge, 0, len(page.Items))
	for _, item := range page.Items {
		edgeCursor := scope
		edgeCursor.LastID = item.ID
		edges = append(edges, &model.CommentEdge{Cursor: r.Cursors.Encode(edgeCursor), Node: r.asComment(item)})
	}
	var end *string
	if len(edges) > 0 {
		value := edges[len(edges)-1].Cursor
		end = &value
	}
	var start *string
	if len(edges) > 0 {
		value := edges[0].Cursor
		start = &value
	}
	return &model.CommentConnection{Edges: edges,
		PageInfo: &model.PageInfo{HasNextPage: page.HasNext, HasPreviousPage: page.HasPrev,
			StartCursor: start, EndCursor: end}}
}
func (r *Resolver) postConnection(page domain.Page[domain.Post]) *model.PostConnection {
	return r.scopedPostConnection(page, pagination.Cursor{Kind: "posts"})
}
func (r *Resolver) scopedPostConnection(page domain.Page[domain.Post], scope pagination.Cursor) *model.PostConnection {
	edges := make([]*model.PostEdge, 0, len(page.Items))
	for _, item := range page.Items {
		cursor := scope
		cursor.LastID = item.ID
		edges = append(edges, &model.PostEdge{Cursor: r.Cursors.Encode(cursor), Node: r.asPost(item)})
	}
	var end *string
	if len(edges) > 0 {
		value := edges[len(edges)-1].Cursor
		end = &value
	}
	var start *string
	if len(edges) > 0 {
		value := edges[0].Cursor
		start = &value
	}
	return &model.PostConnection{Edges: edges,
		PageInfo: &model.PageInfo{HasNextPage: page.HasNext, HasPreviousPage: page.HasPrev,
			StartCursor: start, EndCursor: end}}
}
func pageSize(value *int) (int, error) {
	first := 20
	if value != nil {
		first = *value
	}
	return first, domain.ValidatePageSize(first)
}
func orderValue(value *model.CommentSort) domain.Sort {
	if value != nil && *value == model.CommentSortOldest {
		return domain.SortOldest
	}
	return domain.SortNewest
}
func parseOptional(value *string) (*int64, error) {
	if value == nil {
		return nil, nil
	}
	id, err := domain.ParseID(*value)
	if err != nil {
		return nil, err
	}
	return &id, nil
}
func (r *Resolver) loadComments(
	ctx context.Context, postID int64, parentID *int64,
	first *int, after, before *string, sort *model.CommentSort,
) (*model.CommentConnection, error) {
	size, err := pageSize(first)
	if err != nil {
		return nil, err
	}
	order := orderValue(sort)
	scope := pagination.Cursor{Kind: "comments", PostID: postID, Order: order}
	if parentID != nil {
		scope.ParentID = *parentID
	}
	if after != nil && before != nil {
		return nil, domain.Fault(domain.ErrInput, "use after or before, not both")
	}
	var beforeID int64
	if before != nil {
		beforeID, err = r.Cursors.Decode(*before, scope)
		if err != nil {
			return nil, err
		}
	}
	var afterID int64
	if after != nil {
		afterID, err = r.Cursors.Decode(*after, scope)
		if err != nil {
			return nil, err
		}
	}
	key := repliesKey{
		PostID: postID, ParentID: scope.ParentID,
		AfterID: afterID, BeforeID: beforeID, First: size, Sort: order,
	}
	var page domain.Page[domain.Comment]
	if loaders := getLoaders(ctx); loaders != nil {
		page, err = loaders.Replies.Load(ctx, key)
	} else {
		var cursor *int64
		if afterID != 0 {
			cursor = &afterID
		}
		page, err = r.Service.Comments(ctx, domain.CommentPageRequest{
			PostID: postID, ParentID: parentID, After: cursor, Before: func() *int64 {
				if beforeID == 0 {
					return nil
				}
				return &beforeID
			}(), First: size, Sort: order,
		})
	}
	if err != nil {
		return nil, err
	}
	return r.commentConnection(page, scope), nil
}
