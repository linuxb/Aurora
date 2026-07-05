package events

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/redis/go-redis/v9"
)

type RedisBroker struct {
	client *redis.Client
	prefix string
}

func NewRedisBroker(addr, password string, db int, prefix string) (*RedisBroker, error) {
	client := redis.NewClient(&redis.Options{
		Addr:     addr,
		Password: password,
		DB:       db,
	})

	ctx, cancel := context.WithTimeout(context.Background(), redisDialTimeout)
	defer cancel()
	if err := client.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("redis ping failed: %w", err)
	}

	if prefix == "" {
		prefix = "aurora"
	}

	return &RedisBroker{client: client, prefix: prefix}, nil
}

func (b *RedisBroker) Subscribe(ctx context.Context, sessionID string) (<-chan Event, error) {
	pubSub := b.client.Subscribe(ctx, b.channel(sessionID))
	if _, err := pubSub.Receive(ctx); err != nil {
		_ = pubSub.Close()
		return nil, fmt.Errorf("redis subscribe failed: %w", err)
	}

	in := pubSub.Channel()
	out := make(chan Event, 64)

	go func() {
		defer close(out)
		defer func() { _ = pubSub.Close() }()

		for {
			select {
			case <-ctx.Done():
				return
			case msg, ok := <-in:
				if !ok {
					return
				}
				var evt Event
				if err := json.Unmarshal([]byte(msg.Payload), &evt); err != nil {
					continue
				}
				select {
				case out <- evt:
				default:
				}
			}
		}
	}()

	return out, nil
}

func (b *RedisBroker) Publish(ctx context.Context, evt Event) error {
	payload, err := json.Marshal(evt)
	if err != nil {
		return fmt.Errorf("marshal event failed: %w", err)
	}
	if err := b.client.Publish(ctx, b.channel(evt.SessionID), payload).Err(); err != nil {
		return fmt.Errorf("redis publish failed: %w", err)
	}
	return nil
}

func (b *RedisBroker) Close() error {
	return b.client.Close()
}

func (b *RedisBroker) channel(sessionID string) string {
	return b.prefix + ":session:" + sessionID + ":events"
}

func parseRedisDB(raw string) (int, error) {
	if raw == "" {
		return 0, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("invalid redis db: %w", err)
	}
	return value, nil
}
