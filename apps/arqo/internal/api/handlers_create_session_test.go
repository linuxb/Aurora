package api

import (
	"bytes"
	"encoding/json"
	"errors"
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
