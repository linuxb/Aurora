package planner

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestModelRouterPlanUnavailableWithoutURL(t *testing.T) {
	t.Setenv("ARQO_PLANNER_MODEL_URL", "")
	router := NewModelRouterFromEnv()
	_, err := router.Plan("hello", "aot")
	if err == nil {
		t.Fatal("expected unavailable error")
	}
	if err != ErrModelPlannerUnavailable {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestModelRouterPlanSuccess(t *testing.T) {
	router := &ModelRouter{
		endpointURL: "http://planner.local/plan",
		modelName:   "test-model",
		client: &http.Client{
			Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": []string{"application/json"}},
					Body: io.NopCloser(strings.NewReader(
						`{"plan":{"source":"model","nodes":[{"node_id":"n1","node_type":"SKILL_SINK","skill_name":"QueryLog","dependencies":[]},{"node_id":"n2","node_type":"SKILL_SINK","skill_name":"SendEmail","dependencies":["n1"]}],"intent_context":{"from":"model"}}}`,
					)),
				}, nil
			}),
		},
	}
	plan, err := router.Plan("summarize logs", "aot")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if plan.Source != "model" {
		t.Fatalf("unexpected source: %s", plan.Source)
	}
	if len(plan.Nodes) != 2 {
		t.Fatalf("unexpected node size: %d", len(plan.Nodes))
	}
	if plan.PlanID == "" {
		t.Fatal("expected generated plan id")
	}
}
