package service

import (
	"context"

	"github.com/Alexander-D-Karpov/commeng/internal/domain"
)

func (s *Service) CreatePost(ctx context.Context, authorID int64, title, body string) (domain.Post, error) {
	if authorID == 0 {
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
	return s.Store.CreatePost(ctx, authorID, title, body)
}

func (s *Service) SetCommentsEnabled(ctx context.Context, actorID, postID int64, enabled bool) (domain.Post, error) {
	if actorID == 0 {
		return domain.Post{}, domain.Fault(domain.ErrAuth, "login required")
	}
	return s.Store.SetCommentsEnabled(ctx, postID, actorID, enabled)
}

func (s *Service) Post(ctx context.Context, id int64) (domain.Post, error) {
	return s.Store.Post(ctx, id)
}
func (s *Service) Posts(ctx context.Context, after *int64, first int) (domain.Page[domain.Post], error) {
	return s.Store.Posts(ctx, after, first)
}
func (s *Service) User(ctx context.Context, id int64) (domain.User, error) {
	return s.Store.UserByID(ctx, id)
}
func (s *Service) Users(ctx context.Context, ids []int64) (map[int64]domain.User, error) {
	return s.Store.Users(ctx, ids)
}
func (s *Service) Ping(ctx context.Context) error { return s.Store.Ping(ctx) }
