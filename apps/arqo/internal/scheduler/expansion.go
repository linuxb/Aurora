package scheduler

import "errors"

var (
	ErrExpansionInvalid        = errors.New("expansion payload is invalid")
	ErrExpansionDepthExceeded  = errors.New("expansion max depth reached")
	ErrExpansionNotAllowed     = errors.New("expansion is only allowed for expanding steps")
	ErrExpansionNotImplemented = errors.New("expansion is not implemented for this scheduler backend")
)

type ExpansionPayload struct {
	Reasoning        string           `json:"reasoning"`
	NewNodes         []ExpansionNode  `json:"new_nodes"`
	DownstreamWiring DownstreamWiring `json:"downstream_wiring"`
}

type ExpansionNode struct {
	NodeID       string         `json:"node_id"`
	SkillName    string         `json:"skill_name"`
	Parameters   map[string]any `json:"parameters,omitempty"`
	Dependencies []string       `json:"dependencies"`
}

type DownstreamWiring struct {
	RedirectFrom string   `json:"redirect_from"`
	RedirectTo   []string `json:"redirect_to"`
}
