package resolver

import (
	"context"
	"time"

	"github.com/vikstrous/dataloadgen"

	"github.com/Alexander-D-Karpov/commeng/internal/domain"
	"github.com/Alexander-D-Karpov/commeng/internal/pagination"
	"github.com/Alexander-D-Karpov/commeng/internal/service"
)

type Resolver struct {
	Service *service.Service
	Cursors *pagination.Signer
}

type repliesKey struct {
	PostID   int64
	ParentID int64 // zero for roots
	AfterID  int64 // zero for the first page
	BeforeID int64 // zero unless paging backward
	First    int
	Sort     domain.Sort
}
type loaders struct {
	Users   *dataloadgen.Loader[int64, domain.User]
	Replies *dataloadgen.Loader[repliesKey, domain.Page[domain.Comment]]
	Likes   *dataloadgen.Loader[int64, bool]
}
type contextKey struct{}

// gqlgen schedules sibling fields concurrently. An 8ms coalescing window
// leaves room for scheduling under -race, without querying the repository
// once per node. Keep this bound to a single GraphQL response/event.
const loaderWait = 8 * time.Millisecond
const loaderBatchSize = 256

func WithLoaders(ctx context.Context, svc *service.Service, actorKey string) context.Context {
	l := &loaders{}
	l.Users = dataloadgen.NewLoader(func(ctx context.Context, keys []int64) ([]domain.User, []error) {
		records, err := svc.Users(ctx, keys)
		values, errs := make([]domain.User, len(keys)), make([]error, len(keys))
		for i, key := range keys {
			values[i] = records[key]
			errs[i] = err
		}
		return values, errs
	}, dataloadgen.WithWait(loaderWait), dataloadgen.WithBatchCapacity(loaderBatchSize))
	l.Replies = dataloadgen.NewLoader(
		func(ctx context.Context, keys []repliesKey) ([]domain.Page[domain.Comment], []error) {
			requests := make([]domain.CommentPageRequest, len(keys))
			for i, key := range keys {
				var parent, after *int64
				if key.ParentID != 0 {
					parent = &key.ParentID
				}
				if key.AfterID != 0 {
					after = &key.AfterID
				}
				var before *int64
				if key.BeforeID != 0 {
					before = &key.BeforeID
				}
				requests[i] = domain.CommentPageRequest{
					PostID: key.PostID, ParentID: parent, After: after, Before: before,
					First: key.First, Sort: key.Sort,
				}
			}
			result, err := svc.CommentsBatch(ctx, requests)
			errs := make([]error, len(keys))
			if err != nil {
				result = make([]domain.Page[domain.Comment], len(keys))
				for i := range errs {
					errs[i] = err
				}
			}
			return result, errs
		},
		dataloadgen.WithWait(loaderWait),
		dataloadgen.WithBatchCapacity(loaderBatchSize),
	)
	l.Likes = dataloadgen.NewLoader(func(ctx context.Context, keys []int64) ([]bool, []error) {
		values := make([]bool, len(keys))
		errs := make([]error, len(keys))
		if actorKey == "" {
			return values, errs
		}
		likes, err := svc.HasLikes(ctx, actorKey, keys)
		for i, id := range keys {
			values[i] = likes[id]
			errs[i] = err
		}
		return values, errs
	}, dataloadgen.WithWait(loaderWait), dataloadgen.WithBatchCapacity(loaderBatchSize))
	return context.WithValue(ctx, contextKey{}, l)
}
func getLoaders(ctx context.Context) *loaders {
	l, _ := ctx.Value(contextKey{}).(*loaders)
	return l
}
