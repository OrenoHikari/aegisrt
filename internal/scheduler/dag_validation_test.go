package scheduler

import (
	"errors"
	"reflect"
	"testing"
)

func TestValidateDAGReturnsStableTopologicalOrder(t *testing.T) {
	nodes := []DAGNode{
		{ID: "report", DependsOn: []string{"analysis"}},
		{ID: "inspect"},
		{ID: "independent"},
		{ID: "analysis", DependsOn: []string{"inspect"}},
	}

	order, err := ValidateDAG(nodes)
	if err != nil {
		t.Fatalf("validate DAG: %v", err)
	}

	expected := []string{"inspect", "independent", "analysis", "report"}
	if !reflect.DeepEqual(order, expected) {
		t.Fatalf("expected order %v, got %v", expected, order)
	}
}

func TestValidateDAGRejectsDuplicateNode(t *testing.T) {
	_, err := ValidateDAG([]DAGNode{{ID: "task-1"}, {ID: "task-1"}})
	if !errors.Is(err, ErrDAGDuplicateNode) {
		t.Fatalf("expected duplicate node error, got %v", err)
	}
}

func TestValidateDAGRejectsUnknownDependency(t *testing.T) {
	_, err := ValidateDAG([]DAGNode{{ID: "task-1", DependsOn: []string{"missing"}}})
	if !errors.Is(err, ErrDAGUnknownDependency) {
		t.Fatalf("expected unknown dependency error, got %v", err)
	}
}

func TestValidateDAGRejectsCycle(t *testing.T) {
	_, err := ValidateDAG([]DAGNode{
		{ID: "task-1", DependsOn: []string{"task-2"}},
		{ID: "task-2", DependsOn: []string{"task-1"}},
	})
	if !errors.Is(err, ErrDAGCycle) {
		t.Fatalf("expected cycle error, got %v", err)
	}
}
