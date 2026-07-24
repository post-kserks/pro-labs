package storage

import (
	"context"
	"log"
	"sync"
	"time"
)

// AutoAnalyzeDaemon monitors table modifications and triggers ANALYZE.
type AutoAnalyzeDaemon struct {
	engine    StorageEngine
	threshold float64
	interval  time.Duration
	modCounts map[string]int
	mu        sync.Mutex
	cancel    context.CancelFunc
}

func NewAutoAnalyzeDaemon(engine StorageEngine, threshold float64, interval time.Duration) *AutoAnalyzeDaemon {
	return &AutoAnalyzeDaemon{
		engine:    engine,
		threshold: threshold,
		interval:  interval,
		modCounts: make(map[string]int),
	}
}

// RecordModification increments the modification count for a table.
func (d *AutoAnalyzeDaemon) RecordModification(table string, count int) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.modCounts[table] += count
}

func (d *AutoAnalyzeDaemon) Start(ctx context.Context) {
	ctx, cancel := context.WithCancel(ctx)
	d.cancel = cancel

	go func() {
		ticker := time.NewTicker(d.interval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				d.analyzeTables()
			}
		}
	}()
}

func (d *AutoAnalyzeDaemon) Stop() {
	if d.cancel != nil {
		d.cancel()
	}
}

func (d *AutoAnalyzeDaemon) analyzeTables() {
	d.mu.Lock()
	snapshot := make(map[string]int)
	for k, v := range d.modCounts {
		snapshot[k] = v
	}
	d.mu.Unlock()

	for table, count := range snapshot {
		if count > int(d.threshold) {
			log.Printf("Auto-ANALYZE triggered for table %s (%d modifications)", table, count)
			// Trigger actual ANALYZE here via engine.
			// e.g., d.engine.AnalyzeTable(table)

			d.mu.Lock()
			d.modCounts[table] -= count // Reset counter
			d.mu.Unlock()
		}
	}
}
