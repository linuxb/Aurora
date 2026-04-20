package scheduler

import (
	"sync"
	"sync/atomic"
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

func TestConcurrentPullPreventsDuplicateLease(t *testing.T) {
	store := NewStore()
	snapshot, err := store.CreateDemoSession("u-concurrency", "lease test")
	if err != nil {
		t.Fatalf("create session failed: %v", err)
	}

	var wg sync.WaitGroup
	const workers = 16
	var successCount atomic.Int32
	taskIDs := make(chan string, workers)

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			task, err := store.PullReadyTask("worker-"+time.Now().Format("150405.000")+string(rune('a'+worker)), time.Minute)
			if err == nil {
				successCount.Add(1)
				taskIDs <- task.TaskID
				return
			}
			if err != ErrNoReadyTask {
				t.Errorf("unexpected pull error: %v", err)
			}
		}(i)
	}
	wg.Wait()
	close(taskIDs)

	if got := successCount.Load(); got != 1 {
		t.Fatalf("expected exactly one successful lease, got=%d", got)
	}
	for taskID := range taskIDs {
		if taskID == "" {
			t.Fatal("leased task id is empty")
		}
	}

	final, err := store.GetSessionSnapshot(snapshot.Session.SessionID)
	if err != nil {
		t.Fatalf("snapshot failed: %v", err)
	}
	runningCount := 0
	for _, task := range final.Tasks {
		if task.Status == model.TaskStatusRunning {
			runningCount++
		}
	}
	if runningCount != 1 {
		t.Fatalf("expected 1 running task after concurrent pull, got=%d", runningCount)
	}
}

func TestConcurrentCompleteNoDependencyUnderflow(t *testing.T) {
	store := NewStore()
	snapshot, err := store.CreateDemoSession("u-concurrency", "complete test")
	if err != nil {
		t.Fatalf("create session failed: %v", err)
	}

	task, err := store.PullReadyTask("worker-1", time.Minute)
	if err != nil {
		t.Fatalf("pull failed: %v", err)
	}

	const attempts = 10
	var wg sync.WaitGroup
	var successCount atomic.Int32

	for i := 0; i < attempts; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := store.CompleteTask(CompleteTaskInput{
				TaskID:   task.TaskID,
				WorkerID: "worker-1",
				Success:  true,
				Summary:  "ok",
				RawData:  map[string]any{"k": "v"},
			}); err == nil {
				successCount.Add(1)
			} else if err != ErrTaskNotRunnable {
				t.Errorf("unexpected complete error: %v", err)
			}
		}()
	}
	wg.Wait()

	if got := successCount.Load(); got != 1 {
		t.Fatalf("expected exactly one successful complete, got=%d", got)
	}

	final, err := store.GetSessionSnapshot(snapshot.Session.SessionID)
	if err != nil {
		t.Fatalf("snapshot failed: %v", err)
	}

	for _, tsk := range final.Tasks {
		if tsk.PendingDependenciesCount < 0 {
			t.Fatalf("pending_dependencies_count underflow detected on task=%s count=%d", tsk.TaskID, tsk.PendingDependenciesCount)
		}
		if tsk.SkillName == "LLMSummarize" {
			if tsk.PendingDependenciesCount != 0 {
				t.Fatalf("expected LLMSummarize dependency count=0, got=%d", tsk.PendingDependenciesCount)
			}
			if tsk.Status != model.TaskStatusReady {
				t.Fatalf("expected LLMSummarize status READY, got=%s", tsk.Status)
			}
		}
	}
}
