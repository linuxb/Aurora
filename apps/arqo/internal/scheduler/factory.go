package scheduler

import (
	"fmt"
	"os"
	"strings"
)

func NewEngineFromEnv() (Engine, string, error) {
	backend := strings.ToLower(strings.TrimSpace(os.Getenv("ARQO_SCHEDULER_BACKEND")))
	if backend == "" {
		backend = "memory"
	}

	switch backend {
	case "memory":
		return NewStore(), backend, nil
	case "mysql":
		engine, err := NewMySQLStoreFromEnv()
		if err != nil {
			return nil, "", err
		}
		return engine, backend, nil
	case "tidb":
		engine, err := NewTiDBStoreFromEnv()
		if err != nil {
			return nil, "", err
		}
		return engine, backend, nil
	default:
		return nil, "", fmt.Errorf("unsupported scheduler backend: %s", backend)
	}
}
