package memory

import (
	"context"
	"slices"
	"sort"
	"time"

	"github.com/Alexander-D-Karpov/commeng/internal/domain"
)

func copyParent(id *int64) *int64 {
	if id == nil {
		return nil
	}
	copyID := *id
	return &copyID
}

func cloneComment(item domain.Comment) domain.Comment {
	if item.ParentID != nil {
		parent := *item.ParentID
		item.ParentID = &parent
	}
	item.UserID = copyParent(item.UserID)
	if item.EditedAt != nil {
		updated := *item.EditedAt
		item.EditedAt = &updated
	}
	return item
}

func parentID(parent *int64) int64 {
	if parent == nil {
		return 0
	}
	return *parent
}
func (s *Store) CreateComment(ctx context.Context, input domain.CommentCreate) (domain.Comment, error) {
	if err := ctx.Err(); err != nil {
		return domain.Comment{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	post, ok := s.posts[input.PostID]
	if !ok {
		return domain.Comment{}, domain.Fault(domain.ErrNotFound, "post not found")
	}
	if !post.CommentsEnabled {
		return domain.Comment{}, domain.Fault(domain.ErrDisabled, "comments are disabled")
	}
	if input.ParentID != nil {
		parent, exists := s.comments[*input.ParentID]
		if !exists || parent.PostID != input.PostID {
			return domain.Comment{}, domain.Fault(domain.ErrInput, "invalid parent")
		}
		if parent.ThreadBlocked || parent.Locked || parent.State != domain.StateVisible {
			return domain.Comment{}, domain.Fault(domain.ErrDisabled, "thread is locked or hidden")
		}
	}
	if input.UserID != nil {
		if _, exists := s.users[*input.UserID]; !exists {
			return domain.Comment{}, domain.Fault(domain.ErrAuth, "user not found")
		}
	}
	s.nextComment++
	comment := domain.Comment{
		ID: s.nextComment, PostID: input.PostID, ParentID: copyParent(input.ParentID), Name: input.Name,
		Email: input.Email, AvatarHash: input.AvatarHash, Body: input.Body,
		UserID:        copyParent(input.UserID),
		IsPostAuthor:  input.UserID != nil && *input.UserID == post.AuthorID,
		ThreadBlocked: false,
		State:         domain.StateVisible, CreatedAt: time.Now().UTC(),
	}
	s.comments[comment.ID] = comment
	key := scope{input.PostID, parentID(input.ParentID)}
	s.children[key] = append(s.children[key], comment.ID)
	if input.ParentID != nil {
		parent := s.comments[*input.ParentID]
		parent.ReplyCount++
		s.comments[parent.ID] = parent
	}
	post.CommentCount++
	post.EventVersion++
	s.posts[post.ID] = post
	s.commentEvents[post.ID] = append(s.commentEvents[post.ID], domain.CommentEvent{
		Sequence: post.EventVersion, PostID: post.ID, Comment: cloneComment(comment), CreatedAt: comment.CreatedAt,
	})
	if input.ParentID != nil {
		parent := s.comments[*input.ParentID]
		if parent.UserID != nil && (input.UserID == nil || *input.UserID != *parent.UserID) {
			s.nextNotification++
			n := domain.Notification{
				ID: s.nextNotification, UserID: *parent.UserID, PostID: input.PostID,
				CommentID: comment.ID, ActorUserID: copyParent(input.UserID), Kind: "REPLY", CreatedAt: comment.CreatedAt,
			}
			s.notifications[n.ID] = n
			s.notificationsByUser[n.UserID] = append(s.notificationsByUser[n.UserID], n.ID)
		}
	}
	return cloneComment(comment), nil
}
func (s *Store) Comment(ctx context.Context, id int64) (domain.Comment, error) {
	if err := ctx.Err(); err != nil {
		return domain.Comment{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	comment, ok := s.comments[id]
	if !ok {
		return domain.Comment{}, domain.Fault(domain.ErrNotFound, "comment not found")
	}
	return cloneComment(comment), nil
}

// commentsPage uses only the sorted child-ID index, never scans the tree.
// Before returns the nearest preceding page in the selected sort order.
func (s *Store) commentsPage(req domain.CommentPageRequest) domain.Page[domain.Comment] {
	ids := s.children[scope{req.PostID, parentID(req.ParentID)}]
	count := len(ids)
	start, end := 0, min(req.First, count)
	if req.Before != nil {
		before := *req.Before
		if req.Sort == domain.SortOldest {
			end = sortSearchGreaterEqual(ids, before)
		} else {
			end = count - sortSearchGreater(ids, before)
		}
		start = max(0, end-req.First)
	} else if req.After != nil {
		after := *req.After
		if req.Sort == domain.SortOldest {
			start = sortSearchGreater(ids, after)
		} else {
			start = count - sortSearchGreaterEqual(ids, after)
		}
		end = min(count, start+req.First)
	}
	page := domain.Page[domain.Comment]{
		Items:   make([]domain.Comment, 0, end-start),
		HasPrev: start > 0,
		HasNext: end < count,
	}
	for position := start; position < end; position++ {
		index := position
		if req.Sort == domain.SortNewest {
			index = count - position - 1
		}
		page.Items = append(page.Items, cloneComment(s.comments[ids[index]]))
	}
	return page
}
func sortSearchGreater(ids []int64, n int64) int {
	return sort.Search(len(ids), func(i int) bool {
		return ids[i] > n
	})
}
func sortSearchGreaterEqual(ids []int64, n int64) int {
	return sort.Search(len(ids), func(i int) bool {
		return ids[i] >= n
	})
}
func (s *Store) Comments(ctx context.Context, req domain.CommentPageRequest) (domain.Page[domain.Comment], error) {
	if req.After != nil && req.Before != nil {
		return domain.Page[domain.Comment]{}, domain.Fault(domain.ErrInput, "use after or before, not both")
	}
	if !domain.ValidSort(req.Sort) {
		return domain.Page[domain.Comment]{}, domain.Fault(domain.ErrInput, "invalid sort")
	}
	if err := domain.ValidatePageSize(req.First); err != nil {
		return domain.Page[domain.Comment]{}, err
	}
	if err := ctx.Err(); err != nil {
		return domain.Page[domain.Comment]{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if _, ok := s.posts[req.PostID]; !ok {
		return domain.Page[domain.Comment]{}, domain.Fault(domain.ErrNotFound, "post not found")
	}
	return s.commentsPage(req), nil
}

func (s *Store) CommentsBatch(
	ctx context.Context,
	reqs []domain.CommentPageRequest,
) ([]domain.Page[domain.Comment], error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]domain.Page[domain.Comment], len(reqs))
	for i, req := range reqs {
		if req.After != nil && req.Before != nil {
			return nil, domain.Fault(domain.ErrInput, "use after or before, not both")
		}
		if !domain.ValidSort(req.Sort) {
			return nil, domain.Fault(domain.ErrInput, "invalid sort")
		}
		if err := domain.ValidatePageSize(req.First); err != nil {
			return nil, err
		}
		if _, ok := s.posts[req.PostID]; !ok {
			return nil, domain.Fault(domain.ErrNotFound, "post not found")
		}
		result[i] = s.commentsPage(req)
	}
	return result, nil
}

func (s *Store) LocateComment(
	ctx context.Context,
	id int64,
	first int,
	order domain.Sort,
) (domain.LocatedComment, error) {
	if !domain.ValidSort(order) {
		return domain.LocatedComment{}, domain.Fault(domain.ErrInput, "invalid sort")
	}
	if err := domain.ValidatePageSize(first); err != nil {
		return domain.LocatedComment{}, err
	}
	if err := ctx.Err(); err != nil {
		return domain.LocatedComment{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	target, ok := s.comments[id]
	if !ok {
		return domain.LocatedComment{}, domain.Fault(domain.ErrNotFound, "comment not found")
	}
	ids := s.children[scope{target.PostID, parentID(target.ParentID)}]
	targetIdx := sortSearchGreaterEqual(ids, id)
	if targetIdx >= len(ids) || ids[targetIdx] != id {
		return domain.LocatedComment{}, domain.Fault(domain.ErrNotFound, "target not in tree")
	}
	rank := targetIdx
	if order == domain.SortNewest {
		rank = len(ids) - targetIdx - 1
	}
	pageNumber := rank/first + 1
	index := rank % first
	items := make([]domain.Comment, 0, first)
	start := rank - index
	for i := start; i < start+first && i < len(ids); i++ {
		itemID := ids[i]
		if order == domain.SortNewest {
			itemID = ids[len(ids)-1-i]
		}
		items = append(items, cloneComment(s.comments[itemID]))
	}
	return domain.LocatedComment{
		Target: cloneComment(target),
		Page: domain.Page[domain.Comment]{
			Items:   items,
			HasNext: start+first < len(ids),
			HasPrev: start > 0,
		},
		PageNumber: int64(pageNumber), IndexInPage: index,
	}, nil
}
func (s *Store) Ancestors(ctx context.Context, id int64, limit int) ([]domain.Comment, error) {
	if limit < 0 || limit > 100 {
		return nil, domain.Fault(domain.ErrInput, "ancestor limit must be 0–100")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	target, ok := s.comments[id]
	if !ok {
		return nil, domain.Fault(domain.ErrNotFound, "comment not found")
	}
	ancestors := make([]domain.Comment, 0, min(limit, 16))
	for current := parentID(target.ParentID); current != 0 && len(ancestors) < limit; {
		item, exists := s.comments[current]
		if !exists {
			return nil, domain.Fault(domain.ErrInput, "broken tree")
		}
		ancestors = append(ancestors, cloneComment(item))
		current = parentID(item.ParentID)
	}
	slices.Reverse(ancestors)
	return ancestors, nil
}

// recalculateThreadFlags runs only when moderation modifies the ancestry or
// lock/visibility of a subtree; ordinary replies are O(1) on their parent.
// Caller holds Store.mu for writing.
func (s *Store) recalculateThreadFlags(rootID int64) {
	queue := []int64{rootID}
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		current := s.comments[id]
		if current.ParentID == nil {
			current.ThreadBlocked = false
		} else {
			parent := s.comments[*current.ParentID]
			current.ThreadBlocked = parent.ThreadBlocked || parent.Locked || parent.State != domain.StateVisible
		}
		s.comments[id] = current
		queue = append(queue, s.children[scope{current.PostID, id}]...)
	}
}

func (s *Store) AncestorsPage(
	ctx context.Context,
	targetID int64,
	before *int64,
	first int,
) (domain.AncestorPage, error) {
	if err := domain.ValidatePageSize(first); err != nil {
		return domain.AncestorPage{}, err
	}
	if err := ctx.Err(); err != nil {
		return domain.AncestorPage{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	target, ok := s.comments[targetID]
	if !ok {
		return domain.AncestorPage{}, domain.Fault(domain.ErrNotFound, "comment not found")
	}
	start := parentID(target.ParentID)
	if before != nil {
		found := false
		for current := start; current != 0; {
			if current == *before {
				found = true
				break
			}
			current = parentID(s.comments[current].ParentID)
		}
		if !found {
			return domain.AncestorPage{}, domain.Fault(domain.ErrInput, "before must be an ancestor of comment")
		}
		start = parentID(s.comments[*before].ParentID)
	}
	page := domain.AncestorPage{Items: make([]domain.Comment, 0, first)}
	for current := start; current != 0; {
		if len(page.Items) == first {
			id := page.Items[len(page.Items)-1].ID
			page.NextBefore = &id
			break
		}
		item := s.comments[current]
		page.Items = append(page.Items, cloneComment(item))
		current = parentID(item.ParentID)
	}
	return page, nil
}
