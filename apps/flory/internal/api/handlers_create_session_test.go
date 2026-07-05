package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"aurora/apps/flory/internal/events"
	"aurora/apps/flory/internal/model"
	"aurora/apps/flory/internal/planner"
	"aurora/apps/flory/internal/scheduler"
)

type failingRouter struct{}

func (r *failingRouter) Plan(_ string, _ string) (planner.Plan, error) {
	return planner.Plan{}, errors.New("planner unavailable")
}

type fakeMemoryClient struct {
	ingests  []Mem3IngestRequest
	searches []Mem3SearchRequest
	response Mem3SearchResponse
	err      error
}

func (m *fakeMemoryClient) Ingest(_ context.Context, req Mem3IngestRequest) error {
	m.ingests = append(m.ingests, req)
	return m.err
}

func (m *fakeMemoryClient) Search(_ context.Context, req Mem3SearchRequest) (Mem3SearchResponse, error) {
	m.searches = append(m.searches, req)
	return m.response, m.err
}

func TestCreateSessionRejectsInvalidPlan(t *testing.T) {
	server := NewServer(scheduler.NewStore(), events.NewMemoryBroker())
	mux := http.NewServeMux()
	server.Register(mux)

	body := map[string]any{
		"user_id": "u1",
		"intent":  "please generate invalid_dag for test",
	}
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/v1/sessions", bytes.NewReader(raw))
	res := httptest.NewRecorder()

	mux.ServeHTTP(res, req)
	if got, want := res.Code, http.StatusUnprocessableEntity; got != want {
		t.Fatalf("unexpected status code: got=%d want=%d body=%s", got, want, res.Body.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(res.Body.Bytes(), &payload); err != nil {
		t.Fatalf("invalid json response: %v", err)
	}
	if _, ok := payload["plan"]; !ok {
		t.Fatalf("expected plan field in invalid response, got=%v", payload)
	}
}

func TestCreateSessionAcceptsValidPlan(t *testing.T) {
	server := NewServer(scheduler.NewStore(), events.NewMemoryBroker())
	mux := http.NewServeMux()
	server.Register(mux)

	body := map[string]any{
		"user_id": "u2",
		"intent":  "summarize logs and send mail",
	}
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/v1/sessions", bytes.NewReader(raw))
	res := httptest.NewRecorder()

	mux.ServeHTTP(res, req)
	if got, want := res.Code, http.StatusCreated; got != want {
		t.Fatalf("unexpected status code: got=%d want=%d body=%s", got, want, res.Body.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(res.Body.Bytes(), &payload); err != nil {
		t.Fatalf("invalid json response: %v", err)
	}
	if _, ok := payload["plan"]; !ok {
		t.Fatalf("expected plan field in created response, got=%v", payload)
	}
}

func TestCreateSessionPlanPayloadCompatibilityBaseline(t *testing.T) {
	server := NewServerWithPlannerAndMemory(
		scheduler.NewStore(),
		events.NewMemoryBroker(),
		planner.NewMockRouter(),
		&fakeMemoryClient{},
	)
	mux := http.NewServeMux()
	server.Register(mux)

	body := map[string]any{
		"user_id":       "u-plan-compat",
		"intent":        "investigate payment issue and notify",
		"planning_mode": "jit",
	}
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/v1/sessions", bytes.NewReader(raw))
	res := httptest.NewRecorder()

	mux.ServeHTTP(res, req)
	if got, want := res.Code, http.StatusCreated; got != want {
		t.Fatalf("unexpected status code: got=%d want=%d body=%s", got, want, res.Body.String())
	}

	var payload map[string]any
	if err := json.Unmarshal(res.Body.Bytes(), &payload); err != nil {
		t.Fatalf("invalid json response: %v", err)
	}
	plan, ok := payload["plan"].(map[string]any)
	if !ok {
		t.Fatalf("plan is missing or invalid type: %#v", payload["plan"])
	}
	if _, ok := plan["plan_id"].(string); !ok || fmt.Sprint(plan["plan_id"]) == "" {
		t.Fatalf("plan_id missing or invalid: %#v", plan["plan_id"])
	}
	if got, ok := plan["source"].(string); !ok || got == "" {
		t.Fatalf("source missing or invalid: %#v", plan["source"])
	}
	if _, ok := plan["intent_context"].(map[string]any); !ok {
		t.Fatalf("intent_context missing or invalid: %#v", plan["intent_context"])
	}
	intentContext := plan["intent_context"].(map[string]any)
	if _, ok := intentContext["registered_skills"]; !ok {
		t.Fatalf("expected registered_skills in intent_context, got=%v", intentContext)
	}
	nodes, ok := plan["nodes"].([]any)
	if !ok || len(nodes) == 0 {
		t.Fatalf("nodes missing or invalid: %#v", plan["nodes"])
	}
	firstNode, ok := nodes[0].(map[string]any)
	if !ok {
		t.Fatalf("first node invalid type: %#v", nodes[0])
	}
	requiredFields := []string{"node_id", "node_type", "skill_name", "dependencies"}
	for _, field := range requiredFields {
		if _, ok := firstNode[field]; !ok {
			t.Fatalf("node missing required field %s: %#v", field, firstNode)
		}
	}
}

func TestPullTaskInjectsPlannerMemHintAndMemoryHits(t *testing.T) {
	memory := &fakeMemoryClient{}
	server := NewServerWithPlannerAndMemory(
		scheduler.NewStore(),
		events.NewMemoryBroker(),
		planner.NewMockRouter(),
		memory,
	)
	mux := http.NewServeMux()
	server.Register(mux)

	createBody := map[string]any{
		"user_id":       "u-mem",
		"intent":        "investigate issue",
		"planning_mode": "jit",
	}
	createRaw, _ := json.Marshal(createBody)
	createReq := httptest.NewRequest(http.MethodPost, "/v1/sessions", bytes.NewReader(createRaw))
	createRes := httptest.NewRecorder()
	mux.ServeHTTP(createRes, createReq)
	if createRes.Code != http.StatusCreated {
		t.Fatalf("create failed: status=%d body=%s", createRes.Code, createRes.Body.String())
	}

	pullRaw, _ := json.Marshal(map[string]any{"worker_id": "w1"})
	pullReq := httptest.NewRequest(http.MethodPost, "/v1/tasks/pull", bytes.NewReader(pullRaw))
	pullRes := httptest.NewRecorder()
	mux.ServeHTTP(pullRes, pullReq)
	if pullRes.Code != http.StatusOK {
		t.Fatalf("pull failed: status=%d body=%s", pullRes.Code, pullRes.Body.String())
	}
	var task map[string]any
	if err := json.Unmarshal(pullRes.Body.Bytes(), &task); err != nil {
		t.Fatalf("invalid pull response: %v", err)
	}
	params, ok := task["parameters"].(map[string]any)
	if !ok {
		t.Fatalf("missing task parameters: %#v", task)
	}
	if _, ok := params["mem_hint"]; !ok {
		t.Fatalf("expected mem_hint in planner parameters: %v", params)
	}
	if _, ok := params["working_memory"]; !ok {
		t.Fatalf("expected working_memory in planner parameters: %v", params)
	}
	if len(memory.ingests) != 1 || memory.ingests[0].Kind != "DAG_CONTEXT" {
		t.Fatalf("expected DAG_CONTEXT ingest before execution, got=%v", memory.ingests)
	}
	if len(memory.searches) != 1 {
		t.Fatalf("expected one Mem3 search before dispatch, got=%d", len(memory.searches))
	}
	search := memory.searches[0]
	if search.Scope.TenantID != "u-mem" || search.CurrentTask.TaskID == "" || search.MemHint.Version != "1.0" {
		t.Fatalf("unexpected Mem3 search envelope: %+v", search)
	}
}

func TestCompleteTaskIngestsTaskOutputToMem3(t *testing.T) {
	memory := &fakeMemoryClient{}
	server := NewServerWithPlannerAndMemory(
		scheduler.NewStore(), events.NewMemoryBroker(), planner.NewMockRouter(), memory,
	)
	mux := http.NewServeMux()
	server.Register(mux)

	createRaw, _ := json.Marshal(map[string]any{"user_id": "u-output", "intent": "summarize logs"})
	createRes := httptest.NewRecorder()
	mux.ServeHTTP(createRes, httptest.NewRequest(http.MethodPost, "/v1/sessions", bytes.NewReader(createRaw)))
	if createRes.Code != http.StatusCreated {
		t.Fatalf("create failed: %d %s", createRes.Code, createRes.Body.String())
	}

	pullRaw, _ := json.Marshal(map[string]any{"worker_id": "w-output"})
	pullRes := httptest.NewRecorder()
	mux.ServeHTTP(pullRes, httptest.NewRequest(http.MethodPost, "/v1/tasks/pull", bytes.NewReader(pullRaw)))
	var task model.Task
	if err := json.Unmarshal(pullRes.Body.Bytes(), &task); err != nil {
		t.Fatalf("decode task: %v", err)
	}

	completeRaw, _ := json.Marshal(map[string]any{
		"worker_id": "w-output", "success": true, "summary": "done",
		"raw_data": map[string]any{"records": 3},
	})
	completeRes := httptest.NewRecorder()
	mux.ServeHTTP(completeRes, httptest.NewRequest(
		http.MethodPost, "/v1/tasks/"+task.TaskID+"/complete", bytes.NewReader(completeRaw),
	))
	if completeRes.Code != http.StatusOK {
		t.Fatalf("complete failed: %d %s", completeRes.Code, completeRes.Body.String())
	}
	if len(memory.ingests) != 2 || memory.ingests[1].Kind != "TASK_OUTPUT" {
		t.Fatalf("expected DAG_CONTEXT and TASK_OUTPUT ingests, got=%v", memory.ingests)
	}
	payload, ok := memory.ingests[1].Payload.(Mem3TaskOutput)
	if !ok || payload.TaskID != task.TaskID || payload.NodeType != "skill" {
		t.Fatalf("unexpected task output payload: %#v", memory.ingests[1].Payload)
	}
}

func TestCreateSessionPlanGenerationFailure(t *testing.T) {
	server := NewServerWithPlanner(scheduler.NewStore(), events.NewMemoryBroker(), &failingRouter{})
	mux := http.NewServeMux()
	server.Register(mux)

	body := map[string]any{
		"user_id": "u3",
		"intent":  "any",
	}
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/v1/sessions", bytes.NewReader(raw))
	res := httptest.NewRecorder()

	mux.ServeHTTP(res, req)
	if got, want := res.Code, http.StatusInternalServerError; got != want {
		t.Fatalf("unexpected status code: got=%d want=%d body=%s", got, want, res.Body.String())
	}
}

func TestSweepExpiredEndpoint(t *testing.T) {
	store := scheduler.NewStore()
	server := NewServer(store, events.NewMemoryBroker())
	mux := http.NewServeMux()
	server.Register(mux)

	snapshot, err := store.CreateDemoSession("u-sweep", "sweep test")
	if err != nil {
		t.Fatalf("create demo session failed: %v", err)
	}
	task, err := store.PullReadyTask("sweeper-worker", -1*time.Second)
	if err != nil {
		t.Fatalf("pull failed: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/v1/admin/sweep-expired", nil)
	res := httptest.NewRecorder()
	mux.ServeHTTP(res, req)
	if got, want := res.Code, http.StatusOK; got != want {
		t.Fatalf("unexpected status code: got=%d want=%d body=%s", got, want, res.Body.String())
	}

	var payload map[string]any
	if err := json.Unmarshal(res.Body.Bytes(), &payload); err != nil {
		t.Fatalf("invalid json response: %v", err)
	}
	if got := int(payload["count"].(float64)); got < 1 {
		t.Fatalf("expected at least one expired task, got=%d payload=%v", got, payload)
	}

	updated, err := store.GetSessionSnapshot(snapshot.Session.SessionID)
	if err != nil {
		t.Fatalf("snapshot failed: %v", err)
	}
	found := false
	for _, tsk := range updated.Tasks {
		if tsk.TaskID == task.TaskID {
			found = true
			if tsk.Status != model.TaskStatusFailed {
				t.Fatalf("expected swept task to become FAILED, got=%s", tsk.Status)
			}
		}
	}
	if !found {
		t.Fatalf("task not found after sweep: %s", task.TaskID)
	}
}

func TestSweepExpiredPublishesTimelineEvent(t *testing.T) {
	store := scheduler.NewStore()
	broker := events.NewMemoryBroker()
	server := NewServer(store, broker)
	mux := http.NewServeMux()
	server.Register(mux)

	snapshot, err := store.CreateDemoSession("u-sweep-event", "sweep event test")
	if err != nil {
		t.Fatalf("create demo session failed: %v", err)
	}
	task, err := store.PullReadyTask("sweeper-worker", -1*time.Second)
	if err != nil {
		t.Fatalf("pull failed: %v", err)
	}
	if task.TaskID == "" {
		t.Fatal("expected leased task id")
	}

	subCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch, err := broker.Subscribe(subCtx, snapshot.Session.SessionID)
	if err != nil {
		t.Fatalf("subscribe failed: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/v1/admin/sweep-expired", nil)
	res := httptest.NewRecorder()
	mux.ServeHTTP(res, req)
	if got, want := res.Code, http.StatusOK; got != want {
		t.Fatalf("unexpected status code: got=%d want=%d body=%s", got, want, res.Body.String())
	}

	timer := time.NewTimer(2 * time.Second)
	defer timer.Stop()
	for {
		select {
		case evt := <-ch:
			if evt.EventType == "TASK_SWEEP_EXPIRED" && evt.TaskID == task.TaskID {
				return
			}
		case <-timer.C:
			t.Fatalf("timed out waiting for TASK_SWEEP_EXPIRED event on task=%s", task.TaskID)
		}
	}
}

func TestApplyReplanPatchEndpoint(t *testing.T) {
	store := scheduler.NewStore()
	server := NewServer(store, events.NewMemoryBroker())
	mux := http.NewServeMux()
	server.Register(mux)

	snapshot, err := store.CreateDemoSession("u-replan-api", "api replan test")
	if err != nil {
		t.Fatalf("create session failed: %v", err)
	}
	task, err := store.PullReadyTask("worker-1", time.Minute)
	if err != nil {
		t.Fatalf("pull failed: %v", err)
	}
	if _, err := store.CompleteTask(scheduler.CompleteTaskInput{
		TaskID:       task.TaskID,
		WorkerID:     "worker-1",
		Success:      false,
		ErrorCode:    "FAIL",
		ErrorMessage: "force replanning",
	}); err != nil {
		t.Fatalf("complete failed: %v", err)
	}

	body := map[string]any{
		"reason": "recover by patch",
		"tasks": []map[string]any{
			{
				"ref_id":       "patch_root",
				"node_type":    "skill",
				"skill_name":   "QueryLog",
				"dependencies": []string{},
			},
			{
				"ref_id":       "patch_finish",
				"node_type":    "skill",
				"skill_name":   "SendEmail",
				"dependencies": []string{"patch_root"},
			},
		},
	}
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/v1/sessions/"+snapshot.Session.SessionID+"/replan", bytes.NewReader(raw))
	res := httptest.NewRecorder()
	mux.ServeHTTP(res, req)

	if got, want := res.Code, http.StatusOK; got != want {
		t.Fatalf("unexpected status code: got=%d want=%d body=%s", got, want, res.Body.String())
	}
	after, err := store.GetSessionSnapshot(snapshot.Session.SessionID)
	if err != nil {
		t.Fatalf("snapshot failed: %v", err)
	}
	if after.DAG.Status != model.DAGStatusRunning {
		t.Fatalf("expected DAG RUNNING after api patch, got=%s", after.DAG.Status)
	}
}
