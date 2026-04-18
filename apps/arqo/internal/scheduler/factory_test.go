package scheduler

import "testing"

func TestNewEngineFromEnvDefaultsToMemory(t *testing.T) {
	t.Setenv("ARQO_SCHEDULER_BACKEND", "")
	engine, backend, err := NewEngineFromEnv()
	if err != nil {
		t.Fatalf("new engine failed: %v", err)
	}
	if backend != "memory" {
		t.Fatalf("unexpected backend: %s", backend)
	}
	if engine == nil {
		t.Fatal("engine is nil")
	}
	_ = engine.Close()
}

func TestNewEngineFromEnvUnsupportedBackend(t *testing.T) {
	t.Setenv("ARQO_SCHEDULER_BACKEND", "unknown")
	_, _, err := NewEngineFromEnv()
	if err == nil {
		t.Fatal("expected error for unsupported backend")
	}
}

func TestNewEngineFromEnvMySQLWithoutDriver(t *testing.T) {
	t.Setenv("ARQO_SCHEDULER_BACKEND", "mysql")
	_, _, err := NewEngineFromEnv()
	if err == nil {
		t.Fatal("expected error when mysql driver is not registered")
	}
}
