package live

import (
	"context"
	"testing"
	"time"

	"github.com/Alexander-D-Karpov/commeng/internal/domain"
)

func TestBrokerIsolationAndCancellation(t *testing.T) {
	b := New()
	ctx, cancel := context.WithCancel(context.Background())
	ch := b.Subscribe(ctx, 12)
	b.Publish(domain.Comment{PostID: 13, ID: 5})
	b.Publish(domain.Comment{PostID: 12, ID: 7})
	select {
	case got := <-ch:
		if got.ID != 7 {
			t.Fatalf("wrong event: %#v", got)
		}
	case <-time.After(time.Second):
		t.Fatal("event not delivered")
	}
	cancel()
	select {
	case _, ok := <-ch:
		if ok {
			t.Fatal("expected closed subscription")
		}
	case <-time.After(time.Second):
		t.Fatal("subscription leak")
	}
}

func TestSlowSubscriberIsDisconnected(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	broker := New()
	_ = broker.Subscribe(ctx, 9)
	for index := 0; index < 100; index++ {
		broker.Publish(domain.Comment{PostID: 9, ID: int64(index + 1)})
	}
	if got := broker.SubscriberCount(9); got != 0 {
		t.Fatalf("slow subscriber left attached: %d", got)
	}
}
