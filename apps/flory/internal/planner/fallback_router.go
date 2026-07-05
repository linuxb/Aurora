package planner

import "fmt"

type FallbackRouter struct {
	primary   Router
	secondary Router
}

func NewFallbackRouter(primary Router, secondary Router) *FallbackRouter {
	return &FallbackRouter{
		primary:   primary,
		secondary: secondary,
	}
}

func (r *FallbackRouter) Plan(intent string, planningMode string) (Plan, error) {
	if r.primary == nil {
		return Plan{}, fmt.Errorf("primary planner router is nil")
	}
	plan, err := r.primary.Plan(intent, planningMode)
	if err == nil {
		return plan, nil
	}
	if r.secondary == nil {
		return Plan{}, err
	}
	return r.secondary.Plan(intent, planningMode)
}

func (r *FallbackRouter) ExtractIntent(intent string, planningMode string) (map[string]any, error) {
	if extractor, ok := r.primary.(IntentExtractor); ok {
		context, err := extractor.ExtractIntent(intent, planningMode)
		if err == nil {
			return context, nil
		}
	}
	if extractor, ok := r.secondary.(IntentExtractor); ok {
		return extractor.ExtractIntent(intent, planningMode)
	}
	return map[string]any{"original_intent": intent}, nil
}

func (r *FallbackRouter) PlanWithContext(intent string, planningMode string, intentContext map[string]any) (Plan, error) {
	if contextual, ok := r.primary.(ContextPlanner); ok {
		plan, err := contextual.PlanWithContext(intent, planningMode, intentContext)
		if err == nil {
			return plan, nil
		}
	}
	if contextual, ok := r.secondary.(ContextPlanner); ok {
		return contextual.PlanWithContext(intent, planningMode, intentContext)
	}
	return r.Plan(intent, planningMode)
}
