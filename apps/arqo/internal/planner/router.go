package planner

import "strings"

import "aurora/apps/arqo/internal/model"

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
			{NodeID: "node_a", NodeType: model.NodeTypeSkillSink, SkillName: "QueryLog", Dependencies: []string{"missing_node"}},
		})
	}

	if strings.EqualFold(strings.TrimSpace(planningMode), "jit") {
		return NewPlan("mock", []Node{
			{NodeID: "planner", NodeType: model.NodeTypeExpandPlanning, SkillName: "ReActPlanner"},
			{NodeID: "final", NodeType: model.NodeTypeSkillSink, SkillName: "SendEmail", Dependencies: []string{"planner"}},
		})
	}

	return NewPlan("mock", []Node{
		{NodeID: "query_log", NodeType: model.NodeTypeSkillSink, SkillName: "QueryLog"},
		{NodeID: "summarize", NodeType: model.NodeTypeSkillSink, SkillName: "LLMSummarize", Dependencies: []string{"query_log"}},
		{NodeID: "send_email", NodeType: model.NodeTypeSkillSink, SkillName: "SendEmail", Dependencies: []string{"summarize"}},
	})
}
