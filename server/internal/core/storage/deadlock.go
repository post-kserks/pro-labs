package storage

import (
	"sync"
)

// DeadlockDetector manages a Wait-For Graph (WFG) to detect deadlocks among transactions.
type DeadlockDetector struct {
	mu sync.Mutex
	// wfg maps a transaction ID to the list of transaction IDs it is waiting for.
	wfg map[uint64][]uint64
}

func NewDeadlockDetector() *DeadlockDetector {
	return &DeadlockDetector{
		wfg: make(map[uint64][]uint64),
	}
}

// AddEdge adds a directed edge from `waiter` to `holder`.
// It returns true if a deadlock (cycle) is detected after adding this edge.
func (d *DeadlockDetector) AddEdge(waiter, holder uint64) bool {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.wfg[waiter] = append(d.wfg[waiter], holder)

	return d.detectCycle(waiter)
}

// RemoveEdge removes a directed edge from `waiter` to `holder`.
func (d *DeadlockDetector) RemoveEdge(waiter, holder uint64) {
	d.mu.Lock()
	defer d.mu.Unlock()

	edges := d.wfg[waiter]
	for i, h := range edges {
		if h == holder {
			// Remove the edge
			d.wfg[waiter] = append(edges[:i], edges[i+1:]...)
			break
		}
	}
}

// RemoveNode removes all edges involving `tx` when it commits or aborts.
func (d *DeadlockDetector) RemoveNode(tx uint64) {
	d.mu.Lock()
	defer d.mu.Unlock()

	delete(d.wfg, tx)
	for waiter, holders := range d.wfg {
		var newHolders []uint64
		for _, h := range holders {
			if h != tx {
				newHolders = append(newHolders, h)
			}
		}
		d.wfg[waiter] = newHolders
	}
}

// detectCycle checks if there is a cycle in the WFG starting from `startNode`.
// It uses DFS.
func (d *DeadlockDetector) detectCycle(startNode uint64) bool {
	visited := make(map[uint64]bool)
	recursionStack := make(map[uint64]bool)

	var dfs func(node uint64) bool
	dfs = func(node uint64) bool {
		visited[node] = true
		recursionStack[node] = true

		for _, neighbor := range d.wfg[node] {
			if !visited[neighbor] {
				if dfs(neighbor) {
					return true
				}
			} else if recursionStack[neighbor] {
				return true
			}
		}

		recursionStack[node] = false
		return false
	}

	return dfs(startNode)
}
