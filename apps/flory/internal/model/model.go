package model

import (
	"fmt"
	"strings"
	"time"
)

type DAGStatus string

type TaskStatus string
type NodeType string

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

const (
	NodeTypeSkill   NodeType = "skill"
	NodeTypePlanner NodeType = "planner"

	// Deprecated aliases retained for source compatibility during the migration.
	NodeTypeSkillSink      = NodeTypeSkill
	NodeTypeExpandPlanning = NodeTypePlanner
)

func ParseNodeType(raw string) (NodeType, error) {
	normalized := strings.ToLower(strings.TrimSpace(raw))
	switch normalized {
	case string(NodeTypeSkill), "skill_sink":
		return NodeTypeSkill, nil
	case string(NodeTypePlanner), "expand_planning", "expanding":
		return NodeTypePlanner, nil
	default:
		return "", fmt.Errorf("invalid node_type %q", raw)
	}
}

type DAG struct {
	DAGID             string         `json:"dag_id"`
	TenantID          string         `json:"tenant_id"`
	AgentID           string         `json:"agent_id"`
	SessionID         string         `json:"session_id"`
	UserID            string         `json:"user_id"`
	OriginalIntent    string         `json:"original_intent"`
	IntentContext     map[string]any `json:"intent_context,omitempty"`
	Status            DAGStatus      `json:"status"`
	ReplanCount       int            `json:"replan_count"`
	CurrentDepth      int            `json:"current_depth"`
	MaxDepth          int            `json:"max_depth"`
	JITUnmappedStreak int            `json:"jit_unmapped_streak"`
	MaxUnmappedStreak int            `json:"max_unmapped_streak"`
	CreatedAt         time.Time      `json:"created_at"`
}

type Task struct {
	TaskID                    string         `json:"task_id"`
	DAGID                     string         `json:"dag_id"`
	Sequence                  int64          `json:"sequence"`
	NodeType                  NodeType       `json:"node_type"`
	SkillName                 string         `json:"skill_name,omitempty"`
	Goal                      string         `json:"goal,omitempty"`
	MemHint                   map[string]any `json:"mem_hint"`
	Status                    TaskStatus     `json:"status"`
	PendingDependenciesCount  int            `json:"pending_dependencies_count"`
	OwnerID                   string         `json:"owner_id,omitempty"`
	ExpireAt                  *time.Time     `json:"expire_at,omitempty"`
	Dependencies              []string       `json:"dependencies"`
	Children                  []string       `json:"children"`
	Parameters                map[string]any `json:"parameters,omitempty"`
	LastSummary               string         `json:"last_summary,omitempty"`
	LastErrorCode             string         `json:"last_error_code,omitempty"`
	LastHumanReadableErrorMsg string         `json:"last_human_readable_error_msg,omitempty"`
}

type Session struct {
	SessionID string    `json:"session_id"`
	DAGID     string    `json:"dag_id"`
	TenantID  string    `json:"tenant_id"`
	AgentID   string    `json:"agent_id"`
	UserID    string    `json:"user_id"`
	Intent    string    `json:"intent"`
	CreatedAt time.Time `json:"created_at"`
}
