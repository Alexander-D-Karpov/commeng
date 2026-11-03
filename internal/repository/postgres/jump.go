package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/Alexander-D-Karpov/commeng/internal/domain"
)

func (s *Store) LocateComment(
	ctx context.Context,
	id int64,
	first int,
	sort domain.Sort,
) (domain.LocatedComment, error) {
	if err := domain.ValidatePageSize(first); err != nil {
		return domain.LocatedComment{}, err
	}
	direction, comparison, err := orderSQL(sort)
	if err != nil {
		return domain.LocatedComment{}, err
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return domain.LocatedComment{}, err
	}
	defer func() {
		_ = tx.Rollback(context.Background())
	}()
	target, err := scanComment(tx.QueryRow(ctx, `SELECT `+commentColumns+` FROM comments WHERE id=$1`, id))
	if err != nil {
		return domain.LocatedComment{}, err
	}
	predicate, extra := scopeWhere(target.ParentID)
	args := append([]any{target.PostID}, extra...)
	args = append(args, id)
	var rank int64
	// In NEWEST ordering, entries with a higher ID precede this comment.
	reverse := ">"
	if sort == domain.SortOldest {
		reverse = "<"
	}
	query := fmt.Sprintf("SELECT count(*) FROM comments WHERE %s AND id %s $%d", predicate, reverse, len(args))
	if err := tx.QueryRow(ctx, query, args...).Scan(&rank); err != nil {
		return domain.LocatedComment{}, err
	}
	pageNumber := rank/int64(first) + 1
	index := int(rank % int64(first))
	// Walk at most first (<=100) IDs toward the first element of the target's page.
	// A potentially huge OFFSET is never used.
	pivotComparison := ">="
	pivotOrder := "ASC"
	if sort == domain.SortOldest {
		pivotComparison = "<="
		pivotOrder = "DESC"
	}
	pivotSQL := fmt.Sprintf(`
        SELECT id FROM comments WHERE %s AND id %s $%d
        ORDER BY id %s LIMIT $%d`,
		predicate, pivotComparison, len(args), pivotOrder, len(args)+1,
	)
	pivotArgs := append(append([]any(nil), args...), index+1)
	pivotRows, err := tx.Query(ctx, pivotSQL, pivotArgs...)
	if err != nil {
		return domain.LocatedComment{}, err
	}
	var pivot int64
	var seen int
	for pivotRows.Next() {
		if err := pivotRows.Scan(&pivot); err != nil {
			pivotRows.Close()
			return domain.LocatedComment{}, err
		}
		seen++
	}
	err = pivotRows.Err()
	pivotRows.Close()
	if err != nil {
		return domain.LocatedComment{}, err
	}
	if seen != index+1 {
		return domain.LocatedComment{}, domain.Fault(domain.ErrConflict, "tree changed")
	}
	// Inclusive pivot: first page item is included. All page queries are indexable.
	args = append(args[:len(args)-1], pivot, first+1)
	inclusive := "<="
	if comparison == ">" {
		inclusive = ">="
	}
	pageSQL := fmt.Sprintf(`
        SELECT %s FROM comments WHERE %s AND id %s $%d
        ORDER BY id %s LIMIT $%d`,
		commentColumns, predicate, inclusive, len(args)-1, direction, len(args),
	)
	rows, err := tx.Query(ctx, pageSQL, args...)
	if err != nil {
		return domain.LocatedComment{}, err
	}
	page := domain.Page[domain.Comment]{Items: make([]domain.Comment, 0, first), HasPrev: pageNumber > 1}
	for rows.Next() {
		var c domain.Comment
		if err := rows.Scan(commentScanFields(&c)...); err != nil {
			rows.Close()
			return domain.LocatedComment{}, err
		}
		if len(page.Items) == first {
			page.HasNext = true
			break
		}
		page.Items = append(page.Items, c)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return domain.LocatedComment{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.LocatedComment{}, err
	}
	return domain.LocatedComment{Target: target, Page: page, PageNumber: pageNumber, IndexInPage: index}, nil
}
