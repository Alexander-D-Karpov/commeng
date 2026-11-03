package memory

import (
	"context"
	"strings"

	"github.com/Alexander-D-Karpov/commeng/internal/domain"
)

// Moderate atomically checks the actor, applies the change and updates thread
// flags. All work is in memory; no network I/O is performed while locked.
func (s *Store) Moderate(ctx context.Context, command domain.Moderation) (domain.Comment, error) {
	if err := ctx.Err(); err != nil {
		return domain.Comment{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	item, post, actor, err := s.moderationTarget(command)
	if err != nil {
		return domain.Comment{}, err
	}
	original := item
	if err := s.applyModeration(&item, command, actor); err != nil {
		return domain.Comment{}, err
	}
	if item.State == original.State && item.Locked == original.Locked &&
		parentID(item.ParentID) == parentID(original.ParentID) {
		return cloneComment(item), nil
	}

	// Only policy changes and moves advance this revision, never regular replies.
	post.TreeVersion++
	s.posts[post.ID] = post
	s.comments[item.ID] = item
	s.recalculateThreadFlags(item.ID)
	return cloneComment(s.comments[item.ID]), nil
}

// moderationTarget requires s.mu to be held by the caller.
func (s *Store) moderationTarget(command domain.Moderation) (domain.Comment, domain.Post, domain.User, error) {
	item, ok := s.comments[command.CommentID]
	if !ok {
		return domain.Comment{}, domain.Post{}, domain.User{}, domain.Fault(domain.ErrNotFound, "comment not found")
	}
	post := s.posts[item.PostID]
	actor, ok := s.users[command.ActorID]
	if !ok {
		return domain.Comment{}, domain.Post{}, domain.User{}, domain.Fault(domain.ErrAuth, "login required")
	}
	if actor.Role != domain.RoleAdmin && actor.Role != domain.RoleModerator && post.AuthorID != actor.ID {
		return domain.Comment{}, domain.Post{}, domain.User{}, domain.Fault(
			domain.ErrForbidden,
			"moderation not permitted",
		)
	}
	if command.Kind == domain.MoveComment && post.TreeVersion != command.ExpectedVersion {
		return domain.Comment{}, domain.Post{}, domain.User{}, domain.Fault(domain.ErrConflict, "tree version changed")
	}
	return item, post, actor, nil
}

func (s *Store) applyModeration(item *domain.Comment, command domain.Moderation, actor domain.User) error {
	switch command.Kind {
	case domain.HideComment:
		item.State = domain.StateHidden
	case domain.RestoreComment:
		if item.State == domain.StateDeleted {
			return domain.Fault(domain.ErrConflict, "deleted comments cannot be restored")
		}
		item.State = domain.StateVisible
	case domain.DeleteComment:
		item.State = domain.StateDeleted
		item.Body, item.Name, item.Email = "[deleted]", "[deleted]", "deleted@invalid.example"
		item.AvatarHash = strings.Repeat("0", 64)
		item.UserID = nil
	case domain.LockComment:
		item.Locked = true
	case domain.UnlockComment:
		item.Locked = false
	case domain.MoveComment:
		if actor.Role != domain.RoleAdmin && actor.Role != domain.RoleModerator {
			return domain.Fault(domain.ErrForbidden, "moving requires moderator")
		}
		return s.moveComment(item, command.NewParentID)
	default:
		return domain.Fault(domain.ErrInput, "unknown moderation action")
	}
	return nil
}

// validateMoveDestination runs before any mutations, so an invalid move never
// changes a parent count or removes a child from its original index.
func (s *Store) validateMoveDestination(item domain.Comment, destination *int64) error {
	for current := parentID(destination); current != 0; {
		ancestor, exists := s.comments[current]
		if !exists || ancestor.PostID != item.PostID || ancestor.ID == item.ID ||
			ancestor.Locked || ancestor.ThreadBlocked || ancestor.State != domain.StateVisible {
			return domain.Fault(domain.ErrInput, "invalid destination, hidden thread, or cycle")
		}
		current = parentID(ancestor.ParentID)
	}
	return nil
}

func (s *Store) moveComment(item *domain.Comment, destination *int64) error {
	if err := s.validateMoveDestination(*item, destination); err != nil {
		return err
	}
	if parentID(item.ParentID) == parentID(destination) {
		return nil
	}

	oldScope := scope{item.PostID, parentID(item.ParentID)}
	children := s.children[oldScope]
	index := sortSearchGreaterEqual(children, item.ID)
	if index >= len(children) || children[index] != item.ID {
		return domain.Fault(domain.ErrInput, "broken parent index")
	}

	// The index is checked first; from here all modifications are atomic under mu.
	s.children[oldScope] = append(children[:index:index], children[index+1:]...)
	if item.ParentID != nil {
		parent := s.comments[*item.ParentID]
		parent.ReplyCount--
		s.comments[parent.ID] = parent
	}
	item.ParentID = copyParent(destination)
	if item.ParentID != nil {
		parent := s.comments[*item.ParentID]
		parent.ReplyCount++
		s.comments[parent.ID] = parent
	}
	newScope := scope{item.PostID, parentID(item.ParentID)}
	ids := s.children[newScope]
	insertAt := sortSearchGreaterEqual(ids, item.ID)
	ids = append(ids, 0)
	copy(ids[insertAt+1:], ids[insertAt:])
	ids[insertAt] = item.ID
	s.children[newScope] = ids
	return nil
}
