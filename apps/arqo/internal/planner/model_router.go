package planner

import "errors"

var ErrModelPlannerUnavailable = errors.New("model planner backend is unavailable")

type ModelRouter struct{}

func NewModelRouter() *ModelRouter {
	return &ModelRouter{}
}

func (r *ModelRouter) Plan(_ string, _ string) (Plan, error) {
	return Plan{}, ErrModelPlannerUnavailable
}
