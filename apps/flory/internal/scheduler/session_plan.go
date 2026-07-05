package scheduler

import (
	"encoding/json"

	"aurora/apps/flory/internal/model"
)

type SessionTaskSpec struct {
	RefID        string
	NodeType     model.NodeType
	SkillName    string
	Goal         string
	MemHint      MemHint
	Parameters   map[string]any
	Dependencies []string
}

type SessionIdentity struct {
	SessionID string
	DAGID     string
}

type CreateSessionPlanInput struct {
	Identity      SessionIdentity
	TenantID      string
	AgentID       string
	UserID        string
	Intent        string
	IntentContext map[string]any
	Tasks         []SessionTaskSpec
}

func NewSessionIdentity() (SessionIdentity, error) {
	sessionID, err := newPrefixedID("sess")
	if err != nil {
		return SessionIdentity{}, err
	}
	dagID, err := newPrefixedID("dag")
	if err != nil {
		return SessionIdentity{}, err
	}
	return SessionIdentity{SessionID: sessionID, DAGID: dagID}, nil
}

func memHintMap(hint MemHint) map[string]any {
	if hint.Version == "" {
		hint = DefaultMemHint()
	}
	raw, err := json.Marshal(hint)
	if err != nil {
		return map[string]any{"version": "1.0", "strategy": string(MemHintStrategyNone)}
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return map[string]any{"version": "1.0", "strategy": string(MemHintStrategyNone)}
	}
	delete(out, "target_step_id")
	delete(out, "semantic_query")
	return out
}
