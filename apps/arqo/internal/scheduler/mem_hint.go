package scheduler

import (
	"fmt"
	"strings"
)

type MemHintStrategy string

const (
	MemHintStrategyKVPointGet     MemHintStrategy = "KV_POINT_GET"
	MemHintStrategyGraphTraversal MemHintStrategy = "GRAPH_TRAVERSAL"
	MemHintStrategyNone           MemHintStrategy = "NONE"
)

type MemHint struct {
	Strategy      MemHintStrategy `json:"strategy"`
	TargetStepID  string          `json:"target_step_id,omitempty"`
	SemanticQuery string          `json:"semantic_query,omitempty"`
}

func NormalizeMemHintStrategy(raw string) MemHintStrategy {
	switch strings.ToUpper(strings.TrimSpace(raw)) {
	case string(MemHintStrategyKVPointGet):
		return MemHintStrategyKVPointGet
	case string(MemHintStrategyGraphTraversal):
		return MemHintStrategyGraphTraversal
	default:
		return MemHintStrategyNone
	}
}

func ValidateMemHint(hint *MemHint) error {
	if hint == nil {
		return nil
	}
	hint.Strategy = NormalizeMemHintStrategy(string(hint.Strategy))
	switch hint.Strategy {
	case MemHintStrategyKVPointGet:
		if strings.TrimSpace(hint.TargetStepID) == "" {
			return fmt.Errorf("mem_hint.target_step_id is required for strategy=%s", hint.Strategy)
		}
	case MemHintStrategyGraphTraversal:
		if strings.TrimSpace(hint.SemanticQuery) == "" {
			return fmt.Errorf("mem_hint.semantic_query is required for strategy=%s", hint.Strategy)
		}
	case MemHintStrategyNone:
		// no-op
	default:
		return fmt.Errorf("mem_hint.strategy %q is not supported", hint.Strategy)
	}
	return nil
}
