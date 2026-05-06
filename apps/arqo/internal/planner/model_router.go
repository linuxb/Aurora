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
	endpointURL string
	modelName   string
	apiKey      string
	client      *http.Client
}

type modelPlanRequest struct {
	Intent      string `json:"intent"`
	PlanningMode string `json:"planning_mode"`
	Model       string `json:"model"`
	Schema      string `json:"schema"`
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
	if raw := strings.TrimSpace(os.Getenv("ARQO_PLANNER_MODEL_TIMEOUT_MS")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
			timeoutMS = parsed
		}
	}
	return &ModelRouter{
		endpointURL: strings.TrimSpace(os.Getenv("ARQO_PLANNER_MODEL_URL")),
		modelName:   envOrDefault("ARQO_PLANNER_MODEL_NAME", "planner-default"),
		apiKey:      strings.TrimSpace(os.Getenv("ARQO_PLANNER_MODEL_API_KEY")),
		client: &http.Client{
			Timeout: time.Duration(timeoutMS) * time.Millisecond,
		},
	}
}

func (r *ModelRouter) Plan(intent string, planningMode string) (Plan, error) {
	if strings.TrimSpace(r.endpointURL) == "" {
		return Plan{}, ErrModelPlannerUnavailable
	}

	reqBody, _ := json.Marshal(modelPlanRequest{
		Intent:      intent,
		PlanningMode: planningMode,
		Model:       r.modelName,
		Schema:      "arqo_plan_v1",
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
	}
	return plan, nil
}

func envOrDefault(key, fallback string) string {
	if val := strings.TrimSpace(os.Getenv(key)); val != "" {
		return val
	}
	return fallback
}
