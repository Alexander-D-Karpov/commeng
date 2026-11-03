// Package service validates public operations and owns authorization rules.
package service

import (
	"fmt"
	"strings"

	"github.com/Alexander-D-Karpov/commeng/internal/domain"
	"github.com/Alexander-D-Karpov/commeng/internal/live"
	"github.com/Alexander-D-Karpov/commeng/internal/repository"
)

// Service contains business operations independent of GraphQL and HTTP.
type Service struct {
	Store             repository.Repository
	Events            *live.Broker
	BaseURL           string
	PermalinkTemplate string
	DistributedEvents bool
	EventFollower     interface {
		RegisterTopic(postID, afterSequence int64)
		Ready() bool
	}
}

func New(store repository.Repository, events *live.Broker, baseURL string) *Service {
	return &Service{
		Store:   store,
		Events:  events,
		BaseURL: strings.TrimRight(baseURL, "/"),
	}
}

func (s *Service) AvatarURL(hash string, size int) string {
	if hash == "" {
		return ""
	}
	if size < 1 {
		size = 80
	}
	if size > 256 {
		size = 256
	}
	return fmt.Sprintf(
		"https://www.gravatar.com/avatar/%s?s=%d&d=identicon",
		hash,
		size,
	)
}

func (s *Service) Permalink(comment domain.Comment) string {
	if s.PermalinkTemplate != "" {
		value := strings.ReplaceAll(
			s.PermalinkTemplate,
			"{post}",
			domain.ID(comment.PostID),
		)
		return strings.ReplaceAll(
			value,
			"{comment}",
			domain.ID(comment.ID),
		)
	}
	return fmt.Sprintf(
		"%s/p/%d/c/%d",
		s.BaseURL,
		comment.PostID,
		comment.ID,
	)
}
