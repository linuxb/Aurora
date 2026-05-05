package planner

import (
	"testing"

	"aurora/apps/arqo/internal/model"
)

func TestValidateDAG_Valid(t *testing.T) {
	result := ValidateDAG([]Node{
		{NodeID: "a", NodeType: model.NodeTypeSkillSink, SkillName: "QueryLog"},
		{NodeID: "b", NodeType: model.NodeTypeSkillSink, SkillName: "LLMSummarize", Dependencies: []string{"a"}},
		{NodeID: "c", NodeType: model.NodeTypeSkillSink, SkillName: "SendEmail", Dependencies: []string{"b"}},
	})
	if !result.Valid {
		t.Fatalf("expected valid DAG, got errors=%v", result.Errors)
	}
	if len(result.Errors) != 0 {
		t.Fatalf("expected no errors, got %d", len(result.Errors))
	}
}

func TestValidateDAG_DanglingDependency(t *testing.T) {
	result := ValidateDAG([]Node{
		{NodeID: "a", NodeType: model.NodeTypeSkillSink, SkillName: "QueryLog", Dependencies: []string{"missing"}},
	})
	if result.Valid {
		t.Fatal("expected invalid result")
	}
	if len(result.Errors) == 0 || result.Errors[0].Code != ValidationErrDanglingDep {
		t.Fatalf("expected dangling dependency error, got=%v", result.Errors)
	}
}

func TestValidateDAG_Cycle(t *testing.T) {
	result := ValidateDAG([]Node{
		{NodeID: "a", NodeType: model.NodeTypeSkillSink, SkillName: "A", Dependencies: []string{"c"}},
		{NodeID: "b", NodeType: model.NodeTypeSkillSink, SkillName: "B", Dependencies: []string{"a"}},
		{NodeID: "c", NodeType: model.NodeTypeSkillSink, SkillName: "C", Dependencies: []string{"b"}},
	})
	if result.Valid {
		t.Fatal("expected invalid result for cycle")
	}
	foundCycle := false
	for _, err := range result.Errors {
		if err.Code == ValidationErrCyclicDependency {
			foundCycle = true
		}
	}
	if !foundCycle {
		t.Fatalf("expected cycle error, got=%v", result.Errors)
	}
}

func TestValidateDAG_IsolatedNodeWarning(t *testing.T) {
	result := ValidateDAG([]Node{
		{NodeID: "a", NodeType: model.NodeTypeSkillSink, SkillName: "QueryLog"},
		{NodeID: "b", NodeType: model.NodeTypeSkillSink, SkillName: "LLMSummarize", Dependencies: []string{"a"}},
		{NodeID: "isolated", NodeType: model.NodeTypeSkillSink, SkillName: "SendEmail"},
	})
	if !result.Valid {
		t.Fatalf("expected valid result, got errors=%v", result.Errors)
	}
	found := false
	for _, w := range result.Warnings {
		if w.Code == ValidationWarnIsolatedNode && w.NodeID == "isolated" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected isolated warning, got=%v", result.Warnings)
	}
}

func TestValidateDAG_MissingSkillName(t *testing.T) {
	result := ValidateDAG([]Node{
		{NodeID: "a", NodeType: model.NodeTypeSkillSink},
	})
	if result.Valid {
		t.Fatal("expected invalid result")
	}
	if len(result.Errors) == 0 || result.Errors[0].Code != ValidationErrMissingSkillName {
		t.Fatalf("expected missing skill_name error, got=%v", result.Errors)
	}
}

func TestValidateDAG_MissingNodeType(t *testing.T) {
	result := ValidateDAG([]Node{
		{NodeID: "a", SkillName: "QueryLog"},
	})
	if result.Valid {
		t.Fatal("expected invalid result")
	}
	if len(result.Errors) == 0 || result.Errors[0].Code != ValidationErrMissingNodeType {
		t.Fatalf("expected missing node_type error, got=%v", result.Errors)
	}
}

func TestValidateDAG_ExpandingNodeMustUsePlannerSkill(t *testing.T) {
	result := ValidateDAG([]Node{
		{NodeID: "a", NodeType: model.NodeTypeExpandPlanning, SkillName: "QueryLog"},
	})
	if result.Valid {
		t.Fatal("expected invalid result")
	}
	if len(result.Errors) == 0 || result.Errors[0].Code != ValidationErrInvalidNodeSkill {
		t.Fatalf("expected invalid node skill error, got=%v", result.Errors)
	}
}

func TestValidateDAG_ExpandingNodeAllowsEmptySkillName(t *testing.T) {
	result := ValidateDAG([]Node{
		{NodeID: "planner", NodeType: model.NodeTypeExpandPlanning, SkillName: ""},
		{NodeID: "sink", NodeType: model.NodeTypeSkillSink, SkillName: "SendEmail", Dependencies: []string{"planner"}},
	})
	if !result.Valid {
		t.Fatalf("expected valid result, got errors=%v", result.Errors)
	}
}
