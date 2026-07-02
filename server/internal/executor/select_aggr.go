package executor

import (
	"fmt"
	"sort"
	"strings"

	"vaultdb/internal/parser"
	"vaultdb/internal/storage"
)

// collectAggregates extracts all AggregateExpr nodes from column expressions.
func collectAggregates(columns []parser.SelectColumn) []*parser.AggregateExpr {
	var result []*parser.AggregateExpr
	for _, col := range columns {
		collectAggregatesFromExpr(col.Expr, &result)
	}
	return result
}

func collectAggregatesFromExpr(expr parser.Expression, result *[]*parser.AggregateExpr) {
	switch e := expr.(type) {
	case *parser.AggregateExpr:
		*result = append(*result, e)
	case *parser.BinaryExpr:
		collectAggregatesFromExpr(e.Left, result)
		collectAggregatesFromExpr(e.Right, result)
	case *parser.AndExpr:
		collectAggregatesFromExpr(e.Left, result)
		collectAggregatesFromExpr(e.Right, result)
	case *parser.OrExpr:
		collectAggregatesFromExpr(e.Left, result)
		collectAggregatesFromExpr(e.Right, result)
	case *parser.FunctionCall:
		for _, arg := range e.Args {
			collectAggregatesFromExpr(arg, result)
		}
	}
}

// resolveNestedAggregates replaces AggregateExpr nodes in an expression with their
// computed values from aggregators, enabling arithmetic like AVG(x) + 1.
func resolveNestedAggregates(expr parser.Expression, aggMap map[*parser.AggregateExpr]Aggregator, columns []parser.SelectColumn) (parser.Expression, error) {
	switch e := expr.(type) {
	case *parser.AggregateExpr:
		// Find matching aggregator by pointer identity
		if agg, exists := aggMap[e]; exists {
			res := agg.Result()
			return &parser.Value{Type: "int", IntVal: aggResultToInt64(res)}, nil
		}
		return expr, nil
	case *parser.BinaryExpr:
		left, err := resolveNestedAggregates(e.Left, aggMap, columns)
		if err != nil {
			return nil, err
		}
		right, err := resolveNestedAggregates(e.Right, aggMap, columns)
		if err != nil {
			return nil, err
		}
		return &parser.BinaryExpr{Left: left, Operator: e.Operator, Right: right}, nil
	case *parser.AndExpr:
		left, err := resolveNestedAggregates(e.Left, aggMap, columns)
		if err != nil {
			return nil, err
		}
		right, err := resolveNestedAggregates(e.Right, aggMap, columns)
		if err != nil {
			return nil, err
		}
		return &parser.AndExpr{Left: left, Right: right}, nil
	case *parser.OrExpr:
		left, err := resolveNestedAggregates(e.Left, aggMap, columns)
		if err != nil {
			return nil, err
		}
		right, err := resolveNestedAggregates(e.Right, aggMap, columns)
		if err != nil {
			return nil, err
		}
		return &parser.OrExpr{Left: left, Right: right}, nil
	case *parser.FunctionCall:
		args := make([]parser.Expression, len(e.Args))
		for i, a := range e.Args {
			resolved, err := resolveNestedAggregates(a, aggMap, columns)
			if err != nil {
				return nil, err
			}
			args[i] = resolved
		}
		return &parser.FunctionCall{Name: e.Name, Args: args}, nil
	default:
		return expr, nil
	}
}

// resolveAggregatesInExpr replaces AggregateExpr nodes in HAVING clause
// with their computed values from the aggregators.
func resolveAggregatesInExpr(expr parser.Expression, columns []parser.SelectColumn, aggregators []Aggregator) parser.Expression {
	switch e := expr.(type) {
	case *parser.BinaryExpr:
		return &parser.BinaryExpr{
			Left:     resolveAggregatesInExpr(e.Left, columns, aggregators),
			Operator: e.Operator,
			Right:    resolveAggregatesInExpr(e.Right, columns, aggregators),
		}
	case *parser.AggregateExpr:
		// Find matching column by aggregate name and args
		for i, col := range columns {
			if aggExpr, ok := col.Expr.(*parser.AggregateExpr); ok {
				if strings.EqualFold(aggExpr.Name, e.Name) && aggregators[i] != nil {
					result := aggregators[i].Result()
					return &parser.Value{Type: "int", IntVal: aggResultToInt64(result)}
				}
			}
		}
		return e
	default:
		return expr
	}
}

func aggResultToInt64(v interface{}) int64 {
	switch val := v.(type) {
	case int64:
		return val
	case int:
		return int64(val)
	case float64:
		return int64(val)
	default:
		return 0
	}
}

func (c *SelectCommand) hasAggregates() bool {
	for _, col := range c.stmt.Columns {
		if c.containsAggregate(col.Expr) {
			return true
		}
	}
	return c.containsAggregate(c.stmt.Having)
}

func (c *SelectCommand) containsAggregate(expr parser.Expression) bool {
	if expr == nil {
		return false
	}
	switch e := expr.(type) {
	case *parser.AggregateExpr:
		return true
	case *parser.BinaryExpr:
		return c.containsAggregate(e.Left) || c.containsAggregate(e.Right)
	case *parser.AndExpr:
		return c.containsAggregate(e.Left) || c.containsAggregate(e.Right)
	case *parser.OrExpr:
		return c.containsAggregate(e.Left) || c.containsAggregate(e.Right)
	case *parser.NotExpr:
		return c.containsAggregate(e.Expr)
	case *parser.FunctionCall:
		for _, arg := range e.Args {
			if c.containsAggregate(arg) {
				return true
			}
		}
	}
	return false
}
func (c *SelectCommand) executeWithGrouping(rows []storage.Row, schema *storage.TableSchema, asOfNote string, ctx *ExecutionContext) (*Result, error) {
	groups := make(map[string][]storage.Row)
	groupOrder := make([]string, 0)

	for _, row := range rows {
		keyParts := make([]string, len(c.stmt.GroupBy))
		for i, expr := range c.stmt.GroupBy {
			val, err := evalOperand(expr, row, schema, ctx)
			if err != nil {
				return nil, fmt.Errorf("eval group by key: %w", err)
			}
			keyParts[i] = valueToString(val)
		}
		key := strings.Join(keyParts, "\x00")
		if _, ok := groups[key]; !ok {
			groupOrder = append(groupOrder, key)
		}
		groups[key] = append(groups[key], row)
	}

	// If no GROUP BY but has aggregates, treat everything as one group
	if len(c.stmt.GroupBy) == 0 && len(groupOrder) == 0 && c.hasAggregates() {
		groupOrder = append(groupOrder, "")
		groups[""] = rows
	}

	projectColumns := make([]string, len(c.stmt.Columns))
	for i, col := range c.stmt.Columns {
		if col.Alias != "" {
			projectColumns[i] = col.Alias
		} else if colRef, ok := col.Expr.(*parser.ColumnRef); ok {
			projectColumns[i] = colRef.Name
		} else {
			projectColumns[i] = fmt.Sprintf("col%d", i)
		}
	}

	resultRows := make([][]string, 0)
	for _, key := range groupOrder {
		groupRows := groups[key]

		// Collect all AggregateExpr nodes from all columns (top-level and nested)
		allAggExprs := collectAggregates(c.stmt.Columns)

		// Create aggregators for each unique aggregate
		aggMap := make(map[*parser.AggregateExpr]Aggregator)
		for _, aggExpr := range allAggExprs {
			if _, exists := aggMap[aggExpr]; !exists {
				aggArgs := make([]interface{}, len(aggExpr.Args))
				if len(groupRows) > 0 {
					for j, argExpr := range aggExpr.Args {
						argVal, err := evalOperand(argExpr, groupRows[0], schema, ctx)
						if err != nil {
							argVal = nil
						}
						aggArgs[j] = argVal
					}
				}
				aggMap[aggExpr] = NewAggregator(aggExpr.Name, aggExpr.Distinct, aggArgs...)
			}
		}

		// Map column index to aggregator (for top-level aggregates)
		aggregators := make([]Aggregator, len(c.stmt.Columns))
		for i, col := range c.stmt.Columns {
			if aggExpr, ok := col.Expr.(*parser.AggregateExpr); ok {
				aggregators[i] = aggMap[aggExpr]
			}
		}

		// Process all rows in group — feed all aggregators
		for _, row := range groupRows {
			for aggExpr, agg := range aggMap {
				var key, val interface{}
				if strings.EqualFold(aggExpr.Name, "JSON_OBJECT_AGG") && len(aggExpr.Args) >= 2 {
					var err error
					key, err = evalOperand(aggExpr.Args[0], row, schema, ctx)
					if err != nil {
						return nil, fmt.Errorf("eval JSON_OBJECT_AGG key: %w", err)
					}
					val, err = evalOperand(aggExpr.Args[1], row, schema, ctx)
					if err != nil {
						return nil, fmt.Errorf("eval JSON_OBJECT_AGG value: %w", err)
					}
				} else if len(aggExpr.Args) > 0 {
					if colRef, ok := aggExpr.Args[0].(*parser.ColumnRef); ok && colRef.Name == "*" {
						val = int64(1)
					} else {
						var err error
						val, err = evalOperand(aggExpr.Args[0], row, schema, ctx)
						if err != nil {
							return nil, fmt.Errorf("eval aggregate argument: %w", err)
						}
					}
				} else {
					val = int64(1)
				}
				agg.Add(key, val)
			}
		}

		// Calculate result for this group
		resultRow := make([]string, len(c.stmt.Columns))
		// We need a virtual row for HAVING evaluation if it uses aggregates
		virtualRow := make(storage.Row, len(c.stmt.Columns))

		for i, col := range c.stmt.Columns {
			if aggregators[i] != nil {
				res := aggregators[i].Result()
				resultRow[i] = valueToString(res)
				virtualRow[i] = res
			} else if c.containsAggregate(col.Expr) {
				// Expression contains aggregates nested in arithmetic (e.g., AVG(x) + 1)
				resolved, err := resolveNestedAggregates(col.Expr, aggMap, c.stmt.Columns)
				if err != nil {
					return nil, fmt.Errorf("eval column expression: %w", err)
				}
				val, err := evalOperand(resolved, nil, schema, ctx)
				if err != nil {
					val = nil
				}
				resultRow[i] = valueToString(val)
				virtualRow[i] = val
			} else {
				// Pick from first row of group for non-aggregates
				if len(groupRows) > 0 {
					val, err := evalOperand(col.Expr, groupRows[0], schema, ctx)
					if err != nil {
						return nil, fmt.Errorf("eval column expression: %w", err)
					}
					resultRow[i] = valueToString(val)
					virtualRow[i] = val
				} else {
					resultRow[i] = "NULL"
					virtualRow[i] = nil
				}
			}
		}

		// Handle HAVING
		if c.stmt.Having != nil {
			// Build a temporary schema for the projected results
			projectedSchema := &storage.TableSchema{
				Columns: make([]storage.ColumnSchema, len(c.stmt.Columns)),
			}
			for i, name := range projectColumns {
				projectedSchema.Columns[i] = storage.ColumnSchema{Name: name}
			}

			// Resolve aggregate expressions in HAVING to their computed values
			havingExpr := resolveAggregatesInExpr(c.stmt.Having, c.stmt.Columns, aggregators)

			// Evaluate HAVING on the projected (aggregated) result row
			ok, err := evalExpr(havingExpr, virtualRow, projectedSchema, ctx)
			if err != nil {
				// Fallback to original row if HAVING uses non-aggregates
				ok, err = evalExpr(havingExpr, groupRows[0], schema, ctx)
				if err != nil {
					continue
				}
			}
			if !ok {
				continue
			}
		}

		resultRows = append(resultRows, resultRow)
	}

	resultRows = c.orderAndPageGrouped(resultRows, projectColumns)

	return &Result{
		Type:     "rows",
		Columns:  projectColumns,
		Rows:     resultRows,
		AsOfNote: asOfNote,
	}, nil
}

// orderAndPageGrouped applies ORDER BY / OFFSET / LIMIT to grouped output.
// Sort keys are resolved against the projected columns: by alias or column
// name, or by 1-based position (ORDER BY 2).
func (c *SelectCommand) orderAndPageGrouped(rows [][]string, projectColumns []string) [][]string {
	if len(c.stmt.OrderBy) > 0 {
		colIndexByName := make(map[string]int, len(projectColumns))
		for i, name := range projectColumns {
			colIndexByName[strings.ToLower(name)] = i
		}

		type sortKey struct {
			idx  int
			desc bool
		}
		keys := make([]sortKey, 0, len(c.stmt.OrderBy))
		for _, item := range c.stmt.OrderBy {
			idx := -1
			switch expr := item.Expr.(type) {
			case *parser.ColumnRef:
				if i, ok := colIndexByName[strings.ToLower(expr.Name)]; ok {
					idx = i
				}
			case parser.Value:
				if expr.Type == "int" && expr.IntVal >= 1 && int(expr.IntVal) <= len(projectColumns) {
					idx = int(expr.IntVal) - 1
				}
			case *parser.Value:
				if expr.Type == "int" && expr.IntVal >= 1 && int(expr.IntVal) <= len(projectColumns) {
					idx = int(expr.IntVal) - 1
				}
			}
			if idx >= 0 {
				keys = append(keys, sortKey{idx: idx, desc: item.Direction == "DESC"})
			}
		}

		if len(keys) > 0 {
			sort.SliceStable(rows, func(i, j int) bool {
				for _, k := range keys {
					cmp := compareResultCells(rows[i][k.idx], rows[j][k.idx])
					if cmp == 0 {
						continue
					}
					if k.desc {
						return cmp > 0
					}
					return cmp < 0
				}
				return false
			})
		}
	}

	start := 0
	limit, hasLimit, offset, hasOffset := c.resolveLimitOffset(nil)
	if hasOffset {
		start = offset
		if start > len(rows) {
			start = len(rows)
		}
	}
	end := len(rows)
	if hasLimit {
		end = start + limit
		if end > len(rows) {
			end = len(rows)
		}
	}
	return rows[start:end]
}

// compareResultCells compares rendered cells numerically when both parse as
// numbers, lexically otherwise.
