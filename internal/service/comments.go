package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"

	"github.com/Alexander-D-Karpov/commeng/internal/domain"
)

// CreateComment stores an anonymous comment. Authenticated clients should use
// CreateCommentAs, which takes the display name/email from their account.
func (s *Service) CreateComment(ctx context.Context, input domain.CommentCreate) (domain.Comment, error) {
	return s.CreateCommentAs(ctx, nil, input)
}

func (s *Service) CreateCommentAs(
	ctx context.Context,
	author *domain.User,
	input domain.CommentCreate,
) (domain.Comment, error) {
	// Caller-supplied domain input must never be an authority for identity.
	// Only the authenticated principal may populate UserID.
	input.UserID = nil
	if author != nil {
		input.UserID = &author.ID
		input.Name = author.Name
		input.Email = author.Email
	}
	name, err := domain.LabelText(input.Name, 80)
	if err != nil {
		return domain.Comment{}, domain.Field("name", err)
	}
	email, err := domain.NormalizeEmail(input.Email)
	if err != nil {
		return domain.Comment{}, domain.Field("email", err)
	}
	body, err := domain.Text(input.Body, 2000)
	if err != nil {
		return domain.Comment{}, domain.Field("body", err)
	}
	hash := sha256.Sum256([]byte(email))
	input.Name, input.Email, input.Body, input.AvatarHash = name, email, body, hex.EncodeToString(hash[:])
	created, err := s.Store.CreateComment(ctx, input)
	if err != nil {
		return domain.Comment{}, err
	}
	if !s.DistributedEvents {
		s.Events.Publish(created)
	}
	return created, nil
}

func (s *Service) Comment(ctx context.Context, id int64) (domain.Comment, error) {
	return s.Store.Comment(ctx, id)
}
func (s *Service) Comments(ctx context.Context, req domain.CommentPageRequest) (domain.Page[domain.Comment], error) {
	return s.Store.Comments(ctx, req)
}

func (s *Service) CommentsBatch(
	ctx context.Context,
	req []domain.CommentPageRequest,
) ([]domain.Page[domain.Comment], error) {
	return s.Store.CommentsBatch(ctx, req)
}

func (s *Service) LocateComment(
	ctx context.Context,
	id int64,
	size int,
	sort domain.Sort,
) (domain.LocatedComment, error) {
	return s.Store.LocateComment(ctx, id, size, sort)
}
func (s *Service) Ancestors(ctx context.Context, id int64, limit int) ([]domain.Comment, error) {
	return s.Store.Ancestors(ctx, id, limit)
}

func (s *Service) HasLikes(ctx context.Context, actor string, ids []int64) (map[int64]bool, error) {
	return s.Store.HasLikes(ctx, actor, ids)
}
func (s *Service) Like(ctx context.Context, actor string, id int64, enabled bool) (domain.Comment, error) {
	if actor == "" {
		return domain.Comment{}, domain.Fault(domain.ErrAuth, "login or visitor token required")
	}
	return s.Store.SetLike(ctx, id, actor, enabled)
}

func (s *Service) AncestorsPage(ctx context.Context, id int64, before *int64, first int) (domain.AncestorPage, error) {
	return s.Store.AncestorsPage(ctx, id, before, first)
}

// Subscribe verifies the post before allocating a subscription. Subscriptions
// are live-only; consumers must refresh after reconnecting.
func (s *Service) Subscribe(ctx context.Context, postID int64) (<-chan domain.Comment, error) {
	if _, err := s.Store.Post(ctx, postID); err != nil {
		return nil, err
	}
	head, err := s.Store.CommentEventHead(ctx, postID)
	if err != nil {
		return nil, err
	}
	subscribed := s.Events.Subscribe(ctx, postID)
	if s.EventFollower != nil {
		s.EventFollower.RegisterTopic(postID, head)
	}
	return subscribed, nil
}
