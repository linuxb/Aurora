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

	"aurora/apps/arqo/internal/events"
	"aurora/apps/arqo/internal/model"
	"aurora/apps/arqo/internal/planner"
	"aurora/apps/arqo/internal/scheduler"
)

type failingRouter struct{}

func (r *failingRouter) Plan(_ string, _ string) (planner.Plan, error) {
	return planner.Plan{}, errors.New("planner unavailable")
}

type fakeMemorySearcher struct {
	entries []MemoryEntry
	err     error
}

func (m *fakeMemorySearcher) Search(_ string, _ string, _ string, _ int) ([]MemoryEntry, error) {
	return m.entries, m.err
}

func (m *fakeMemorySearcher) SearchByHint(_ string, _ string, _ string, _ int) ([]MemoryEntry, error) {
	return m.entries, m.err
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
		&fakeMemorySearcher{
			entries: []MemoryEntry{
				{UserID: "u-plan-compat", SessionID: "old_sess", TaskID: "old_task", Summary: "recent payment incident"},
			},
		},
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
	if _, ok := intentContext["memory_hits"]; !ok {
		t.Fatalf("expected memory_hits injected into intent_context, got=%v", intentContext)
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
				"node_type":    "SKILL_SINK",
				"skill_name":   "QueryLog",
				"dependencies": []string{},
			},
			{
				"ref_id":       "patch_finish",
				"node_type":    "SKILL_SINK",
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
