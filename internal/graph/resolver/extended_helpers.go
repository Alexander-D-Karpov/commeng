package resolver

import (
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"

	"github.com/Alexander-D-Karpov/commeng/internal/domain"
	"github.com/Alexander-D-Karpov/commeng/internal/graph/model"
	"github.com/Alexander-D-Karpov/commeng/internal/pagination"
)

func searchScope(query string) string {
	h := sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace(query))))
	return hex.EncodeToString(h[:])
}

func (r *Resolver) notificationConnection(
	page domain.Page[domain.Notification],
	scope pagination.Cursor,
) *model.NotificationConnection {
	edges := make([]*model.NotificationEdge, 0, len(page.Items))
	for _, n := range page.Items {
		var read *string
		if n.ReadAt != nil {
			x := n.ReadAt.UTC().Format("2006-01-02T15:04:05.999999999Z07:00")
			read = &x
		}
		cursor := scope
		cursor.LastID = n.ID
		edges = append(edges, &model.NotificationEdge{Cursor: r.Cursors.Encode(cursor), Node: &model.Notification{
			ID: domain.ID(n.ID), PostID: domain.ID(n.PostID), CommentID: domain.ID(n.CommentID), Kind: n.Kind,
			CreatedAt: n.CreatedAt.UTC().Format("2006-01-02T15:04:05.999999999Z07:00"), ReadAt: read,
		}})
	}
	info := &model.PageInfo{HasNextPage: page.HasNext, HasPreviousPage: page.HasPrev}
	if len(edges) > 0 {
		start, end := edges[0].Cursor, edges[len(edges)-1].Cursor
		info.StartCursor = &start
		info.EndCursor = &end
	}
	return &model.NotificationConnection{Edges: edges, PageInfo: info}
}
func unreadScope(unread bool) string { return strconv.FormatBool(unread) }

func (r *Resolver) eventConnection(
	page domain.Page[domain.CommentEvent],
	scope pagination.Cursor,
) *model.CommentEventConnection {
	edges := make([]*model.CommentEventEdge, 0, len(page.Items))
	for _, event := range page.Items {
		cursor := scope
		cursor.LastID = event.Sequence
		edges = append(edges, &model.CommentEventEdge{Cursor: r.Cursors.Encode(cursor), Node: &model.CommentEvent{
			Sequence: domain.ID(event.Sequence), Comment: r.asComment(event.Comment),
			CreatedAt: event.CreatedAt.UTC().Format("2006-01-02T15:04:05.999999999Z07:00"),
		}})
	}
	info := &model.PageInfo{HasNextPage: page.HasNext, HasPreviousPage: page.HasPrev}
	if len(edges) > 0 {
		first, last := edges[0].Cursor, edges[len(edges)-1].Cursor
		info.StartCursor = &first
		info.EndCursor = &last
	}
	return &model.CommentEventConnection{Edges: edges, PageInfo: info}
}
