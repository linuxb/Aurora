package planner

import (
	"fmt"
	"time"
)

type Plan struct {
	PlanID        string              `json:"plan_id"`
	Source        string              `json:"source"`
	IntentContext map[string]any      `json:"intent_context,omitempty"`
	Nodes         []Node              `json:"nodes"`
	Warnings      []ValidationWarning `json:"warnings,omitempty"`
}

func NewPlan(source string, nodes []Node) Plan {
	return Plan{
		PlanID:        fmt.Sprintf("plan_%d", time.Now().UTC().UnixNano()),
		Source:        source,
		IntentContext: map[string]any{},
		Nodes:         nodes,
	}
}
