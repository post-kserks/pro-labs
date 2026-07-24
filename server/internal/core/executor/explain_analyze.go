package executor

import (
	"fmt"
	"time"
)

// ExplainAnalyzeTracker tracks execution statistics for physical plan nodes.
type ExplainAnalyzeTracker struct {
	NodeName     string
	StartTime    time.Time
	EndTime      time.Time
	RowsProduced int
	BufferHits   int
	BufferMisses int
	Children     []*ExplainAnalyzeTracker
}

func NewExplainAnalyzeTracker(nodeName string) *ExplainAnalyzeTracker {
	return &ExplainAnalyzeTracker{
		NodeName: nodeName,
	}
}

func (t *ExplainAnalyzeTracker) Start() {
	t.StartTime = time.Now()
}

func (t *ExplainAnalyzeTracker) Stop() {
	t.EndTime = time.Now()
}

func (t *ExplainAnalyzeTracker) AddRow() {
	t.RowsProduced++
}

func (t *ExplainAnalyzeTracker) RecordBufferHit() {
	t.BufferHits++
}

func (t *ExplainAnalyzeTracker) RecordBufferMiss() {
	t.BufferMisses++
}

func (t *ExplainAnalyzeTracker) AddChild(child *ExplainAnalyzeTracker) {
	t.Children = append(t.Children, child)
}

func (t *ExplainAnalyzeTracker) String() string {
	return t.format(0)
}

func (t *ExplainAnalyzeTracker) format(indent int) string {
	prefix := ""
	for i := 0; i < indent; i++ {
		prefix += "  "
	}

	duration := t.EndTime.Sub(t.StartTime)
	result := fmt.Sprintf("%s-> %s (actual time=%.3f ms rows=%d loops=1)\n", prefix, t.NodeName, float64(duration.Microseconds())/1000.0, t.RowsProduced)
	if t.BufferHits > 0 || t.BufferMisses > 0 {
		result += fmt.Sprintf("%s     Buffers: shared hit=%d read=%d\n", prefix, t.BufferHits, t.BufferMisses)
	}

	for _, child := range t.Children {
		result += child.format(indent + 1)
	}
	return result
}
