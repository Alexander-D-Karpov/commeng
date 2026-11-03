package service

import (
	"context"
	"unicode/utf8"

	"github.com/Alexander-D-Karpov/commeng/internal/domain"
)

// Moderate validates the command before delegating its atomic execution.
func (s *Service) Moderate(ctx context.Context, command domain.Moderation) (domain.Comment, error) {
	if command.ActorID <= 0 {
		return domain.Comment{}, domain.Fault(domain.ErrAuth, "login required")
	}
	if utf8.RuneCountInString(command.Reason) > 500 {
		return domain.Comment{}, domain.Fault(domain.ErrInput, "moderation reason exceeds 500 code points")
	}
	switch command.Kind {
	case domain.HideComment, domain.RestoreComment, domain.DeleteComment, domain.LockComment, domain.UnlockComment:
		// Idempotent actions are not rejected due to a stale tree version.
	case domain.MoveComment:
		if command.ExpectedVersion < 1 {
			return domain.Comment{}, domain.Fault(domain.ErrInput, "MOVE requires expectedTreeVersion")
		}
	default:
		return domain.Comment{}, domain.Fault(domain.ErrInput, "unsupported moderation action")
	}
	return s.Store.Moderate(ctx, command)
}
