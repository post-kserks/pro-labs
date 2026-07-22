package ddl

import (
	"fmt"
	"math"
	"sort"

	"vaultdb/internal/core/executor/eval"
	"vaultdb/internal/core/executor/optimizer"
	"vaultdb/internal/core/executor/types"
	"vaultdb/internal/core/parser"
)

func init() {
	types.RegisterCommand("ANALYZE", func(stmt parser.Statement) types.Command {
		return &AnalyzeCommand{stmt: stmt.(*parser.AnalyzeStatement)}
	})
}

type AnalyzeCommand struct {
	stmt *parser.AnalyzeStatement
}

func (c *AnalyzeCommand) Execute(ctx *types.ExecutionContext) (*types.Result, error) {
	dbName := ctx.Session.CurrentDatabase()
	if dbName == "" {
		return nil, fmt.Errorf("no database selected")
	}

	var tablesToAnalyze []string
	if c.stmt.TableName != "" {
		if !ctx.Storage.TableExists(dbName, c.stmt.TableName) {
			return nil, fmt.Errorf("table '%s' does not exist", c.stmt.TableName)
		}
		tablesToAnalyze = append(tablesToAnalyze, c.stmt.TableName)
	} else {
		tables, err := ctx.Storage.ListTables(dbName)
		if err != nil {
			return nil, err
		}
		for _, info := range tables {
			tablesToAnalyze = append(tablesToAnalyze, info.Name)
		}
	}

	for _, tblName := range tablesToAnalyze {
		if err := analyzeTable(ctx, dbName, tblName); err != nil {
			return nil, fmt.Errorf("analyze table %s: %w", tblName, err)
		}
	}

	return &types.Result{
		Message: fmt.Sprintf("ANALYZE completed successfully for %d table(s)", len(tablesToAnalyze)),
	}, nil
}

type valFreq struct {
	val  interface{}
	freq int
}

func analyzeTable(ctx *types.ExecutionContext, dbName, tblName string) error {
	schema, err := ctx.Storage.GetTableSchema(dbName, tblName)
	if err != nil {
		return err
	}

	rows, err := ctx.Storage.ReadCurrentRows(dbName, tblName)
	if err != nil {
		return err
	}

	rowCount := len(rows)
	if rowCount == 0 {
		return nil // No stats to gather if table is empty
	}

	for i, col := range schema.Columns {
		nullCount := 0
		freqMap := make(map[string]valFreq)

		var sortedVals []interface{}

		for _, row := range rows {
			if i >= len(row) || row[i] == nil {
				nullCount++
				continue
			}
			val := row[i]
			strVal := types.ValueToString(val)
			if f, ok := freqMap[strVal]; ok {
				f.freq++
				freqMap[strVal] = f
			} else {
				freqMap[strVal] = valFreq{val: val, freq: 1}
				sortedVals = append(sortedVals, val)
			}
		}

		distinctValues := len(freqMap)
		nullFraction := float64(nullCount) / float64(rowCount)

		// Calculate MCV
		var freqs []valFreq
		for _, f := range freqMap {
			freqs = append(freqs, f)
		}
		sort.Slice(freqs, func(a, b int) bool {
			return freqs[a].freq > freqs[b].freq
		})

		mcvCount := 10 // Top 10 most common values
		if mcvCount > len(freqs) {
			mcvCount = len(freqs)
		}
		var mcv []interface{}
		for j := 0; j < mcvCount; j++ {
			// Only consider values that appear more than once or if table is tiny
			if freqs[j].freq > 1 || rowCount < 10 {
				mcv = append(mcv, freqs[j].val)
			}
		}

		// Calculate Histogram (Equi-depth)
		// We need to sort sortedVals
		sort.Slice(sortedVals, func(a, b int) bool {
			return eval.CompareOrdering(sortedVals[a], sortedVals[b]) < 0
		})

		histCount := 10 // Up to 10 buckets
		var histogram []interface{}
		if len(sortedVals) > 0 {
			step := float64(len(sortedVals)) / float64(histCount)
			for j := 0; j < histCount; j++ {
				idx := int(math.Floor(float64(j) * step))
				if idx >= len(sortedVals) {
					idx = len(sortedVals) - 1
				}
				histogram = append(histogram, sortedVals[idx])
			}
			histogram = append(histogram, sortedVals[len(sortedVals)-1])
		}

		stats := &optimizer.ColumnStats{
			TableName:      tblName,
			ColumnName:     col.Name,
			TableRowCount:  int64(rowCount),
			NullFraction:   nullFraction,
			DistinctValues: int64(distinctValues),
			MCV:            mcv,
			Histogram:      histogram,
		}

		optimizer.GlobalStatsRegistry.Set(dbName, tblName, col.Name, stats)
	}

	return nil
}
