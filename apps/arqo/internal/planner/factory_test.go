package planner

import "testing"

func TestNewRouterFromEnv_DefaultMock(t *testing.T) {
	t.Setenv("ARQO_PLANNER_BACKEND", "")
	router, backend, err := NewRouterFromEnv()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if backend != "mock" {
		t.Fatalf("unexpected backend: %s", backend)
	}
	if router == nil {
		t.Fatal("expected router")
	}
}

func TestNewRouterFromEnv_Unsupported(t *testing.T) {
	t.Setenv("ARQO_PLANNER_BACKEND", "unknown")
	_, _, err := NewRouterFromEnv()
	if err == nil {
		t.Fatal("expected unsupported backend error")
	}
}

func TestNewRouterFromEnv_ModelWithMockFallback(t *testing.T) {
	t.Setenv("ARQO_PLANNER_BACKEND", "model")
	t.Setenv("ARQO_PLANNER_FALLBACK", "mock")
	router, backend, err := NewRouterFromEnv()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if backend != "model->mock" {
		t.Fatalf("unexpected backend: %s", backend)
	}
	plan, planErr := router.Plan("summarize logs", "aot")
	if planErr != nil {
		t.Fatalf("unexpected plan error: %v", planErr)
	}
	if plan.Source != "mock" {
		t.Fatalf("expected fallback plan source mock, got=%s", plan.Source)
	}
}
