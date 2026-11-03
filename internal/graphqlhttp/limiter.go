package graphqlhttp

import (
	"sync"
	"time"
)

type limiter struct {
	mu      sync.Mutex
	entries map[string]window
	count   uint64
	enabled bool
}
type window struct {
	start time.Time
	hits  int
}

func newLimiter() *limiter {
	return &limiter{entries: make(map[string]window), enabled: true}
}

// disable is called only during server initialization in a loopback test deployment.
func (l *limiter) disable() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.enabled = false
}

func (l *limiter) allow(key string, limit int) bool {
	return l.allowAt(key, limit, time.Now())
}

// allowAt makes window rollover deterministic and testable.
func (l *limiter) allowAt(key string, limit int, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.enabled {
		return true
	}
	current := l.entries[key]
	if now.Sub(current.start) >= time.Minute {
		current = window{start: now}
	}
	if current.hits >= limit {
		return false
	}
	current.hits++
	l.entries[key] = current
	l.count++
	if l.count%512 == 0 {
		for key, value := range l.entries {
			if now.Sub(value.start) > 2*time.Minute {
				delete(l.entries, key)
			}
		}
	}
	return true
}
