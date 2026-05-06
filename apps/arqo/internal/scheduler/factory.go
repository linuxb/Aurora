package scheduler

import (
	"fmt"
	"os"
	"strings"
)

func NewEngineFromEnv() (Engine, string, error) {
	backend := strings.ToLower(strings.TrimSpace(os.Getenv("ARQO_SCHEDULER_BACKEND")))
	policy := leaseExpirePolicyFromEnv()
	if backend == "" {
		backend = "memory"
	}

	switch backend {
	case "memory":
		return NewStoreWithLeasePolicy(policy), backend, nil
	case "mysql":
		engine, err := NewMySQLStoreFromEnv()
		if err != nil {
			return nil, "", err
		}
		engine.leaseExpirePolicy = policy
		return engine, backend, nil
	case "tidb":
		engine, err := NewTiDBStoreFromEnv()
		if err != nil {
			return nil, "", err
		}
		engine.leaseExpirePolicy = policy
		return engine, backend, nil
	default:
		return nil, "", fmt.Errorf("unsupported scheduler backend: %s", backend)
	}
}
