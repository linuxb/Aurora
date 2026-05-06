package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"aurora/apps/arqo/internal/events"
	"aurora/apps/arqo/internal/planner"
	"aurora/apps/arqo/internal/scheduler"
)

type failingRouter struct{}

func (r *failingRouter) Plan(_ string, _ string) (planner.Plan, error) {
	return planner.Plan{}, errors.New("planner unavailable")
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
	server := NewServer(scheduler.NewStore(), events.NewMemoryBroker())
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
