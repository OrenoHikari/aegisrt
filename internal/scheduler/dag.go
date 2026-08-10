package scheduler

import (
	"errors"
	"fmt"
	"strings"
)

var (
	ErrDAGDuplicateNode     = errors.New("DAG node ID is duplicated")
	ErrDAGUnknownDependency = errors.New("DAG dependency does not exist")
	ErrDAGCycle             = errors.New("DAG contains a cycle")
)

// DAGNode is the dependency information required to validate a graph before
// its jobs are submitted to Scheduler.
type DAGNode struct {
	ID        string
	DependsOn []string
}

// ValidateDAG validates a complete dependency graph and returns a stable
// topological order. Nodes that are otherwise independent retain input order.
//
// Scheduler.Submit still performs its normal per-job dependency checks. This
// whole-graph validation exists for callers such as task planners that receive
// an entire DAG before submission.
func ValidateDAG(nodes []DAGNode) ([]string, error) {
	if len(nodes) == 0 {
		return nil, fmt.Errorf("DAG must contain at least one node")
	}

	indexes := make(map[string]int, len(nodes))
	for index, node := range nodes {
		id := strings.TrimSpace(node.ID)
		if id == "" {
			return nil, fmt.Errorf("DAG node ID is required")
		}

		if _, exists := indexes[id]; exists {
			return nil, fmt.Errorf("%w: %s", ErrDAGDuplicateNode, id)
		}

		indexes[id] = index
	}

	indegree := make([]int, len(nodes))
	dependents := make([][]int, len(nodes))

	for index, node := range nodes {
		seen := make(map[string]struct{}, len(node.DependsOn))

		for _, rawDependency := range node.DependsOn {
			dependencyID := strings.TrimSpace(rawDependency)
			if dependencyID == "" {
				return nil, fmt.Errorf("node %s has an empty dependency", node.ID)
			}

			if _, duplicate := seen[dependencyID]; duplicate {
				continue
			}
			seen[dependencyID] = struct{}{}

			dependencyIndex, exists := indexes[dependencyID]
			if !exists {
				return nil, fmt.Errorf(
					"%w: node %s depends on %s",
					ErrDAGUnknownDependency,
					node.ID,
					dependencyID,
				)
			}

			indegree[index]++
			dependents[dependencyIndex] = append(dependents[dependencyIndex], index)
		}
	}

	ready := make([]int, 0, len(nodes))
	for index, count := range indegree {
		if count == 0 {
			ready = append(ready, index)
		}
	}

	order := make([]string, 0, len(nodes))

	for len(ready) > 0 {
		index := ready[0]
		ready = ready[1:]
		order = append(order, strings.TrimSpace(nodes[index].ID))

		for _, dependentIndex := range dependents[index] {
			indegree[dependentIndex]--
			if indegree[dependentIndex] == 0 {
				ready = insertStableIndex(ready, dependentIndex)
			}
		}
	}

	if len(order) != len(nodes) {
		return nil, ErrDAGCycle
	}

	return order, nil
}

func insertStableIndex(indexes []int, value int) []int {
	position := len(indexes)

	for index, current := range indexes {
		if value < current {
			position = index
			break
		}
	}

	indexes = append(indexes, 0)
	copy(indexes[position+1:], indexes[position:])
	indexes[position] = value

	return indexes
}
