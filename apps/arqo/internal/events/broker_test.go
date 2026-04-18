package events

import (
	"testing"
	"time"
)

func TestPublishAndSubscribe(t *testing.T) {
	broker := NewBroker()
	ch, cancel := broker.Subscribe("sess_1")
	defer cancel()

	evt := Event{
		SessionID: "sess_1",
		EventType: "NODE_PROGRESS",
		TaskID:    "task_1",
		Message:   "running",
		At:        time.Now().UTC(),
	}
	broker.Publish(evt)

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
	broker := NewBroker()
	ch, cancel := broker.Subscribe("sess_2")
	defer cancel()

	broker.Publish(Event{SessionID: "sess_1", EventType: "NODE_PROGRESS", Message: "hello", At: time.Now().UTC()})

	select {
	case <-ch:
		t.Fatal("unexpected event for different session")
	case <-time.After(120 * time.Millisecond):
	}
}
