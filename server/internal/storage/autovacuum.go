package storage

import (
	"context"
	"fmt"
	"log/slog"
	"time"
)

// AutoVacuum запускает vacuum для таблицы если доля устаревших строк
// превышает threshold (по умолчанию 20%).
type AutoVacuum struct {
	engine    StorageEngine
	threshold float64       // 0.2 = 20%
	interval  time.Duration // как часто проверять
	logger    *slog.Logger
}

func NewAutoVacuum(engine StorageEngine, threshold float64, interval time.Duration, logger *slog.Logger) *AutoVacuum {
	if logger == nil {
		logger = slog.Default()
	}
	if threshold <= 0 {
		threshold = 0.2
	}
	if interval <= 0 {
		interval = 1 * time.Minute
	}
	return &AutoVacuum{
		engine:    engine,
		threshold: threshold,
		interval:  interval,
		logger:    logger,
	}
}

func (av *AutoVacuum) Run(ctx context.Context) {
	defer func() {
		if r := recover(); r != nil {
			av.logger.Error("panic in autovacuum", "panic", r)
		}
	}()
	ticker := time.NewTicker(av.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			av.checkAndVacuum(ctx)
		}
	}
}

func (av *AutoVacuum) checkAndVacuum(ctx context.Context) {
	dbs, err := av.engine.ListDatabases()
	if err != nil {
		return
	}
	for _, db := range dbs {
		tables, err := av.engine.ListTables(db)
		if err != nil {
			continue
		}
		for _, t := range tables {
			stats, err := av.engine.TableVersionStats(db, t.Name)
			if err != nil {
				continue
			}
			if stats.TotalRows == 0 {
				continue
			}
			deadRatio := float64(stats.DeadRows) / float64(stats.TotalRows)
			if deadRatio > av.threshold {
				av.logger.Info("autovacuum triggered",
					"db", db,
					"table", t.Name,
					"dead_ratio", fmt.Sprintf("%.1f%%", deadRatio*100))
				_, err := av.engine.Vacuum(db, t.Name)
				if err != nil {
					av.logger.Error("autovacuum failed", "db", db, "table", t.Name, "error", err)
				}
			}
		}
	}
}
