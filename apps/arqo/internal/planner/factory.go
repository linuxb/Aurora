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
	default:
		return nil, "", fmt.Errorf("unsupported planner backend: %s", backend)
	}
}
