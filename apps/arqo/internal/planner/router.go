package planner

import "strings"

import "aurora/apps/arqo/internal/model"
import "aurora/apps/arqo/internal/scheduler"

type Router interface {
	Plan(intent string, planningMode string) (Plan, error)
}

type IntentExtractor interface {
	ExtractIntent(intent string, planningMode string) (map[string]any, error)
}

type ContextPlanner interface {
	PlanWithContext(intent string, planningMode string, intentContext map[string]any) (Plan, error)
}

func NewMockRouter() *MockRouter {
	return &MockRouter{
		lightweightModel: NewMockLightweightIntentModel(),
		registeredSkills: RegisteredSkillsFromEnv(),
	}
}

type MockRouter struct {
	lightweightModel LightweightIntentModel
	registeredSkills []string
}

func (r *MockRouter) Plan(intent string, planningMode string) (Plan, error) {
	intentContext, err := r.ExtractIntent(intent, planningMode)
	if err != nil {
		return Plan{}, err
	}
	return r.PlanWithContext(intent, planningMode, intentContext)
}

func (r *MockRouter) ExtractIntent(intent string, planningMode string) (map[string]any, error) {
	planContext := r.lightweightModel.Extract(intent, planningMode)
	planContext["registered_skills"] = append([]string{}, r.registeredSkills...)
	return planContext, nil
}

func (r *MockRouter) PlanWithContext(intent string, planningMode string, planContext map[string]any) (Plan, error) {
	normalized := strings.ToLower(strings.TrimSpace(intent))
	if strings.Contains(normalized, "invalid_dag") {
		plan := NewPlan("mock", []Node{
			{NodeID: "node_a", NodeType: model.NodeTypeSkill, SkillName: "QueryLog", MemHint: scheduler.DefaultMemHint(), Dependencies: []string{"missing_node"}},
		})
		plan.IntentContext = planContext
		return plan, nil
	}

	if strings.EqualFold(strings.TrimSpace(planningMode), "jit") {
		plan := NewPlan("mock", []Node{
			{NodeID: "planner", NodeType: model.NodeTypePlanner, SkillName: "ReActPlanner", Goal: intent, MemHint: scheduler.DefaultMemHint()},
			{NodeID: "final", NodeType: model.NodeTypeSkill, SkillName: "SendEmail", MemHint: scheduler.DefaultMemHint(), Dependencies: []string{"planner"}},
		})
		plan.IntentContext = planContext
		return plan, nil
	}

	plan := NewPlan("mock", []Node{
		{NodeID: "query_log", NodeType: model.NodeTypeSkill, SkillName: "QueryLog", MemHint: scheduler.DefaultMemHint()},
		{NodeID: "summarize", NodeType: model.NodeTypeSkill, SkillName: "LLMSummarize", MemHint: scheduler.DefaultMemHint(), Dependencies: []string{"query_log"}},
		{NodeID: "send_email", NodeType: model.NodeTypeSkill, SkillName: "SendEmail", MemHint: scheduler.DefaultMemHint(), Dependencies: []string{"summarize"}},
	})
	plan.IntentContext = planContext
	return plan, nil
}
