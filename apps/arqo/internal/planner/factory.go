package planner

import (
	"fmt"
	"os"
	"strings"
)

func NewRouterFromEnv() (Router, string, error) {
	backend := strings.ToLower(strings.TrimSpace(os.Getenv("ARQO_PLANNER_BACKEND")))
	if backend == "" {
		backend = "mock"
	}

	switch backend {
	case "mock":
		return NewMockRouter(), backend, nil
	case "model":
		fallback := strings.ToLower(strings.TrimSpace(os.Getenv("ARQO_PLANNER_FALLBACK")))
		if fallback == "" {
			fallback = "mock"
		}
		switch fallback {
		case "mock":
			return NewFallbackRouter(NewModelRouter(), NewMockRouter()), "model->mock", nil
		case "none":
			return NewModelRouter(), "model", nil
		default:
			return nil, "", fmt.Errorf("unsupported planner fallback backend: %s", fallback)
		}
	default:
		return nil, "", fmt.Errorf("unsupported planner backend: %s", backend)
	}
}
