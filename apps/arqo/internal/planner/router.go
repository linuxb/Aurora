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
			{NodeID: "node_a", SkillName: "QueryLog", Dependencies: []string{"missing_node"}},
		})
	}

	if strings.EqualFold(strings.TrimSpace(planningMode), "jit") {
		return NewPlan("mock", []Node{
			{NodeID: "planner", SkillName: "ReActPlanner"},
			{NodeID: "final", SkillName: "SendEmail", Dependencies: []string{"planner"}},
		})
	}

	return NewPlan("mock", []Node{
		{NodeID: "query_log", SkillName: "QueryLog"},
		{NodeID: "summarize", SkillName: "LLMSummarize", Dependencies: []string{"query_log"}},
		{NodeID: "send_email", SkillName: "SendEmail", Dependencies: []string{"summarize"}},
	})
}
