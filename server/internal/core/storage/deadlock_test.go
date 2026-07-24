package storage

import (
	"testing"
)

func TestDeadlockWFG(t *testing.T) {
	d := NewDeadlockDetector()

	// A -> B
	if d.AddEdge(1, 2) {
		t.Errorf("Expected no deadlock for A -> B")
	}

	// B -> C
	if d.AddEdge(2, 3) {
		t.Errorf("Expected no deadlock for B -> C")
	}

	// C -> A (Deadlock!)
	if !d.AddEdge(3, 1) {
		t.Errorf("Expected deadlock for C -> A")
	}

	// Remove cycle and check again
	d.RemoveEdge(3, 1)
	if d.detectCycle(1) {
		t.Errorf("Cycle should have been removed")
	}

	// D -> E
	d.AddEdge(4, 5)
	// Remove node D
	d.RemoveNode(4)
	if len(d.wfg[4]) != 0 {
		t.Errorf("Expected node 4 to be completely removed")
	}
}
