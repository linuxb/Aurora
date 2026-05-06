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
