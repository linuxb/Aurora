package scheduler

import (
	"fmt"
	"strings"
)

type MemHintStrategy string

const (
	MemHintStrategyKVPointGet  MemHintStrategy = "KV_POINT_GET"
	MemHintStrategyGraphLocal  MemHintStrategy = "GRAPH_LOCAL_TRAVERSAL"
	MemHintStrategyGraphGlobal MemHintStrategy = "GRAPH_GLOBAL_SUMMARY"
	MemHintStrategyNone        MemHintStrategy = "NONE"

	// Deprecated alias accepted while old planners are being upgraded.
	MemHintStrategyGraphTraversal = MemHintStrategyGraphLocal
)

type MemHintQuery struct {
	Text         string   `json:"text,omitempty"`
	Keywords     []string `json:"keywords,omitempty"`
	TargetTaskID string   `json:"target_task_id,omitempty"`
}

type MemHintPlannerIntent struct {
	Question        string `json:"question,omitempty"`
	CostTier        string `json:"cost_tier,omitempty"`
	LatencyBudgetMS int    `json:"latency_budget_ms,omitempty"`
	PreferFreshness *bool  `json:"prefer_freshness,omitempty"`
}

type MemHintFallback struct {
	AllowFallback *bool    `json:"allow_fallback,omitempty"`
	FallbackOrder []string `json:"fallback_order,omitempty"`
}

type MemHint struct {
	Version       string               `json:"version"`
	TargetSystem  string               `json:"target_system,omitempty"`
	Strategy      MemHintStrategy      `json:"strategy"`
	Query         MemHintQuery         `json:"query,omitempty"`
	PlannerIntent MemHintPlannerIntent `json:"planner_intent,omitempty"`
	Fallback      MemHintFallback      `json:"fallback,omitempty"`
	TargetStepID  string               `json:"target_step_id,omitempty"`
	SemanticQuery string               `json:"semantic_query,omitempty"`
}

func NormalizeMemHintStrategy(raw string) MemHintStrategy {
	switch strings.ToUpper(strings.TrimSpace(raw)) {
	case string(MemHintStrategyKVPointGet):
		return MemHintStrategyKVPointGet
	case string(MemHintStrategyGraphLocal), "GRAPH_TRAVERSAL":
		return MemHintStrategyGraphLocal
	case string(MemHintStrategyGraphGlobal):
		return MemHintStrategyGraphGlobal
	default:
		return MemHintStrategyNone
	}
}

func ValidateMemHint(hint *MemHint) error {
	if hint == nil {
		return nil
	}
	if strings.TrimSpace(hint.Version) == "" {
		hint.Version = "1.0"
	}
	if hint.Version != "1.0" {
		return fmt.Errorf("mem_hint.version %q is not supported", hint.Version)
	}
	if strings.TrimSpace(hint.TargetSystem) == "" {
		hint.TargetSystem = "AUTO"
	}
	if hint.Query.TargetTaskID == "" {
		hint.Query.TargetTaskID = hint.TargetStepID
	}
	if hint.Query.Text == "" {
		hint.Query.Text = hint.SemanticQuery
	}
	hint.Strategy = NormalizeMemHintStrategy(string(hint.Strategy))
	switch hint.Strategy {
	case MemHintStrategyKVPointGet:
		if strings.TrimSpace(hint.Query.TargetTaskID) == "" {
			return fmt.Errorf("mem_hint.query.target_task_id is required for strategy=%s", hint.Strategy)
		}
	case MemHintStrategyGraphLocal:
		if strings.TrimSpace(hint.Query.Text) == "" && len(hint.Query.Keywords) == 0 {
			return fmt.Errorf("mem_hint.query.text or keywords is required for strategy=%s", hint.Strategy)
		}
	case MemHintStrategyGraphGlobal:
		if strings.TrimSpace(hint.Query.Text) == "" {
			return fmt.Errorf("mem_hint.query.text is required for strategy=%s", hint.Strategy)
		}
	case MemHintStrategyNone:
		// no-op
	default:
		return fmt.Errorf("mem_hint.strategy %q is not supported", hint.Strategy)
	}
	return nil
}

func DefaultMemHint() MemHint {
	allowFallback := true
	preferFreshness := true
	return MemHint{
		Version:      "1.0",
		TargetSystem: "AUTO",
		Strategy:     MemHintStrategyNone,
		PlannerIntent: MemHintPlannerIntent{
			CostTier:        "MEDIUM",
			LatencyBudgetMS: 1500,
			PreferFreshness: &preferFreshness,
		},
		Fallback: MemHintFallback{
			AllowFallback: &allowFallback,
			FallbackOrder: []string{"KV_RECENT_WINDOW", "KV_FULLTEXT", "GRAPH_LOCAL_TRAVERSAL"},
		},
	}
}
