package graphqlhttp

import (
	"encoding/json"
	"net/http"

	"github.com/Alexander-D-Karpov/commeng/internal/domain"
)

func (s *Server) permalink(w http.ResponseWriter, r *http.Request) {
	postID, err := domain.ParseID(r.PathValue("post"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	commentID, err := domain.ParseID(r.PathValue("comment"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	location, err := s.service.LocateComment(r.Context(), commentID, 20, domain.SortNewest)
	if err != nil || location.Target.PostID != postID {
		http.NotFound(w, r)
		return
	}
	public := func(c domain.Comment) map[string]any {
		body := any(c.Body)
		name := c.Name
		if c.State != domain.StateVisible {
			body = nil
			name = "Hidden comment"
		}
		return map[string]any{"id": domain.ID(c.ID), "body": body, "name": name, "state": c.State}
	}
	page := make([]map[string]any, 0, len(location.Page.Items))
	for _, c := range location.Page.Items {
		page = append(page, public(c))
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"target": public(location.Target), "page": page,
		"pageNumber": location.PageNumber, "indexInPage": location.IndexInPage,
		"hasNextPage": location.Page.HasNext, "hasPreviousPage": location.Page.HasPrev,
	})
}
func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
