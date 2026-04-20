package scheduler

import (
	"testing"
	"time"

	"aurora/apps/arqo/internal/model"
)

func TestHappyPathDAGFlow(t *testing.T) {
	store := NewStore()
	snapshot, err := store.CreateDemoSession("u1", "summarize logs and email")
	if err != nil {
		t.Fatalf("create session failed: %v", err)
	}

	if got, want := snapshot.DAG.Status, model.DAGStatusRunning; got != want {
		t.Fatalf("unexpected dag status: got=%s want=%s", got, want)
	}

	task1, err := store.PullReadyTask("worker-1", time.Minute)
	if err != nil {
		t.Fatalf("pull failed: %v", err)
	}
	if task1.SkillName != "QueryLog" {
		t.Fatalf("unexpected first task: %s", task1.SkillName)
	}

	if _, err := store.CompleteTask(CompleteTaskInput{
		TaskID:   task1.TaskID,
		WorkerID: "worker-1",
		Success:  true,
		Summary:  "query ok",
		RawData:  map[string]any{"records": 42},
	}); err != nil {
		t.Fatalf("complete task1 failed: %v", err)
	}

	task2, err := store.PullReadyTask("worker-2", time.Minute)
	if err != nil {
		t.Fatalf("pull task2 failed: %v", err)
	}
	if task2.SkillName != "LLMSummarize" {
		t.Fatalf("unexpected second task: %s", task2.SkillName)
	}

	if _, err := store.CompleteTask(CompleteTaskInput{
		TaskID:   task2.TaskID,
		WorkerID: "worker-2",
		Success:  true,
		Summary:  "summary ok",
		RawData:  "summary body",
	}); err != nil {
		t.Fatalf("complete task2 failed: %v", err)
	}

	task3, err := store.PullReadyTask("worker-3", time.Minute)
	if err != nil {
		t.Fatalf("pull task3 failed: %v", err)
	}
	if task3.SkillName != "SendEmail" {
		t.Fatalf("unexpected third task: %s", task3.SkillName)
	}

	if _, err := store.CompleteTask(CompleteTaskInput{
		TaskID:   task3.TaskID,
		WorkerID: "worker-3",
		Success:  true,
		Summary:  "email sent",
		RawData:  map[string]any{"message_id": "msg-1"},
	}); err != nil {
		t.Fatalf("complete task3 failed: %v", err)
	}

	final, err := store.GetSessionSnapshot(snapshot.Session.SessionID)
	if err != nil {
		t.Fatalf("get snapshot failed: %v", err)
	}
	if got, want := final.DAG.Status, model.DAGStatusSuccess; got != want {
		t.Fatalf("unexpected final status: got=%s want=%s", got, want)
	}
}

func TestFailureMovesDAGToReplanning(t *testing.T) {
	store := NewStore()
	snapshot, err := store.CreateDemoSession("u1", "test replanning")
	if err != nil {
		t.Fatalf("create session failed: %v", err)
	}
	task, err := store.PullReadyTask("worker-1", time.Minute)
	if err != nil {
		t.Fatalf("pull failed: %v", err)
	}

	if _, err := store.CompleteTask(CompleteTaskInput{
		TaskID:       task.TaskID,
		WorkerID:     "worker-1",
		Success:      false,
		ErrorCode:    "NETWORK_TIMEOUT",
		ErrorMessage: "upstream timeout",
	}); err != nil {
		t.Fatalf("complete failed: %v", err)
	}

	final, err := store.GetSessionSnapshot(snapshot.Session.SessionID)
	if err != nil {
		t.Fatalf("get snapshot failed: %v", err)
	}
	if got, want := final.DAG.Status, model.DAGStatusReplanning; got != want {
		t.Fatalf("unexpected final status: got=%s want=%s", got, want)
	}
	if got, want := final.DAG.ReplanCount, 1; got != want {
		t.Fatalf("unexpected replan count: got=%d want=%d", got, want)
	}
}
