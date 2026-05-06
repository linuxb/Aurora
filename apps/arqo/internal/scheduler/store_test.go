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

func TestJITExpansionRedirectsDownstreamAndReadiesLeaves(t *testing.T) {
	store := NewStore()
	snapshot, err := store.CreateJITDemoSession("u-jit", "investigate payment incident")
	if err != nil {
		t.Fatalf("create jit session failed: %v", err)
	}

	planner, err := store.PullReadyTask("planner-worker", time.Minute)
	if err != nil {
		t.Fatalf("pull planner failed: %v", err)
	}
	if planner.SkillName != "ReActPlanner" {
		t.Fatalf("expected planner task, got=%s", planner.SkillName)
	}

	_, err = store.CompleteTask(CompleteTaskInput{
		TaskID:   planner.TaskID,
		WorkerID: "planner-worker",
		Success:  true,
		Summary:  "expanded",
		RawData:  map[string]any{"decision": "expand"},
		ExpansionPayload: &ExpansionPayload{
			Reasoning:     "need parallel collection",
			MappingStatus: ExpansionMappingMapped,
			NewNodes: []ExpansionNode{
				{
					NodeID:       "dyn_collect_a",
					NodeType:     model.NodeTypeSkillSink,
					SkillName:    "QueryLog",
					Dependencies: []string{planner.TaskID},
				},
				{
					NodeID:       "dyn_collect_b",
					NodeType:     model.NodeTypeSkillSink,
					SkillName:    "QueryLog",
					Dependencies: []string{planner.TaskID},
				},
				{
					NodeID:       "dyn_summary",
					NodeType:     model.NodeTypeSkillSink,
					SkillName:    "LLMSummarize",
					Dependencies: []string{"dyn_collect_a", "dyn_collect_b"},
				},
			},
			DownstreamWiring: DownstreamWiring{
				RedirectFrom: planner.TaskID,
				RedirectTo:   []string{"dyn_summary"},
			},
		},
	})
	if err != nil {
		t.Fatalf("complete with expansion failed: %v", err)
	}

	final, err := store.GetSessionSnapshot(snapshot.Session.SessionID)
	if err != nil {
		t.Fatalf("snapshot failed: %v", err)
	}

	tasksBySkill := make(map[string][]model.Task)
	tasksByID := make(map[string]model.Task)
	for _, task := range final.Tasks {
		tasksBySkill[task.SkillName] = append(tasksBySkill[task.SkillName], task)
		tasksByID[task.TaskID] = task
	}

	if got, want := final.DAG.CurrentDepth, 2; got != want {
		t.Fatalf("unexpected dag depth: got=%d want=%d", got, want)
	}
	if tasksByID["dyn_collect_a"].Status != model.TaskStatusReady {
		t.Fatalf("expected dyn_collect_a READY, got=%s", tasksByID["dyn_collect_a"].Status)
	}
	if tasksByID["dyn_collect_b"].Status != model.TaskStatusReady {
		t.Fatalf("expected dyn_collect_b READY, got=%s", tasksByID["dyn_collect_b"].Status)
	}
	if tasksByID["dyn_summary"].PendingDependenciesCount != 2 {
		t.Fatalf("expected dyn_summary pending count=2, got=%d", tasksByID["dyn_summary"].PendingDependenciesCount)
	}

	finalTasks := tasksBySkill["SendEmail"]
	if len(finalTasks) != 1 {
		t.Fatalf("expected one final SendEmail task, got=%d", len(finalTasks))
	}
	if got := finalTasks[0].Dependencies; len(got) != 1 || got[0] != "dyn_summary" {
		t.Fatalf("expected final task dependency redirected to dyn_summary, got=%v", got)
	}
	if got := tasksByID["dyn_summary"].Children; len(got) != 1 || got[0] != finalTasks[0].TaskID {
		t.Fatalf("expected dyn_summary to wake final task, got children=%v", got)
	}

	leasedDynamic := make(map[string]struct{})
	for i := 0; i < 2; i++ {
		task, err := store.PullReadyTask("dynamic-worker", time.Minute)
		if err != nil {
			t.Fatalf("pull dynamic task failed: %v", err)
		}
		if task.TaskID != "dyn_collect_a" && task.TaskID != "dyn_collect_b" {
			t.Fatalf("unexpected dynamic task lease: %s", task.TaskID)
		}
		if _, duplicate := leasedDynamic[task.TaskID]; duplicate {
			t.Fatalf("duplicate dynamic task lease: %s", task.TaskID)
		}
		leasedDynamic[task.TaskID] = struct{}{}
		if _, err := store.CompleteTask(CompleteTaskInput{
			TaskID:   task.TaskID,
			WorkerID: task.OwnerID,
			Success:  true,
			Summary:  "dynamic node done",
			RawData:  map[string]any{"task_id": task.TaskID},
		}); err != nil {
			t.Fatalf("complete dynamic task failed: %v", err)
		}
	}

	summaryTask, err := store.PullReadyTask("summary-worker", time.Minute)
	if err != nil {
		t.Fatalf("pull dynamic summary failed: %v", err)
	}
	if summaryTask.TaskID != "dyn_summary" {
		t.Fatalf("unexpected summary task: %s", summaryTask.TaskID)
	}
	if _, err := store.CompleteTask(CompleteTaskInput{
		TaskID:   summaryTask.TaskID,
		WorkerID: summaryTask.OwnerID,
		Success:  true,
		Summary:  "summary done",
		RawData:  "summary",
	}); err != nil {
		t.Fatalf("complete dynamic summary failed: %v", err)
	}

	readyFinal, err := store.PullReadyTask("final-worker", time.Minute)
	if err != nil {
		t.Fatalf("pull final task failed after dynamic summary: %v", err)
	}
	if readyFinal.SkillName != "SendEmail" {
		t.Fatalf("expected final SendEmail task, got=%s", readyFinal.SkillName)
	}
}

func TestExpansionRejectedForSkillSinkNode(t *testing.T) {
	store := NewStore()
	snapshot, err := store.CreateDemoSession("u-sink", "plain flow")
	if err != nil {
		t.Fatalf("create session failed: %v", err)
	}
	task, err := store.PullReadyTask("worker-1", time.Minute)
	if err != nil {
		t.Fatalf("pull failed: %v", err)
	}
	if task.NodeType != model.NodeTypeSkillSink {
		t.Fatalf("expected skill sink node, got=%s", task.NodeType)
	}

	_, err = store.CompleteTask(CompleteTaskInput{
		TaskID:   task.TaskID,
		WorkerID: "worker-1",
		Success:  true,
		Summary:  "try to expand",
		ExpansionPayload: &ExpansionPayload{
			Reasoning:     "invalid expansion",
			MappingStatus: ExpansionMappingMapped,
			NewNodes: []ExpansionNode{
				{
					NodeID:       "dyn_1",
					NodeType:     model.NodeTypeSkillSink,
					SkillName:    "QueryLog",
					Dependencies: []string{task.TaskID},
				},
			},
			DownstreamWiring: DownstreamWiring{
				RedirectFrom: task.TaskID,
				RedirectTo:   []string{"dyn_1"},
			},
		},
	})
	if err != ErrExpansionNotAllowed {
		t.Fatalf("expected ErrExpansionNotAllowed, got=%v", err)
	}

	final, err := store.GetSessionSnapshot(snapshot.Session.SessionID)
	if err != nil {
		t.Fatalf("snapshot failed: %v", err)
	}
	if len(final.Tasks) != 3 {
		t.Fatalf("unexpected task count after rejected expansion: %d", len(final.Tasks))
	}
}

func TestCreateSessionFromPlanBuildsRuntimeGraph(t *testing.T) {
	store := NewStore()
	snapshot, err := store.CreateSessionFromPlan("u-plan", "plan-based creation", map[string]any{"macro_intent": "plan"}, []SessionTaskSpec{
		{RefID: "query", NodeType: model.NodeTypeSkillSink, SkillName: "QueryLog"},
		{RefID: "sum", NodeType: model.NodeTypeSkillSink, SkillName: "LLMSummarize", Dependencies: []string{"query"}},
		{RefID: "mail", NodeType: model.NodeTypeSkillSink, SkillName: "SendEmail", Dependencies: []string{"sum"}},
	})
	if err != nil {
		t.Fatalf("create session from plan failed: %v", err)
	}
	if got, want := len(snapshot.Tasks), 3; got != want {
		t.Fatalf("unexpected task count: got=%d want=%d", got, want)
	}
	var readyCount int
	for _, task := range snapshot.Tasks {
		if task.Status == model.TaskStatusReady {
			readyCount++
		}
		if task.TaskID == "query" || task.TaskID == "sum" || task.TaskID == "mail" {
			t.Fatalf("expected runtime task_id mapping, got ref id in snapshot: %s", task.TaskID)
		}
	}
	if got, want := readyCount, 1; got != want {
		t.Fatalf("expected exactly one ready root task, got=%d", got)
	}
}

func TestExpansionSupportsExpandPlanningNodeAndInjectsIntentContext(t *testing.T) {
	store := NewStore()
	snapshot, err := store.CreateJITDemoSession("u-jit", "followup planning")
	if err != nil {
		t.Fatalf("create jit session failed: %v", err)
	}
	planner, err := store.PullReadyTask("planner-worker", time.Minute)
	if err != nil {
		t.Fatalf("pull planner failed: %v", err)
	}
	_, err = store.CompleteTask(CompleteTaskInput{
		TaskID:   planner.TaskID,
		WorkerID: "planner-worker",
		Success:  true,
		Summary:  "expanded with planner child",
		ExpansionPayload: &ExpansionPayload{
			Reasoning:     "needs recursive planning",
			MappingStatus: ExpansionMappingUnmapped,
			NewNodes: []ExpansionNode{
				{
					NodeID:       "dyn_plan_next",
					NodeType:     model.NodeTypeExpandPlanning,
					SkillName:    "ReActPlanner",
					Dependencies: []string{planner.TaskID},
				},
			},
			DownstreamWiring: DownstreamWiring{
				RedirectFrom: planner.TaskID,
				RedirectTo:   []string{"dyn_plan_next"},
			},
		},
	})
	if err != nil {
		t.Fatalf("complete expansion failed: %v", err)
	}
	final, err := store.GetSessionSnapshot(snapshot.Session.SessionID)
	if err != nil {
		t.Fatalf("snapshot failed: %v", err)
	}
	if got, want := final.DAG.JITUnmappedStreak, 1; got != want {
		t.Fatalf("unexpected unmapped streak: got=%d want=%d", got, want)
	}
	var dyn model.Task
	found := false
	for _, task := range final.Tasks {
		if task.TaskID == "dyn_plan_next" {
			dyn = task
			found = true
			break
		}
	}
	if !found {
		t.Fatal("missing dyn_plan_next")
	}
	if dyn.NodeType != model.NodeTypeExpandPlanning {
		t.Fatalf("unexpected node type: %s", dyn.NodeType)
	}
	if _, ok := dyn.Parameters["intent_context"]; !ok {
		t.Fatalf("expected intent_context injected, got params=%v", dyn.Parameters)
	}
}

func TestExpansionUnmappedStreakExhaustedReturnsMissingSkill(t *testing.T) {
	store := NewStore()
	snapshot, err := store.CreateJITDemoSession("u-jit", "missing skill case")
	if err != nil {
		t.Fatalf("create jit session failed: %v", err)
	}
	session := store.sessions[snapshot.Session.SessionID]
	dag := store.dags[session.DAGID]
	dag.MaxUnmappedStreak = 1
	store.dags[session.DAGID] = dag

	planner, err := store.PullReadyTask("planner-worker", time.Minute)
	if err != nil {
		t.Fatalf("pull planner failed: %v", err)
	}
	_, err = store.CompleteTask(CompleteTaskInput{
		TaskID:   planner.TaskID,
		WorkerID: "planner-worker",
		Success:  true,
		Summary:  "cannot map skill",
		ExpansionPayload: &ExpansionPayload{
			Reasoning:     "unknown toolchain",
			MappingStatus: ExpansionMappingUnmapped,
			NewNodes: []ExpansionNode{
				{
					NodeID:       "dyn_plan_again",
					NodeType:     model.NodeTypeExpandPlanning,
					SkillName:    "ReActPlanner",
					Dependencies: []string{planner.TaskID},
				},
			},
			DownstreamWiring: DownstreamWiring{
				RedirectFrom: planner.TaskID,
				RedirectTo:   []string{"dyn_plan_again"},
			},
		},
	})
	if err != ErrSkillMappingExhausted {
		t.Fatalf("expected ErrSkillMappingExhausted, got=%v", err)
	}
	final, err := store.GetSessionSnapshot(snapshot.Session.SessionID)
	if err != nil {
		t.Fatalf("snapshot failed: %v", err)
	}
	if final.DAG.Status != model.DAGStatusReplanning {
		t.Fatalf("unexpected dag status: %s", final.DAG.Status)
	}
}
