package service

import (
	"context"
	"unicode"

	"github.com/Alexander-D-Karpov/commeng/internal/domain"
)

func (s *Service) EditPost(ctx context.Context, actorID, postID int64, title, body string) (domain.Post, error) {
	if actorID <= 0 {
		return domain.Post{}, domain.Fault(domain.ErrAuth, "login required")
	}
	title, err := domain.LabelText(title, 200)
	if err != nil {
		return domain.Post{}, domain.Field("title", err)
	}
	body, err = domain.Text(body, 20000)
	if err != nil {
		return domain.Post{}, domain.Field("body", err)
	}
	return s.Store.EditPost(ctx, postID, actorID, title, body)
}
func (s *Service) DeletePost(ctx context.Context, actorID, postID int64) (domain.Post, error) {
	if actorID <= 0 {
		return domain.Post{}, domain.Fault(domain.ErrAuth, "login required")
	}
	return s.Store.DeletePost(ctx, postID, actorID)
}
func (s *Service) EditComment(ctx context.Context, actorID, commentID int64, body string) (domain.Comment, error) {
	if actorID <= 0 {
		return domain.Comment{}, domain.Fault(domain.ErrAuth, "login required")
	}
	value, err := domain.Text(body, 2000)
	if err != nil {
		return domain.Comment{}, domain.Field("body", err)
	}
	return s.Store.EditComment(ctx, commentID, actorID, value)
}
func (s *Service) DeleteComment(ctx context.Context, actorID, commentID int64) (domain.Comment, error) {
	if actorID <= 0 {
		return domain.Comment{}, domain.Fault(domain.ErrAuth, "login required")
	}
	return s.Store.DeleteComment(ctx, commentID, actorID)
}
func validQuery(query string) (string, error) {
	value, err := domain.LabelText(query, 120)
	if err != nil {
		return "", err
	}
	for _, r := range value {
		if unicode.IsLetter(r) || unicode.IsNumber(r) {
			return value, nil
		}
	}
	return "", domain.Fault(domain.ErrInput, "query must contain a letter or number")
}

func (s *Service) SearchPosts(
	ctx context.Context,
	query string,
	after *int64,
	first int,
) (domain.Page[domain.Post], error) {
	value, err := validQuery(query)
	if err != nil {
		return domain.Page[domain.Post]{}, domain.Field("query", err)
	}
	return s.Store.SearchPosts(ctx, value, after, first)
}

func (s *Service) SearchComments(
	ctx context.Context,
	query string,
	postID, after *int64,
	first int,
) (domain.Page[domain.Comment], error) {
	value, err := validQuery(query)
	if err != nil {
		return domain.Page[domain.Comment]{}, domain.Field("query", err)
	}
	return s.Store.SearchComments(ctx, value, postID, after, first)
}

func (s *Service) Notifications(
	ctx context.Context,
	userID int64,
	after *int64,
	first int,
	unreadOnly bool,
) (domain.Page[domain.Notification], error) {
	if userID <= 0 {
		return domain.Page[domain.Notification]{}, domain.Fault(domain.ErrAuth, "login required")
	}
	return s.Store.Notifications(ctx, userID, after, first, unreadOnly)
}
func (s *Service) MarkNotificationsRead(ctx context.Context, userID int64, ids []int64) (int, error) {
	if userID <= 0 {
		return 0, domain.Fault(domain.ErrAuth, "login required")
	}
	if len(ids) < 1 || len(ids) > 100 {
		return 0, domain.Fault(domain.ErrInput, "provide 1–100 notification IDs")
	}
	return s.Store.MarkNotificationsRead(ctx, userID, ids)
}
