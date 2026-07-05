package planner

import (
	"os"
	"strings"
)

var defaultRegisteredSkills = []string{
	"QueryLog",
	"LLMSummarize",
	"SendEmail",
	"ReActPlanner",
}

func RegisteredSkillsFromEnv() []string {
	raw := strings.TrimSpace(os.Getenv("FLORY_REGISTERED_SKILLS"))
	if raw == "" {
		return append([]string{}, defaultRegisteredSkills...)
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	seen := make(map[string]struct{}, len(parts))
	for _, p := range parts {
		s := strings.TrimSpace(p)
		if s == "" {
			continue
		}
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	if len(out) == 0 {
		return append([]string{}, defaultRegisteredSkills...)
	}
	return out
}
