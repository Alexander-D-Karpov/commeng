package postgres

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/Alexander-D-Karpov/commeng/internal/domain"
	"github.com/Alexander-D-Karpov/commeng/internal/live"
)

// Follower bridges committed PostgreSQL events to the local WebSocket broker.
// A per-post transactionally assigned sequence provides a safe high-water mark
// even when different posts are updated concurrently by separate replicas.
// Events remain queryable through commentEvents after clients reconnect.
type Follower struct {
	store       *Store
	broker      *live.Broker
	mu          sync.Mutex
	checkpoints map[int64]int64
	ready       atomic.Bool
}

func NewFollower(store *Store, broker *live.Broker) *Follower {
	return &Follower{
		store: store, broker: broker,
		checkpoints: make(map[int64]int64),
	}
}

// RegisterTopic starts at the current committed head captured before the
// subscriber was installed. A second subscriber cannot advance the cursor and
// thereby make earlier active subscribers miss an event.
func (f *Follower) RegisterTopic(postID, afterSequence int64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, exists := f.checkpoints[postID]; !exists {
		f.checkpoints[postID] = afterSequence
	}
}

// Run is a long-lived background loop. The caller owns ctx, including shutdown.
// LISTEN/NOTIFY is a wake-up hint, not a data transport. The durable event log
// is polled after reconnect and periodically while connected.
func (f *Follower) Run(ctx context.Context) {
	const reconnectDelay = 2 * time.Second
	for ctx.Err() == nil {
		connection, err := f.store.pool.Acquire(ctx)
		if err != nil {
			slog.WarnContext(ctx, "acquire PostgreSQL event listener failed", "error", err)
			if !sleepContext(ctx, reconnectDelay) {
				return
			}
			continue
		}
		_, err = connection.Exec(ctx, "LISTEN commeng_events")
		if err != nil {
			connection.Release()
			slog.WarnContext(ctx, "LISTEN commeng_events failed", "error", err)
			if !sleepContext(ctx, reconnectDelay) {
				return
			}
			continue
		}
		f.ready.Store(true)
		f.receive(ctx, connection.Conn())
		f.ready.Store(false)
		connection.Release()
		if !sleepContext(ctx, reconnectDelay) {
			return
		}
	}
}

// Receive a notification after each poll, periodically re-polling even if
// the notification channel has been interrupted or no new wake-up arrives.
// The LISTEN connection only waits for notifications; actual event payloads
// are retrieved through the normal pool, so the long-lived wait is never
// cancelled just to enforce a periodic poll (which could poison a pgx conn).
func (f *Follower) receive(ctx context.Context, conn *pgx.Conn) {
	waitCtx, cancel := context.WithCancel(ctx)
	wake := make(chan error, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for waitCtx.Err() == nil {
			_, err := conn.WaitForNotification(waitCtx)
			select {
			case wake <- err:
			case <-waitCtx.Done():
				return
			}
			if err != nil {
				return
			}
		}
	}()
	// Do not return the connection to the pool while WaitForNotification is
	// still using it. Canceling a pgx read can close that connection, which
	// is safe here because the pool discards broken connections.
	defer func() {
		cancel()
		<-done
	}()
	poll := time.NewTicker(3 * time.Second)
	defer poll.Stop()
	for ctx.Err() == nil {
		if err := f.flush(ctx); err != nil {
			slog.WarnContext(ctx, "replay committed events failed", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-poll.C:
			// Notifications can be lost while reconnecting. Always re-read.
		case err := <-wake:
			if err != nil {
				slog.WarnContext(ctx, "PostgreSQL event listener disconnected", "error", err)
				return
			}
		}
	}
}

func sleepContext(ctx context.Context, duration time.Duration) bool {
	select {
	case <-ctx.Done():
		return false
	case <-time.After(duration):
		return true
	}
}

func (f *Follower) flush(ctx context.Context) error {
	active := f.broker.ActivePosts()
	allowed := make(map[int64]struct{}, len(active))
	for _, postID := range active {
		allowed[postID] = struct{}{}
	}
	f.mu.Lock()
	for postID := range f.checkpoints {
		if _, exists := allowed[postID]; !exists && f.broker.SubscriberCount(postID) == 0 {
			// ActivePosts is a snapshot. A fresh subscriber could have
			// registered since it was taken; never discard that cursor.
			delete(f.checkpoints, postID)
		}
	}
	f.mu.Unlock()
	if len(active) == 0 {
		return nil
	}
	for _, postID := range active {
		f.mu.Lock()
		last, registered := f.checkpoints[postID]
		f.mu.Unlock()
		if !registered {
			continue
		}
		for {
			page, err := f.store.CommentEvents(ctx, postID, last, 100)
			if err != nil {
				if domain.HasCode(err, domain.ErrHistoryExpired) {
					// A manual retention purge removed the listener's checkpoint.
					// Current subscribers are best-effort; readers reconcile by
					// re-querying the authoritative comment tree.
					head, headErr := f.store.CommentEventHead(ctx, postID)
					if headErr != nil {
						return headErr
					}
					f.mu.Lock()
					if _, exists := f.checkpoints[postID]; exists {
						f.checkpoints[postID] = head
					}
					f.mu.Unlock()
					slog.WarnContext(
						ctx,
						"subscription event checkpoint expired; clients must reconcile",
						"post_id",
						postID,
					)
					break
				}
				return err
			}
			for _, event := range page.Items {
				f.broker.Publish(event.Comment)
				last = event.Sequence
				f.mu.Lock()
				if stored, ok := f.checkpoints[postID]; ok && stored < last {
					f.checkpoints[postID] = last
				}
				f.mu.Unlock()
			}
			if !page.HasNext {
				break
			}
		}
	}
	return nil
}

// Ready indicates whether this replica has an active PostgreSQL LISTEN session.
func (f *Follower) Ready() bool { return f.ready.Load() }
