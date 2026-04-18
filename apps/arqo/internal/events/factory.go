package events

import (
	"fmt"
	"os"
	"strings"
	"time"
)

const redisDialTimeout = 2 * time.Second

func NewBrokerFromEnv() (Broker, string, error) {
	backend := strings.ToLower(strings.TrimSpace(os.Getenv("ARQO_EVENT_BACKEND")))
	if backend == "" {
		backend = "memory"
	}

	switch backend {
	case "memory":
		return NewMemoryBroker(), backend, nil
	case "redis":
		addr := envOrDefault("ARQO_REDIS_ADDR", "127.0.0.1:6379")
		password := os.Getenv("ARQO_REDIS_PASSWORD")
		db, err := parseRedisDB(os.Getenv("ARQO_REDIS_DB"))
		if err != nil {
			return nil, "", err
		}
		prefix := envOrDefault("ARQO_REDIS_CHANNEL_PREFIX", "aurora")
		broker, err := NewRedisBroker(addr, password, db, prefix)
		if err != nil {
			return nil, "", err
		}
		return broker, backend, nil
	default:
		return nil, "", fmt.Errorf("unsupported event backend: %s", backend)
	}
}

func envOrDefault(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}
