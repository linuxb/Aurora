package planner

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

var ErrModelPlannerUnavailable = errors.New("model planner backend is unavailable")

type ModelRouter struct {
	endpointURL      string
	modelName        string
	apiKey           string
	client           *http.Client
	registeredSkills []string
}

type modelPlanRequest struct {
	Intent           string         `json:"intent"`
	PlanningMode     string         `json:"planning_mode"`
	IntentContext    map[string]any `json:"intent_context,omitempty"`
	Model            string         `json:"model"`
	Schema           string         `json:"schema"`
	RegisteredSkills []string       `json:"registered_skills,omitempty"`
	JSONSchema       map[string]any `json:"json_schema,omitempty"`
}

type modelPlanResponse struct {
	Plan *Plan `json:"plan,omitempty"`

	PlanID        string         `json:"plan_id,omitempty"`
	Source        string         `json:"source,omitempty"`
	IntentContext map[string]any `json:"intent_context,omitempty"`
	Nodes         []Node         `json:"nodes,omitempty"`
}

func NewModelRouterFromEnv() *ModelRouter {
	timeoutMS := 3000
	if raw := strings.TrimSpace(os.Getenv("FLORY_PLANNER_MODEL_TIMEOUT_MS")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
			timeoutMS = parsed
		}
	}
	return &ModelRouter{
		endpointURL: strings.TrimSpace(os.Getenv("FLORY_PLANNER_MODEL_URL")),
		modelName:   envOrDefault("FLORY_PLANNER_MODEL_NAME", "planner-default"),
		apiKey:      strings.TrimSpace(os.Getenv("FLORY_PLANNER_MODEL_API_KEY")),
		client: &http.Client{
			Timeout: time.Duration(timeoutMS) * time.Millisecond,
		},
		registeredSkills: RegisteredSkillsFromEnv(),
	}
}

func (r *ModelRouter) Plan(intent string, planningMode string) (Plan, error) {
	intentContext, err := r.ExtractIntent(intent, planningMode)
	if err != nil {
		return Plan{}, err
	}
	return r.PlanWithContext(intent, planningMode, intentContext)
}

func (r *ModelRouter) ExtractIntent(intent string, planningMode string) (map[string]any, error) {
	return NewMockLightweightIntentModel().Extract(intent, planningMode), nil
}

func (r *ModelRouter) PlanWithContext(intent string, planningMode string, intentContext map[string]any) (Plan, error) {
	if strings.TrimSpace(r.endpointURL) == "" {
		return Plan{}, ErrModelPlannerUnavailable
	}

	reqBody, _ := json.Marshal(modelPlanRequest{
		Intent:           intent,
		PlanningMode:     planningMode,
		IntentContext:    intentContext,
		Model:            r.modelName,
		Schema:           "flory_plan_v1",
		RegisteredSkills: append([]string{}, r.registeredSkills...),
		JSONSchema:       dagPlanJSONSchema(),
	})
	req, err := http.NewRequest(http.MethodPost, r.endpointURL, bytes.NewReader(reqBody))
	if err != nil {
		return Plan{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	if r.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+r.apiKey)
	}

	resp, err := r.client.Do(req)
	if err != nil {
		return Plan{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return Plan{}, errors.New("model planner returned non-2xx status")
	}

	var payload modelPlanResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return Plan{}, err
	}

	if payload.Plan != nil {
		plan := *payload.Plan
		if plan.Source == "" {
			plan.Source = "model"
		}
		if plan.PlanID == "" {
			plan.PlanID = NewPlan("model", plan.Nodes).PlanID
		}
		return plan, nil
	}

	plan := NewPlan("model", payload.Nodes)
	if payload.Source != "" {
		plan.Source = payload.Source
	}
	if payload.PlanID != "" {
		plan.PlanID = payload.PlanID
	}
	if payload.IntentContext != nil {
		plan.IntentContext = payload.IntentContext
	} else {
		plan.IntentContext = intentContext
	}
	return plan, nil
}

func dagPlanJSONSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"nodes": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"node_id": map[string]any{"type": "string"},
						"node_type": map[string]any{
							"type": "string",
							"enum": []string{"skill", "planner"},
						},
						"skill_name": map[string]any{"type": "string"},
						"goal":       map[string]any{"type": "string"},
						"dependencies": map[string]any{
							"type":  "array",
							"items": map[string]any{"type": "string"},
						},
						"mem_hint": map[string]any{
							"type": "object",
							"properties": map[string]any{
								"strategy": map[string]any{
									"type": "string",
									"enum": []string{"KV_POINT_GET", "GRAPH_LOCAL_TRAVERSAL", "GRAPH_GLOBAL_SUMMARY", "NONE"},
								},
								"version":       map[string]any{"type": "string", "const": "1.0"},
								"target_system": map[string]any{"type": "string", "enum": []string{"AUTO", "MEM3_KV", "PLATO_GRAPH"}},
								"query": map[string]any{
									"type": "object",
									"properties": map[string]any{
										"text":           map[string]any{"type": "string"},
										"keywords":       map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
										"target_task_id": map[string]any{"type": "string"},
									},
								},
							},
							"required": []string{"version", "strategy"},
						},
					},
					"required": []string{"node_id", "node_type", "mem_hint", "dependencies"},
				},
			},
		},
		"required": []string{"nodes"},
	}
}

func envOrDefault(key, fallback string) string {
	if val := strings.TrimSpace(os.Getenv(key)); val != "" {
		return val
	}
	return fallback
}
