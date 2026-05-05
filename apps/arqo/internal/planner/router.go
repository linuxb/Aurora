package planner

import "strings"

type Router interface {
	Plan(intent string, planningMode string) []Node
}

type MockRouter struct{}

func NewMockRouter() *MockRouter {
	return &MockRouter{}
}

func (r *MockRouter) Plan(intent string, planningMode string) []Node {
	normalized := strings.ToLower(strings.TrimSpace(intent))
	if strings.Contains(normalized, "invalid_dag") {
		return []Node{
			{NodeID: "node_a", Dependencies: []string{"missing_node"}},
		}
	}

	if strings.EqualFold(strings.TrimSpace(planningMode), "jit") {
		return []Node{
			{NodeID: "planner"},
			{NodeID: "final", Dependencies: []string{"planner"}},
		}
	}

	return []Node{
		{NodeID: "query_log"},
		{NodeID: "summarize", Dependencies: []string{"query_log"}},
		{NodeID: "send_email", Dependencies: []string{"summarize"}},
	}
}
