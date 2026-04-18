package model

import "time"

type DAGStatus string

type TaskStatus string

const (
	DAGStatusRunning    DAGStatus = "RUNNING"
	DAGStatusReplanning DAGStatus = "REPLANNING"
	DAGStatusSuccess    DAGStatus = "SUCCESS"
	DAGStatusFailed     DAGStatus = "FAILED"
)

const (
	TaskStatusPending TaskStatus = "PENDING"
	TaskStatusReady   TaskStatus = "READY"
	TaskStatusRunning TaskStatus = "RUNNING"
	TaskStatusSuccess TaskStatus = "SUCCESS"
	TaskStatusFailed  TaskStatus = "FAILED"
)

type DAG struct {
	DAGID          string    `json:"dag_id"`
	SessionID      string    `json:"session_id"`
	UserID         string    `json:"user_id"`
	OriginalIntent string    `json:"original_intent"`
	Status         DAGStatus `json:"status"`
	ReplanCount    int       `json:"replan_count"`
	CreatedAt      time.Time `json:"created_at"`
}

type Task struct {
	TaskID                    string     `json:"task_id"`
	DAGID                     string     `json:"dag_id"`
	SkillName                 string     `json:"skill_name"`
	Status                    TaskStatus `json:"status"`
	PendingDependenciesCount  int        `json:"pending_dependencies_count"`
	OwnerID                   string     `json:"owner_id,omitempty"`
	ExpireAt                  *time.Time `json:"expire_at,omitempty"`
	Dependencies              []string   `json:"dependencies"`
	Children                  []string   `json:"children"`
	LastSummary               string     `json:"last_summary,omitempty"`
	LastErrorCode             string     `json:"last_error_code,omitempty"`
	LastHumanReadableErrorMsg string     `json:"last_human_readable_error_msg,omitempty"`
}

type Session struct {
	SessionID string    `json:"session_id"`
	DAGID     string    `json:"dag_id"`
	UserID    string    `json:"user_id"`
	Intent    string    `json:"intent"`
	CreatedAt time.Time `json:"created_at"`
}
