package events

import (
	"sync"
	"time"
)

type Event struct {
	SessionID string    `json:"session_id"`
	EventType string    `json:"event_type"`
	TaskID    string    `json:"task_id,omitempty"`
	Message   string    `json:"message"`
	Source    string    `json:"source,omitempty"`
	At        time.Time `json:"at"`
}

type Broker struct {
	mu          sync.RWMutex
	subscribers map[string]map[chan Event]struct{}
}

func NewBroker() *Broker {
	return &Broker{
		subscribers: make(map[string]map[chan Event]struct{}),
	}
}

func (b *Broker) Subscribe(sessionID string) (<-chan Event, func()) {
	ch := make(chan Event, 32)

	b.mu.Lock()
	if _, ok := b.subscribers[sessionID]; !ok {
		b.subscribers[sessionID] = make(map[chan Event]struct{})
	}
	b.subscribers[sessionID][ch] = struct{}{}
	b.mu.Unlock()

	cancel := func() {
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

	return ch, cancel
}

func (b *Broker) Publish(evt Event) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	subs, ok := b.subscribers[evt.SessionID]
	if !ok {
		return
	}
	for ch := range subs {
		select {
		case ch <- evt:
		default:
			// Drop when subscriber is too slow to avoid blocking publish path.
		}
	}
}
