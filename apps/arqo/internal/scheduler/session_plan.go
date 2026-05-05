package scheduler

import "aurora/apps/arqo/internal/model"

type SessionTaskSpec struct {
	RefID        string
	NodeType     model.NodeType
	SkillName    string
	Parameters   map[string]any
	Dependencies []string
}
