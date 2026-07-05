package events

import (
	"context"
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

type Broker interface {
	Subscribe(ctx context.Context, sessionID string) (<-chan Event, error)
	Publish(ctx context.Context, evt Event) error
	Close() error
}
