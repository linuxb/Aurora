package planner

import "fmt"

import "aurora/apps/arqo/internal/model"

type Node struct {
	NodeID       string         `json:"node_id"`
	NodeType     model.NodeType `json:"node_type"`
	SkillName    string         `json:"skill_name"`
	Parameters   map[string]any `json:"parameters,omitempty"`
	Dependencies []string       `json:"dependencies"`
}

type ValidationErrorCode string

const (
	ValidationErrDuplicateNode    ValidationErrorCode = "DUPLICATE_NODE"
	ValidationErrMissingNodeID    ValidationErrorCode = "MISSING_NODE_ID"
	ValidationErrMissingNodeType  ValidationErrorCode = "MISSING_NODE_TYPE"
	ValidationErrInvalidNodeType  ValidationErrorCode = "INVALID_NODE_TYPE"
	ValidationErrMissingSkillName ValidationErrorCode = "MISSING_SKILL_NAME"
	ValidationErrInvalidNodeSkill ValidationErrorCode = "INVALID_NODE_SKILL"
	ValidationErrDanglingDep      ValidationErrorCode = "DANGLING_DEPENDENCY"
	ValidationErrSelfDependency   ValidationErrorCode = "SELF_DEPENDENCY"
	ValidationErrCyclicDependency ValidationErrorCode = "CYCLIC_DEPENDENCY"
)

type ValidationError struct {
	Code    ValidationErrorCode `json:"code"`
	NodeID  string              `json:"node_id,omitempty"`
	Depends string              `json:"depends,omitempty"`
	Detail  string              `json:"detail"`
}

type ValidationWarningCode string

const (
	ValidationWarnIsolatedNode ValidationWarningCode = "ISOLATED_NODE"
)

type ValidationWarning struct {
	Code   ValidationWarningCode `json:"code"`
	NodeID string                `json:"node_id"`
	Detail string                `json:"detail"`
}

type ValidationResult struct {
	Valid    bool                `json:"valid"`
	Errors   []ValidationError   `json:"errors,omitempty"`
	Warnings []ValidationWarning `json:"warnings,omitempty"`
}

func ValidateDAG(nodes []Node) ValidationResult {
	result := ValidationResult{Valid: true}
	nodeMap := make(map[string]Node, len(nodes))
	inDegree := make(map[string]int, len(nodes))
	outDegree := make(map[string]int, len(nodes))

	for _, node := range nodes {
		if node.NodeID == "" {
			result.Valid = false
			result.Errors = append(result.Errors, ValidationError{
				Code:   ValidationErrMissingNodeID,
				Detail: "node_id is required",
			})
			continue
		}
		if node.NodeType == "" {
			result.Valid = false
			result.Errors = append(result.Errors, ValidationError{
				Code:   ValidationErrMissingNodeType,
				NodeID: node.NodeID,
				Detail: "node_type is required",
			})
			continue
		}
		parsedNodeType, err := model.ParseNodeType(string(node.NodeType))
		if err != nil {
			result.Valid = false
			result.Errors = append(result.Errors, ValidationError{
				Code:   ValidationErrInvalidNodeType,
				NodeID: node.NodeID,
				Detail: fmt.Sprintf("node_type %q is not supported", node.NodeType),
			})
			continue
		}
		node.NodeType = parsedNodeType
		if node.NodeType == model.NodeTypeSkillSink {
			if node.SkillName == "" {
				result.Valid = false
				result.Errors = append(result.Errors, ValidationError{
					Code:   ValidationErrMissingSkillName,
					NodeID: node.NodeID,
					Detail: "skill_name is required for skill sink node",
				})
				continue
			}
		}
		if node.NodeType == model.NodeTypeExpandPlanning && node.SkillName != "" && node.SkillName != "ReActPlanner" {
			result.Valid = false
			result.Errors = append(result.Errors, ValidationError{
				Code:   ValidationErrInvalidNodeSkill,
				NodeID: node.NodeID,
				Detail: "expanding node must use ReActPlanner skill when skill_name is provided",
			})
			continue
		}
		if node.NodeType == model.NodeTypeSkillSink && node.SkillName == "ReActPlanner" {
			result.Valid = false
			result.Errors = append(result.Errors, ValidationError{
				Code:   ValidationErrInvalidNodeSkill,
				NodeID: node.NodeID,
				Detail: "ReActPlanner must be declared as EXPAND_PLANNING node",
			})
			continue
		}
		if _, ok := nodeMap[node.NodeID]; ok {
			result.Valid = false
			result.Errors = append(result.Errors, ValidationError{
				Code:   ValidationErrDuplicateNode,
				NodeID: node.NodeID,
				Detail: fmt.Sprintf("duplicate node_id %q", node.NodeID),
			})
			continue
		}
		nodeMap[node.NodeID] = node
		inDegree[node.NodeID] = 0
		outDegree[node.NodeID] = 0
	}

	for _, node := range nodeMap {
		for _, dep := range node.Dependencies {
			if dep == node.NodeID {
				result.Valid = false
				result.Errors = append(result.Errors, ValidationError{
					Code:   ValidationErrSelfDependency,
					NodeID: node.NodeID,
					Detail: "self dependency is not allowed",
				})
				continue
			}
			if _, ok := nodeMap[dep]; !ok {
				result.Valid = false
				result.Errors = append(result.Errors, ValidationError{
					Code:    ValidationErrDanglingDep,
					NodeID:  node.NodeID,
					Depends: dep,
					Detail:  fmt.Sprintf("dependency %q does not exist", dep),
				})
				continue
			}
			inDegree[node.NodeID]++
			outDegree[dep]++
		}
	}

	if !result.Valid {
		return result
	}

	// Kahn's algorithm for cycle detection.
	queue := make([]string, 0, len(nodeMap))
	for id, degree := range inDegree {
		if degree == 0 {
			queue = append(queue, id)
		}
	}

	visited := 0
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		visited++

		for _, node := range nodeMap {
			for _, dep := range node.Dependencies {
				if dep != id {
					continue
				}
				inDegree[node.NodeID]--
				if inDegree[node.NodeID] == 0 {
					queue = append(queue, node.NodeID)
				}
			}
		}
	}

	if visited != len(nodeMap) {
		result.Valid = false
		result.Errors = append(result.Errors, ValidationError{
			Code:   ValidationErrCyclicDependency,
			Detail: "cycle detected in DAG",
		})
		return result
	}

	for id := range nodeMap {
		if inDegree[id] == 0 && outDegree[id] == 0 {
			result.Warnings = append(result.Warnings, ValidationWarning{
				Code:   ValidationWarnIsolatedNode,
				NodeID: id,
				Detail: "node is isolated from the graph",
			})
		}
	}

	return result
}
