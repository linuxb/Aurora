package scheduler

import (
	"time"

	"aurora/apps/arqo/internal/model"
)

type Engine interface {
	CreateDemoSession(userID, intent string) (Snapshot, error)
	PullReadyTask(workerID string, ttl time.Duration) (*model.Task, error)
	CompleteTask(input CompleteTaskInput) (*model.Task, error)
	ExpireRunningTasks(now time.Time) []string
	GetSessionSnapshot(sessionID string) (Snapshot, error)
	ResolveSessionIDByTaskID(taskID string) (string, bool)
	Close() error
}
