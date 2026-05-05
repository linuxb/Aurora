package scheduler

type SessionTaskSpec struct {
	RefID        string
	SkillName    string
	Parameters   map[string]any
	Dependencies []string
}
