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
