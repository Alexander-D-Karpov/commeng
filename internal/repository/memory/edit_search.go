package memory

import (
	"context"
	"strings"
	"time"
	"unicode"

	"github.com/Alexander-D-Karpov/commeng/internal/domain"
)

// MatchesQuery uses Unicode-aware case folding; every query term must match.
// PostgreSQL uses a proper indexed full-text vector instead of this RAM scan.
func tokenize(value string) []string {
	pieces := strings.FieldsFunc(strings.ToLower(value), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsNumber(r)
	})
	return pieces
}
func matchesQuery(query, value string) bool {
	haystack := make(map[string]struct{})
	for _, term := range tokenize(value) {
		haystack[term] = struct{}{}
	}
	for _, term := range tokenize(query) {
		if _, ok := haystack[term]; !ok {
			return false
		}
	}
	return true
}

func (s *Store) EditPost(ctx context.Context, postID, actorID int64, title, body string) (domain.Post, error) {
	if err := ctx.Err(); err != nil {
		return domain.Post{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	post, ok := s.posts[postID]
	if !ok {
		return domain.Post{}, domain.Fault(domain.ErrNotFound, "post not found")
	}
	if post.AuthorID != actorID {
		return domain.Post{}, domain.Fault(domain.ErrForbidden, "only author can edit post")
	}
	if post.Deleted {
		return domain.Post{}, domain.Fault(domain.ErrConflict, "post was deleted")
	}
	post.Title, post.Body, post.UpdatedAt = title, body, time.Now().UTC()
	s.posts[postID] = post
	return post, nil
}

func (s *Store) DeletePost(ctx context.Context, postID, actorID int64) (domain.Post, error) {
	if err := ctx.Err(); err != nil {
		return domain.Post{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	post, ok := s.posts[postID]
	if !ok {
		return domain.Post{}, domain.Fault(domain.ErrNotFound, "post not found")
	}
	if post.AuthorID != actorID {
		return domain.Post{}, domain.Fault(domain.ErrForbidden, "only author can delete post")
	}
	if !post.Deleted {
		post.Deleted, post.CommentsEnabled = true, false
		post.Title, post.Body = "[deleted]", "[deleted]"
		post.UpdatedAt = time.Now().UTC()
		post.TreeVersion++
		s.posts[postID] = post
	}
	return post, nil
}

func (s *Store) SearchPosts(
	ctx context.Context,
	query string,
	after *int64,
	first int,
) (domain.Page[domain.Post], error) {
	if err := domain.ValidatePageSize(first); err != nil {
		return domain.Page[domain.Post]{}, err
	}
	if err := ctx.Err(); err != nil {
		return domain.Page[domain.Post]{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	page := domain.Page[domain.Post]{Items: make([]domain.Post, 0, first)}
	start := len(s.postIDs) - 1
	if after != nil {
		start = sortSearchGreaterEqual(s.postIDs, *after) - 1
	}
	for i := start; i >= 0; i-- {
		post := s.posts[s.postIDs[i]]
		if post.Deleted || !matchesQuery(query, post.Title+" "+post.Body) {
			continue
		}
		if len(page.Items) == first {
			page.HasNext = true
			break
		}
		page.Items = append(page.Items, post)
	}
	if after != nil && len(page.Items) > 0 {
		page.HasPrev = true
	}
	return page, nil
}

func (s *Store) EditComment(ctx context.Context, commentID, actorID int64, body string) (domain.Comment, error) {
	if err := ctx.Err(); err != nil {
		return domain.Comment{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	comment, ok := s.comments[commentID]
	if !ok {
		return domain.Comment{}, domain.Fault(domain.ErrNotFound, "comment not found")
	}
	if comment.UserID == nil || *comment.UserID != actorID {
		return domain.Comment{}, domain.Fault(domain.ErrForbidden, "only authenticated comment author can edit")
	}
	if comment.State != domain.StateVisible {
		return domain.Comment{}, domain.Fault(domain.ErrConflict, "comment is not visible")
	}
	if s.posts[comment.PostID].Deleted {
		return domain.Comment{}, domain.Fault(domain.ErrConflict, "post was deleted")
	}
	now := time.Now().UTC()
	comment.Body, comment.EditedAt = body, &now
	s.comments[commentID] = comment
	return cloneComment(comment), nil
}

func (s *Store) DeleteComment(ctx context.Context, commentID, actorID int64) (domain.Comment, error) {
	if err := ctx.Err(); err != nil {
		return domain.Comment{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	comment, ok := s.comments[commentID]
	if !ok {
		return domain.Comment{}, domain.Fault(domain.ErrNotFound, "comment not found")
	}
	if comment.UserID == nil || *comment.UserID != actorID {
		return domain.Comment{}, domain.Fault(domain.ErrForbidden, "only authenticated comment author can delete")
	}
	if comment.State != domain.StateDeleted {
		comment.State = domain.StateDeleted
		comment.Name, comment.Email, comment.Body = "[deleted]", "deleted@invalid.example", "[deleted]"
		comment.AvatarHash = strings.Repeat("0", 64)
		comment.UserID = nil
		now := time.Now().UTC()
		comment.EditedAt = &now
		s.comments[commentID] = comment
		post := s.posts[comment.PostID]
		post.TreeVersion++
		s.posts[comment.PostID] = post
		s.recalculateThreadFlags(commentID)
	}
	return cloneComment(comment), nil
}

func (s *Store) SearchComments(
	ctx context.Context,
	query string,
	postID, after *int64,
	first int,
) (domain.Page[domain.Comment], error) {
	if err := domain.ValidatePageSize(first); err != nil {
		return domain.Page[domain.Comment]{}, err
	}
	if err := ctx.Err(); err != nil {
		return domain.Page[domain.Comment]{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	page := domain.Page[domain.Comment]{Items: make([]domain.Comment, 0, first)}
	for id := s.nextComment; id > 0; id-- {
		if after != nil && id >= *after {
			continue
		}
		comment, ok := s.comments[id]
		if !ok || comment.State != domain.StateVisible || comment.ThreadBlocked {
			continue
		}
		if postID != nil && comment.PostID != *postID {
			continue
		}
		if s.posts[comment.PostID].Deleted {
			continue
		}
		if !matchesQuery(query, comment.Body) {
			continue
		}
		if len(page.Items) == first {
			page.HasNext = true
			break
		}
		page.Items = append(page.Items, cloneComment(comment))
	}
	if after != nil && len(page.Items) > 0 {
		page.HasPrev = true
	}
	return page, nil
}
