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
		"original_intent": intent,
		"macro_intent":    normalizedIntent,
		"slots": map[string]any{
			"planning_mode": normalizedMode,
		},
		"intent_router_backend": "mock_lightweight_model",
	}
}
