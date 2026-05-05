package planner

import "strings"

type Router interface {
	Plan(intent string, planningMode string) Plan
}

type MockRouter struct{}

func NewMockRouter() *MockRouter {
	return &MockRouter{}
}

func (r *MockRouter) Plan(intent string, planningMode string) Plan {
	normalized := strings.ToLower(strings.TrimSpace(intent))
	if strings.Contains(normalized, "invalid_dag") {
		return NewPlan("mock", []Node{
			{NodeID: "node_a", Dependencies: []string{"missing_node"}},
		})
	}

	if strings.EqualFold(strings.TrimSpace(planningMode), "jit") {
		return NewPlan("mock", []Node{
			{NodeID: "planner"},
			{NodeID: "final", Dependencies: []string{"planner"}},
		})
	}

	return NewPlan("mock", []Node{
		{NodeID: "query_log"},
		{NodeID: "summarize", Dependencies: []string{"query_log"}},
		{NodeID: "send_email", Dependencies: []string{"summarize"}},
	})
}
