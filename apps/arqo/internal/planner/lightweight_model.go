package planner

import "strings"

type LightweightIntentModel interface {
	Extract(intent string, planningMode string) map[string]any
}

type MockLightweightIntentModel struct{}

func NewMockLightweightIntentModel() *MockLightweightIntentModel {
	return &MockLightweightIntentModel{}
}

func (m *MockLightweightIntentModel) Extract(intent string, planningMode string) map[string]any {
	normalizedIntent := strings.ToLower(strings.TrimSpace(intent))
	normalizedMode := strings.ToLower(strings.TrimSpace(planningMode))
	return map[string]any{
		"macro_intent":          normalizedIntent,
		"entities":              []string{},
		"temporal_context":      "",
		"action_verbs":          []string{},
		"extraction_hint":       "mock lightweight intent extraction",
		"original_intent":       intent,
		"planning_mode":         normalizedMode,
		"intent_router_backend": "mock_lightweight_model",
	}
}
