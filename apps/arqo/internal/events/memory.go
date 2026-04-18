package events

import (
	"context"
	"sync"
)

type MemoryBroker struct {
	mu          sync.RWMutex
	subscribers map[string]map[chan Event]struct{}
}

func NewMemoryBroker() *MemoryBroker {
	return &MemoryBroker{subscribers: make(map[string]map[chan Event]struct{})}
}

func (b *MemoryBroker) Subscribe(ctx context.Context, sessionID string) (<-chan Event, error) {
	ch := make(chan Event, 32)

	b.mu.Lock()
	if _, ok := b.subscribers[sessionID]; !ok {
		b.subscribers[sessionID] = make(map[chan Event]struct{})
	}
	b.subscribers[sessionID][ch] = struct{}{}
	b.mu.Unlock()

	go func() {
		<-ctx.Done()
		b.unsubscribe(sessionID, ch)
	}()

	return ch, nil
}

func (b *MemoryBroker) Publish(_ context.Context, evt Event) error {
	b.mu.RLock()
	defer b.mu.RUnlock()

	subs, ok := b.subscribers[evt.SessionID]
	if !ok {
		return nil
	}
	for ch := range subs {
		select {
		case ch <- evt:
		default:
			// Drop when subscriber is too slow to avoid blocking publish path.
		}
	}
	return nil
}

func (b *MemoryBroker) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()

	for sessionID := range b.subscribers {
		for ch := range b.subscribers[sessionID] {
			close(ch)
		}
		delete(b.subscribers, sessionID)
	}
	return nil
}

func (b *MemoryBroker) unsubscribe(sessionID string, ch chan Event) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if _, ok := b.subscribers[sessionID][ch]; ok {
		delete(b.subscribers[sessionID], ch)
		close(ch)
	}
	if len(b.subscribers[sessionID]) == 0 {
		delete(b.subscribers, sessionID)
	}
}
