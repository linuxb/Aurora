package events

import (
	"context"
	"testing"
	"time"
)

func TestPublishAndSubscribe(t *testing.T) {
	broker := NewMemoryBroker()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ch, err := broker.Subscribe(ctx, "sess_1")
	if err != nil {
		t.Fatalf("subscribe failed: %v", err)
	}

	evt := Event{
		SessionID: "sess_1",
		EventType: "NODE_PROGRESS",
		TaskID:    "task_1",
		Message:   "running",
		At:        time.Now().UTC(),
	}
	if err := broker.Publish(context.Background(), evt); err != nil {
		t.Fatalf("publish failed: %v", err)
	}

	select {
	case got := <-ch:
		if got.TaskID != "task_1" {
			t.Fatalf("unexpected task id: %s", got.TaskID)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for event")
	}
}

func TestSessionIsolation(t *testing.T) {
	broker := NewMemoryBroker()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ch, err := broker.Subscribe(ctx, "sess_2")
	if err != nil {
		t.Fatalf("subscribe failed: %v", err)
	}

	if err := broker.Publish(context.Background(), Event{SessionID: "sess_1", EventType: "NODE_PROGRESS", Message: "hello", At: time.Now().UTC()}); err != nil {
		t.Fatalf("publish failed: %v", err)
	}

	select {
	case <-ch:
		t.Fatal("unexpected event for different session")
	case <-time.After(120 * time.Millisecond):
	}
}
