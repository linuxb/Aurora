package scheduler

import "errors"
import "strings"

import "aurora/apps/arqo/internal/model"

var (
	ErrExpansionInvalid        = errors.New("expansion payload is invalid")
	ErrExpansionDepthExceeded  = errors.New("expansion max depth reached")
	ErrExpansionNotAllowed     = errors.New("expansion is only allowed for expanding steps")
	ErrExpansionNotImplemented = errors.New("expansion is not implemented for this scheduler backend")
	ErrSkillMappingExhausted   = errors.New("skill mapping exhausted, missing required skill")
)

const (
	ExpansionMappingMapped   = "mapped"
	ExpansionMappingUnmapped = "unmapped"
)

type ExpansionPayload struct {
	Reasoning        string           `json:"reasoning"`
	MappingStatus    string           `json:"mapping_status"`
	NewNodes         []ExpansionNode  `json:"new_nodes"`
	DownstreamWiring DownstreamWiring `json:"downstream_wiring"`
}

type ExpansionNode struct {
	NodeID       string         `json:"node_id"`
	NodeType     model.NodeType `json:"node_type"`
	SkillName    string         `json:"skill_name"`
	MemHint      *MemHint       `json:"mem_hint,omitempty"`
	Parameters   map[string]any `json:"parameters,omitempty"`
	Dependencies []string       `json:"dependencies"`
}

type DownstreamWiring struct {
	RedirectFrom string   `json:"redirect_from"`
	RedirectTo   []string `json:"redirect_to"`
}

func normalizeMappingStatus(raw string) string {
	return strings.ToLower(strings.TrimSpace(raw))
}
