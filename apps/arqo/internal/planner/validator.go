package planner

import "fmt"

type Node struct {
	NodeID       string
	Dependencies []string
}

type ValidationErrorCode string

const (
	ValidationErrDuplicateNode    ValidationErrorCode = "DUPLICATE_NODE"
	ValidationErrMissingNodeID    ValidationErrorCode = "MISSING_NODE_ID"
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
