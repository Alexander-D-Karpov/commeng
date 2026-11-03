package live

import (
	"context"
	"sync"

	"github.com/Alexander-D-Karpov/commeng/internal/domain"
)

// Broker is an intentionally ephemeral, one-process event fanout. The database
// stays authoritative; slow readers are disconnected and must resync by Query.
type Broker struct {
	mu          sync.Mutex
	next        uint64
	subscribers map[int64]map[uint64]chan domain.Comment
}

func New() *Broker {
	return &Broker{subscribers: make(map[int64]map[uint64]chan domain.Comment)}
}
func (b *Broker) Subscribe(ctx context.Context, postID int64) <-chan domain.Comment {
	b.mu.Lock()
	b.next++
	id := b.next
	events := make(chan domain.Comment, 64)
	if b.subscribers[postID] == nil {
		b.subscribers[postID] = make(map[uint64]chan domain.Comment)
	}
	b.subscribers[postID][id] = events
	b.mu.Unlock()
	go func() {
		<-ctx.Done()
		b.mu.Lock()
		defer b.mu.Unlock()
		if _, exists := b.subscribers[postID][id]; exists {
			delete(b.subscribers[postID], id)
			close(events)
			if len(b.subscribers[postID]) == 0 {
				delete(b.subscribers, postID)
			}
		}
	}()
	return events
}
func (b *Broker) Publish(comment domain.Comment) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for id, ch := range b.subscribers[comment.PostID] {
		select {
		case ch <- comment:
		default:
			close(ch)
			delete(b.subscribers[comment.PostID], id)
		}
	}
	if len(b.subscribers[comment.PostID]) == 0 {
		delete(b.subscribers, comment.PostID)
	}
}

// SubscriberCount is a small diagnostic for tests and monitoring.
func (b *Broker) SubscriberCount(postID int64) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.subscribers[postID])
}

// ActivePosts returns a snapshot of post IDs with live subscribers.
func (b *Broker) ActivePosts() []int64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	ids := make([]int64, 0, len(b.subscribers))
	for id, subscribers := range b.subscribers {
		if len(subscribers) > 0 {
			ids = append(ids, id)
		}
	}
	return ids
}

// TotalSubscribers counts all active local listeners for Prometheus gauges.
func (b *Broker) TotalSubscribers() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	total := 0
	for _, topic := range b.subscribers {
		total += len(topic)
	}
	return total
}
