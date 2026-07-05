package planner

import (
	"fmt"
	"os"
	"strings"
)

func NewRouterFromEnv() (Router, string, error) {
	backend := strings.ToLower(strings.TrimSpace(os.Getenv("FLORY_PLANNER_BACKEND")))
	if backend == "" {
		backend = "mock"
	}

	switch backend {
	case "mock":
		return NewMockRouter(), backend, nil
	case "model":
		modelRouter := NewModelRouterFromEnv()
		fallback := strings.ToLower(strings.TrimSpace(os.Getenv("FLORY_PLANNER_FALLBACK")))
		if fallback == "" {
			fallback = "mock"
		}
		switch fallback {
		case "mock":
			return NewFallbackRouter(modelRouter, NewMockRouter()), "model->mock", nil
		case "none":
			return modelRouter, "model", nil
		default:
			return nil, "", fmt.Errorf("unsupported planner fallback backend: %s", fallback)
		}
	default:
		return nil, "", fmt.Errorf("unsupported planner backend: %s", backend)
	}
}
