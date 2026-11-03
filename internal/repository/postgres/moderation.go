package postgres

import (
	"context"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/Alexander-D-Karpov/commeng/internal/domain"
)

// Moderate is atomic with comment creation and other tree modifications:
// all of them lock the post row before changing the tree.
func (s *Store) Moderate(ctx context.Context, command domain.Moderation) (domain.Comment, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.Comment{}, err
	}
	defer func() { _ = tx.Rollback(context.Background()) }()

	postID, actor, err := authorizeModeration(ctx, tx, command)
	if err != nil {
		return domain.Comment{}, err
	}
	previous, err := scanComment(tx.QueryRow(ctx,
		`SELECT `+commentColumns+` FROM comments WHERE id=$1`, command.CommentID))
	if err != nil {
		return domain.Comment{}, err
	}

	updated, err := applyModeration(ctx, tx, previous, actor, command)
	if err != nil {
		return domain.Comment{}, err
	}
	if updated.State == previous.State && updated.Locked == previous.Locked &&
		parentEqual(updated.ParentID, previous.ParentID) {
		if err := tx.Commit(ctx); err != nil {
			return domain.Comment{}, err
		}
		return updated, nil
	}
	if err := refreshThreadFlags(ctx, tx, updated.ID); err != nil {
		return domain.Comment{}, err
	}
	if _, err := tx.Exec(ctx,
		`UPDATE posts SET tree_version=tree_version+1 WHERE id=$1`, postID); err != nil {
		return domain.Comment{}, err
	}
	if _, err := tx.Exec(ctx, `
        INSERT INTO moderation_audit(
            comment_id, actor_id, action, old_parent_id, new_parent_id, reason
        ) VALUES ($1,$2,$3,$4,$5,$6)`,
		updated.ID, actor.ID, command.Kind, previous.ParentID, updated.ParentID,
		command.Reason,
	); err != nil {
		return domain.Comment{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.Comment{}, err
	}
	return updated, nil
}

func authorizeModeration(
	ctx context.Context, tx pgx.Tx, command domain.Moderation,
) (int64, domain.User, error) {
	var postID int64
	err := tx.QueryRow(ctx,
		`SELECT post_id FROM comments WHERE id=$1`, command.CommentID,
	).Scan(&postID)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, domain.User{}, domain.Fault(domain.ErrNotFound, "comment not found")
	}
	if err != nil {
		return 0, domain.User{}, err
	}

	var version, authorID int64
	err = tx.QueryRow(ctx,
		`SELECT tree_version, author_id FROM posts WHERE id=$1 FOR UPDATE`, postID,
	).Scan(&version, &authorID)
	if err != nil {
		return 0, domain.User{}, err
	}
	actor, err := scanUser(tx.QueryRow(ctx,
		`SELECT `+userColumns+` FROM users WHERE id=$1`, command.ActorID))
	if err != nil {
		return 0, domain.User{}, err
	}
	if !isModerator(actor) && authorID != actor.ID {
		return 0, domain.User{}, domain.Fault(domain.ErrForbidden, "moderation not permitted")
	}
	if command.Kind == domain.MoveComment && version != command.ExpectedVersion {
		return 0, domain.User{}, domain.Fault(domain.ErrConflict, "tree version changed")
	}
	return postID, actor, nil
}

func isModerator(actor domain.User) bool {
	return actor.Role == domain.RoleAdmin || actor.Role == domain.RoleModerator
}

func applyModeration(
	ctx context.Context, tx pgx.Tx, item domain.Comment,
	actor domain.User, command domain.Moderation,
) (domain.Comment, error) {
	switch command.Kind {
	case domain.HideComment:
		item.State = domain.StateHidden
		_, err := tx.Exec(ctx, `UPDATE comments SET state=$2 WHERE id=$1`, item.ID, item.State)
		return item, err
	case domain.RestoreComment:
		if item.State == domain.StateDeleted {
			return domain.Comment{}, domain.Fault(domain.ErrConflict, "deleted comments cannot be restored")
		}
		item.State = domain.StateVisible
		_, err := tx.Exec(ctx, `UPDATE comments SET state=$2 WHERE id=$1`, item.ID, item.State)
		return item, err
	case domain.DeleteComment:
		item.State = domain.StateDeleted
		item.Body, item.Name, item.Email = "[deleted]", "[deleted]", "deleted@invalid.example"
		item.AvatarHash = strings.Repeat("0", 64)
		item.UserID = nil
		_, err := tx.Exec(
			ctx,
			`UPDATE comments SET state='DELETED',body=$2,author_name=$3,author_email=$4,
			 avatar_hash=$5,user_id=NULL,edited_at=now() WHERE id=$1`,
			item.ID,
			item.Body,
			item.Name,
			item.Email,
			item.AvatarHash,
		)
		return item, err
	case domain.LockComment, domain.UnlockComment:
		item.Locked = command.Kind == domain.LockComment
		_, err := tx.Exec(ctx, `UPDATE comments SET locked=$2 WHERE id=$1`, item.ID, item.Locked)
		return item, err
	case domain.MoveComment:
		if !isModerator(actor) {
			return domain.Comment{}, domain.Fault(domain.ErrForbidden, "moving requires moderator")
		}
		return moveComment(ctx, tx, item, command.NewParentID)
	default:
		return domain.Comment{}, domain.Fault(domain.ErrInput, "unknown moderation action")
	}
}

func moveComment(
	ctx context.Context, tx pgx.Tx, item domain.Comment, newParent *int64,
) (domain.Comment, error) {
	if newParent != nil {
		// A destination must exist, belong to the same post, not be in the
		// moving subtree, and have no hidden or locked ancestors.
		var chainLength, invalid int64
		err := tx.QueryRow(ctx, `
            WITH RECURSIVE path AS (
                SELECT id,post_id,parent_id,state,locked,thread_blocked
                FROM comments WHERE id=$1
                UNION ALL
                SELECT c.id,c.post_id,c.parent_id,c.state,c.locked,c.thread_blocked
                FROM comments c JOIN path p ON c.id=p.parent_id
            )
            SELECT count(*), count(*) FILTER (
                WHERE post_id<>$2 OR id=$3 OR state<>'VISIBLE' OR locked OR thread_blocked
            ) FROM path`, *newParent, item.PostID, item.ID,
		).Scan(&chainLength, &invalid)
		if err != nil {
			return domain.Comment{}, err
		}
		if chainLength == 0 || invalid != 0 {
			return domain.Comment{}, domain.Fault(
				domain.ErrInput, "invalid destination, hidden thread, or cycle",
			)
		}
	}

	if item.ParentID != nil {
		if _, err := tx.Exec(ctx,
			`UPDATE comments SET reply_count=reply_count-1 WHERE id=$1`,
			*item.ParentID,
		); err != nil {
			return domain.Comment{}, err
		}
	}
	if newParent != nil {
		if _, err := tx.Exec(ctx,
			`UPDATE comments SET reply_count=reply_count+1 WHERE id=$1`,
			*newParent,
		); err != nil {
			return domain.Comment{}, err
		}
	}
	item.ParentID = newParent
	// Capture effective inherited policy of the new parent, if any.
	blocked := false
	if newParent != nil {
		if err := tx.QueryRow(ctx,
			`SELECT locked OR thread_blocked OR state <> 'VISIBLE' FROM comments WHERE id=$1`, *newParent,
		).Scan(&blocked); err != nil {
			return domain.Comment{}, err
		}
	}
	item.ThreadBlocked = blocked
	if _, err := tx.Exec(ctx,
		`UPDATE comments SET parent_id=$2, thread_blocked=$3 WHERE id=$1`, item.ID, newParent, blocked,
	); err != nil {
		return domain.Comment{}, err
	}
	return item, nil
}

// refreshThreadFlags materializes inherited policy only for a changed subtree.
// The post row is already locked by Moderate; ordinary writes never run this CTE.
func refreshThreadFlags(ctx context.Context, tx pgx.Tx, rootID int64) error {
	_, err := tx.Exec(ctx, `
  WITH RECURSIVE subtree AS (
   SELECT id, post_id, thread_blocked AS inherited,
     (thread_blocked OR locked OR state <> 'VISIBLE') AS blocks_children
   FROM comments WHERE id=$1
   UNION ALL
   SELECT c.id, c.post_id, parent.blocks_children,
     (parent.blocks_children OR c.locked OR c.state <> 'VISIBLE')
   FROM comments c JOIN subtree parent
     ON c.post_id=parent.post_id AND c.parent_id=parent.id
  )
  UPDATE comments c SET thread_blocked=subtree.inherited
  FROM subtree
  WHERE c.id=subtree.id AND c.thread_blocked IS DISTINCT FROM subtree.inherited`, rootID)
	return err
}

func parentEqual(left, right *int64) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}
