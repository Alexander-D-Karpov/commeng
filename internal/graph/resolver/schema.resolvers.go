// Resolvers for the source schema. gqlgen maintains this file with preserve_resolver: true.
package resolver

import (
	"context"

	"github.com/Alexander-D-Karpov/commeng/internal/auth"
	"github.com/Alexander-D-Karpov/commeng/internal/domain"
	"github.com/Alexander-D-Karpov/commeng/internal/graph/generated"
	"github.com/Alexander-D-Karpov/commeng/internal/graph/model"
	"github.com/Alexander-D-Karpov/commeng/internal/pagination"
)

// --- comment.go ---
func (r *postResolver) Author(ctx context.Context, obj *model.Post) (*model.User, error) {
	id, err := domain.ParseID(obj.AuthorID)
	if err != nil {
		return nil, err
	}
	var user domain.User
	if loaders := getLoaders(ctx); loaders != nil {
		user, err = loaders.Users.Load(ctx, id)
	} else {
		user, err = r.Service.User(ctx, id)
	}
	if err != nil {
		return nil, err
	}
	return asUser(user), nil
}
func (r *postResolver) Comments(
	ctx context.Context, obj *model.Post,
	first *int, after, before *string, sort *model.CommentSort,
) (*model.CommentConnection, error) {
	id, err := domain.ParseID(obj.ID)
	if err != nil {
		return nil, err
	}
	return r.loadComments(ctx, id, nil, first, after, before, sort)
}
func (r *commentResolver) Replies(
	ctx context.Context, obj *model.Comment,
	first *int, after, before *string, sort *model.CommentSort,
) (*model.CommentConnection, error) {
	if _, err := pageSize(first); err != nil {
		return nil, err
	}
	if obj.ReplyCount == 0 && after == nil && before == nil {
		return &model.CommentConnection{
			Edges:    []*model.CommentEdge{},
			PageInfo: &model.PageInfo{HasNextPage: false},
		}, nil
	}
	postID, err := domain.ParseID(obj.PostID)
	if err != nil {
		return nil, err
	}
	commentID, err := domain.ParseID(obj.ID)
	if err != nil {
		return nil, err
	}
	return r.loadComments(ctx, postID, &commentID, first, after, before, sort)
}
func (r *commentResolver) ViewerHasLiked(ctx context.Context, obj *model.Comment) (bool, error) {
	viewer := auth.FromContext(ctx)
	if viewer.ActorKey == "" {
		return false, nil
	}
	id, err := domain.ParseID(obj.ID)
	if err != nil {
		return false, err
	}
	if loaders := getLoaders(ctx); loaders != nil {
		return loaders.Likes.Load(ctx, id)
	}
	liked, err := r.Service.HasLikes(ctx, viewer.ActorKey, []int64{id})
	return liked[id], err
}

// --- mutation.go ---
func (r *mutationResolver) Register(ctx context.Context, name, email, password string) (*model.User, error) {
	user, err := r.Service.Register(ctx, name, email, password)
	if err != nil {
		return nil, err
	}
	return asUser(user), nil
}
func (r *mutationResolver) Login(ctx context.Context, email, password string) (*model.AuthPayload, error) {
	token, user, err := r.Service.Login(ctx, email, password)
	if err != nil {
		return nil, err
	}
	return &model.AuthPayload{Token: token, User: asUser(user)}, nil
}
func (r *mutationResolver) CreatePost(ctx context.Context, input model.CreatePostInput) (*model.Post, error) {
	user, err := auth.RequiredUser(ctx)
	if err != nil {
		return nil, err
	}
	post, err := r.Service.CreatePost(ctx, user.ID, input.Title, input.Body)
	if err != nil {
		return nil, err
	}
	return r.asPost(post), nil
}

func (r *mutationResolver) SetPostCommentsEnabled(
	ctx context.Context,
	postID string,
	enabled bool,
) (*model.Post, error) {
	user, err := auth.RequiredUser(ctx)
	if err != nil {
		return nil, err
	}
	id, err := domain.ParseID(postID)
	if err != nil {
		return nil, err
	}
	post, err := r.Service.SetCommentsEnabled(ctx, user.ID, id, enabled)
	if err != nil {
		return nil, err
	}
	return r.asPost(post), nil
}
func (r *mutationResolver) CreateComment(ctx context.Context, input model.CreateCommentInput) (*model.Comment, error) {
	id, err := domain.ParseID(input.PostID)
	if err != nil {
		return nil, err
	}
	parent, err := parseOptional(input.ParentID)
	if err != nil {
		return nil, err
	}
	viewer := auth.FromContext(ctx)
	name, email := "", ""
	if input.Name != nil {
		name = *input.Name
	}
	if input.Email != nil {
		email = *input.Email
	}
	created, err := r.Service.CreateCommentAs(ctx, viewer.User, domain.CommentCreate{
		PostID: id, ParentID: parent, Name: name, Email: email, Body: input.Body,
	})
	if err != nil {
		return nil, err
	}
	return r.asComment(created), nil
}
func (r *mutationResolver) vote(ctx context.Context, id string, enabled bool) (*model.Comment, error) {
	viewer := auth.FromContext(ctx)
	if viewer.ActorKey == "" {
		return nil, domain.Fault(domain.ErrAuth, "login or visitor token required")
	}
	commentID, err := domain.ParseID(id)
	if err != nil {
		return nil, err
	}
	result, err := r.Service.Like(ctx, viewer.ActorKey, commentID, enabled)
	if err != nil {
		return nil, err
	}
	return r.asComment(result), nil
}
func (r *mutationResolver) LikeComment(ctx context.Context, id string) (*model.Comment, error) {
	return r.vote(ctx, id, true)
}
func (r *mutationResolver) UnlikeComment(ctx context.Context, id string) (*model.Comment, error) {
	return r.vote(ctx, id, false)
}

func (r *mutationResolver) ModerateComment(
	ctx context.Context,
	input model.ModerateCommentInput,
) (*model.Comment, error) {
	user, err := auth.RequiredUser(ctx)
	if err != nil {
		return nil, err
	}
	commentID, err := domain.ParseID(input.CommentID)
	if err != nil {
		return nil, err
	}
	parent, err := parseOptional(input.NewParentID)
	if err != nil {
		return nil, err
	}
	version := int64(0)
	if input.ExpectedTreeVersion != nil {
		version, err = domain.ParseID(*input.ExpectedTreeVersion)
		if err != nil {
			return nil, err
		}
	}
	reason := ""
	if input.Reason != nil {
		reason = *input.Reason
	}
	result, err := r.Service.Moderate(ctx, domain.Moderation{
		CommentID: commentID, ActorID: user.ID,
		ExpectedVersion: version, NewParentID: parent,
		Kind: domain.ModerationKind(input.Action), Reason: reason,
	})
	if err != nil {
		return nil, err
	}
	return r.asComment(result), nil
}

func (r *mutationResolver) Logout(ctx context.Context) (bool, error) {
	viewer := auth.FromContext(ctx)
	if err := r.Service.Logout(ctx, viewer.SessionHash); err != nil {
		return false, err
	}
	return true, nil
}

// --- query.go ---
func (r *queryResolver) Viewer(ctx context.Context) (*model.User, error) {
	viewer := auth.FromContext(ctx)
	if viewer.User == nil {
		return nil, nil
	}
	return asUser(*viewer.User), nil
}
func (r *queryResolver) Posts(ctx context.Context, first *int, after *string) (*model.PostConnection, error) {
	size, err := pageSize(first)
	if err != nil {
		return nil, err
	}
	var cursor *int64
	if after != nil {
		last, err := r.Cursors.Decode(*after, pagination.Cursor{Kind: "posts"})
		if err != nil {
			return nil, err
		}
		cursor = &last
	}
	page, err := r.Service.Posts(ctx, cursor, size)
	if err != nil {
		return nil, err
	}
	return r.postConnection(page), nil
}
func (r *queryResolver) Post(ctx context.Context, id string) (*model.Post, error) {
	postID, err := domain.ParseID(id)
	if err != nil {
		return nil, err
	}
	post, err := r.Service.Post(ctx, postID)
	if domain.HasCode(err, domain.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return r.asPost(post), nil
}
func (r *queryResolver) Comment(ctx context.Context, id string) (*model.Comment, error) {
	commentID, err := domain.ParseID(id)
	if err != nil {
		return nil, err
	}
	comment, err := r.Service.Comment(ctx, commentID)
	if domain.HasCode(err, domain.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return r.asComment(comment), nil
}
func (r *queryResolver) CommentJump(
	ctx context.Context, postID, commentID string,
	pageSizeArg *int, sort *model.CommentSort, ancestorLimit *int,
) (*model.CommentJump, error) {
	post, err := domain.ParseID(postID)
	if err != nil {
		return nil, err
	}
	id, err := domain.ParseID(commentID)
	if err != nil {
		return nil, err
	}
	size, err := pageSize(pageSizeArg)
	if err != nil {
		return nil, err
	}
	limit := 20
	if ancestorLimit != nil {
		limit = *ancestorLimit
	}
	if limit < 0 || limit > 100 {
		return nil, domain.Fault(domain.ErrInput, "ancestor limit must be 0–100")
	}
	order := orderValue(sort)
	location, err := r.Service.LocateComment(ctx, id, size, order)
	if domain.HasCode(err, domain.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if location.Target.PostID != post {
		return nil, domain.Fault(domain.ErrNotFound, "comment not in this post")
	}
	ancestors, err := r.Service.Ancestors(ctx, id, limit)
	if err != nil {
		return nil, err
	}
	path := make([]*model.Comment, 0, len(ancestors))
	for _, ancestor := range ancestors {
		path = append(path, r.asComment(ancestor))
	}
	scope := pagination.Cursor{Kind: "comments", PostID: post, Order: order}
	if location.Target.ParentID != nil {
		scope.ParentID = *location.Target.ParentID
	}
	return &model.CommentJump{
		Target: r.asComment(location.Target), Page: r.commentConnection(location.Page, scope),
		PageNumber: int(location.PageNumber), IndexInPage: location.IndexInPage, Ancestors: path,
	}, nil
}

// CommentAncestors paginates upward in very deep threads; nextBefore is the ID
// of the most distant ancestor returned in the current page.
func (r *queryResolver) CommentAncestors(
	ctx context.Context,
	commentID string,
	before *string,
	first *int,
) (*model.AncestorConnection, error) {
	id, err := domain.ParseID(commentID)
	if err != nil {
		return nil, err
	}
	ancestorID, err := parseOptional(before)
	if err != nil {
		return nil, err
	}
	size, err := pageSize(first)
	if err != nil {
		return nil, err
	}
	page, err := r.Service.AncestorsPage(ctx, id, ancestorID, size)
	if err != nil {
		return nil, err
	}
	nodes := make([]*model.Comment, 0, len(page.Items))
	for _, comment := range page.Items {
		nodes = append(nodes, r.asComment(comment))
	}
	var next *string
	if page.NextBefore != nil {
		value := domain.ID(*page.NextBefore)
		next = &value
	}
	return &model.AncestorConnection{Nodes: nodes, NextBefore: next}, nil
}

// --- subscription.go ---
func (r *subscriptionResolver) CommentAdded(ctx context.Context, postID string) (<-chan *model.Comment, error) {
	id, err := domain.ParseID(postID)
	if err != nil {
		return nil, err
	}
	events, err := r.Service.Subscribe(ctx, id)
	if err != nil {
		return nil, err
	}
	out := make(chan *model.Comment, 32)
	go func() {
		defer close(out)
		for {
			select {
			case <-ctx.Done():
				return
			case item, ok := <-events:
				if !ok {
					return
				}
				select {
				case out <- r.asComment(item):
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	return out, nil
}

// --- gqlgen root dispatch ---
// Resolver implementations and dispatch live in this single file to prevent duplicate declarations.

func (r *Resolver) Query() generated.QueryResolver {
	return &queryResolver{r}
}
func (r *Resolver) Mutation() generated.MutationResolver {
	return &mutationResolver{r}
}
func (r *Resolver) Subscription() generated.SubscriptionResolver {
	return &subscriptionResolver{r}
}
func (r *Resolver) Post() generated.PostResolver {
	return &postResolver{r}
}
func (r *Resolver) Comment() generated.CommentResolver {
	return &commentResolver{r}
}

type queryResolver struct{ *Resolver }
type mutationResolver struct{ *Resolver }
type subscriptionResolver struct{ *Resolver }
type postResolver struct{ *Resolver }
type commentResolver struct{ *Resolver }

// --- Author edits, discovery and notifications ---
func (r *mutationResolver) EditPost(ctx context.Context, input model.EditPostInput) (*model.Post, error) {
	user, err := auth.RequiredUser(ctx)
	if err != nil {
		return nil, err
	}
	id, err := domain.ParseID(input.ID)
	if err != nil {
		return nil, err
	}
	post, err := r.Service.EditPost(ctx, user.ID, id, input.Title, input.Body)
	if err != nil {
		return nil, err
	}
	return r.asPost(post), nil
}
func (r *mutationResolver) DeletePost(ctx context.Context, id string) (*model.Post, error) {
	user, err := auth.RequiredUser(ctx)
	if err != nil {
		return nil, err
	}
	postID, err := domain.ParseID(id)
	if err != nil {
		return nil, err
	}
	post, err := r.Service.DeletePost(ctx, user.ID, postID)
	if err != nil {
		return nil, err
	}
	return r.asPost(post), nil
}
func (r *mutationResolver) EditComment(ctx context.Context, id, body string) (*model.Comment, error) {
	user, err := auth.RequiredUser(ctx)
	if err != nil {
		return nil, err
	}
	commentID, err := domain.ParseID(id)
	if err != nil {
		return nil, err
	}
	comment, err := r.Service.EditComment(ctx, user.ID, commentID, body)
	if err != nil {
		return nil, err
	}
	return r.asComment(comment), nil
}
func (r *mutationResolver) DeleteComment(ctx context.Context, id string) (*model.Comment, error) {
	user, err := auth.RequiredUser(ctx)
	if err != nil {
		return nil, err
	}
	commentID, err := domain.ParseID(id)
	if err != nil {
		return nil, err
	}
	comment, err := r.Service.DeleteComment(ctx, user.ID, commentID)
	if err != nil {
		return nil, err
	}
	return r.asComment(comment), nil
}

func (r *queryResolver) SearchPosts(
	ctx context.Context,
	query string,
	first *int,
	after *string,
) (*model.PostConnection, error) {
	size, err := pageSize(first)
	if err != nil {
		return nil, err
	}
	scope := pagination.Cursor{Kind: "search-posts", Scope: searchScope(query)}
	var cursor *int64
	if after != nil {
		id, err := r.Cursors.Decode(*after, scope)
		if err != nil {
			return nil, err
		}
		cursor = &id
	}
	page, err := r.Service.SearchPosts(ctx, query, cursor, size)
	if err != nil {
		return nil, err
	}
	return r.scopedPostConnection(page, scope), nil
}

func (r *queryResolver) SearchComments(
	ctx context.Context,
	query string,
	postID *string,
	first *int,
	after *string,
) (*model.CommentConnection, error) {
	size, err := pageSize(first)
	if err != nil {
		return nil, err
	}
	id, err := parseOptional(postID)
	if err != nil {
		return nil, err
	}
	scope := pagination.Cursor{Kind: "search-comments", Scope: searchScope(query), Order: domain.SortNewest}
	if id != nil {
		scope.PostID = *id
	}
	var cursor *int64
	if after != nil {
		afterID, err := r.Cursors.Decode(*after, scope)
		if err != nil {
			return nil, err
		}
		cursor = &afterID
	}
	page, err := r.Service.SearchComments(ctx, query, id, cursor, size)
	if err != nil {
		return nil, err
	}
	return r.commentConnection(page, scope), nil
}

func (r *queryResolver) Notifications(
	ctx context.Context,
	first *int,
	after *string,
	unreadOnly *bool,
) (*model.NotificationConnection, error) {
	user, err := auth.RequiredUser(ctx)
	if err != nil {
		return nil, err
	}
	size, err := pageSize(first)
	if err != nil {
		return nil, err
	}
	unread := unreadOnly != nil && *unreadOnly
	scope := pagination.Cursor{Kind: "notifications", PostID: user.ID, Scope: unreadScope(unread)}
	var cursor *int64
	if after != nil {
		id, err := r.Cursors.Decode(*after, scope)
		if err != nil {
			return nil, err
		}
		cursor = &id
	}
	page, err := r.Service.Notifications(ctx, user.ID, cursor, size, unread)
	if err != nil {
		return nil, err
	}
	return r.notificationConnection(page, scope), nil
}
func (r *mutationResolver) MarkNotificationsRead(ctx context.Context, ids []string) (int, error) {
	user, err := auth.RequiredUser(ctx)
	if err != nil {
		return 0, err
	}
	out := make([]int64, 0, len(ids))
	for _, value := range ids {
		id, err := domain.ParseID(value)
		if err != nil {
			return 0, err
		}
		out = append(out, id)
	}
	return r.Service.MarkNotificationsRead(ctx, user.ID, out)
}

func (r *queryResolver) CommentEvents(
	ctx context.Context,
	postID string,
	first *int,
	after *string,
) (*model.CommentEventConnection, error) {
	id, err := domain.ParseID(postID)
	if err != nil {
		return nil, err
	}
	size, err := pageSize(first)
	if err != nil {
		return nil, err
	}
	scope := pagination.Cursor{Kind: "comment-events", PostID: id}
	var last int64
	if after != nil {
		last, err = r.Cursors.Decode(*after, scope)
		if err != nil {
			return nil, err
		}
	}
	page, err := r.Service.Store.CommentEvents(ctx, id, last, size)
	if err != nil {
		return nil, err
	}
	return r.eventConnection(page, scope), nil
}
