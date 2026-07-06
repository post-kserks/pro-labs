package executor

import (
	"fmt"
	"strconv"
	"strings"

	"vaultdb/internal/parser"
	"vaultdb/internal/storage"
)

func evalExpr(expr parser.Expression, row storage.Row, schema *storage.TableSchema, ctx *ExecutionContext) (bool, error) {
	if expr == nil {
		return true, nil
	}
	val, err := evalOperand(expr, row, schema, ctx)
	if err != nil {
		return false, err
	}
	if b, ok := val.(bool); ok {
		return b, nil
	}
	return false, fmt.Errorf("expression must return boolean, got %T", val)
}

func evalBinary(expr *parser.BinaryExpr, row storage.Row, schema *storage.TableSchema, ctx *ExecutionContext) (interface{}, error) {
	left, err := evalOperand(expr.Left, row, schema, ctx)
	if err != nil {
		return nil, err
	}
	right, err := evalOperand(expr.Right, row, schema, ctx)
	if err != nil {
		return nil, err
	}

	switch expr.Operator {
	case "=", "!=", "<", ">", "<=", ">=":
		return compareValues(left, right, expr.Operator)
	case "+", "-", "*", "/":
		return evalArithmetic(left, right, expr.Operator)
	case "LIKE":
		return evalLike(left, right)
	case "ILIKE":
		return evalILike(left, right)
	case "IS":
		return left == nil, nil
	case "IS NOT":
		return left != nil, nil
	case "SEMANTIC_MATCH":
		return evalSemanticMatch(left, right, ctx)
	case "FTS_MATCH":
		return evalFtsMatch(left, right)
	case "@>":
		return evalJsonContains(left, right)
	case "<@":
		return evalJsonContainedBy(left, right)
	case "?":
		return evalJsonHasKey(left, right)
	case "||":
		return evalJsonMerge(left, right)
	case "@@":
		return evalFtsMatch(left, right)
	default:
		return nil, fmt.Errorf("unsupported operator '%s'", expr.Operator)
	}
}

func evalOperand(expr parser.Expression, row storage.Row, schema *storage.TableSchema, ctx *ExecutionContext) (interface{}, error) {
	if expr == nil {
		return nil, nil
	}
	switch e := expr.(type) {
	case parser.Value:
		return parserValueToRaw(e), nil
	case *parser.Value:
		return parserValueToRaw(*e), nil
	case *parser.ColumnRef:
		var colIdx map[string]int
		if ctx != nil {
			colIdx = ctx.ColumnIndex
		}
		if e.Table == "old" && ctx != nil && ctx.OldRow != nil {
			return resolveColumn(ctx.OldRow, schema, e.Name, colIdx)
		}
		if e.Table == "new" && ctx != nil && ctx.NewRow != nil {
			return resolveColumn(ctx.NewRow, schema, e.Name, colIdx)
		}
		if e.Table != "" {
			if val, err := resolveColumn(row, schema, e.Table+"."+e.Name, colIdx); err == nil {
				return val, nil
			}
		}
		return resolveColumn(row, schema, e.Name, colIdx)
	case *parser.BinaryExpr:
		return evalBinary(e, row, schema, ctx)
	case *parser.AndExpr:
		left, err := evalExpr(e.Left, row, schema, ctx)
		if err != nil {
			return nil, err
		}
		right, err := evalExpr(e.Right, row, schema, ctx)
		if err != nil {
			return nil, err
		}
		return left && right, nil
	case *parser.OrExpr:
		left, err := evalExpr(e.Left, row, schema, ctx)
		if err != nil {
			return nil, err
		}
		right, err := evalExpr(e.Right, row, schema, ctx)
		if err != nil {
			return nil, err
		}
		return left || right, nil
	case *parser.NotExpr:
		val, err := evalExpr(e.Expr, row, schema, ctx)
		if err != nil {
			return nil, err
		}
		return !val, nil
	case *parser.FunctionCall:
		return evalFunctionCall(e, row, schema, ctx)
	case *parser.AggregateExpr:
		// In SELECT context with aggregates, this is reached when the aggregate
		// is nested inside an expression (e.g., AVG(x) + 1). The top-level
		// aggregate executor handles standalone AggregateExpr nodes.
		// Return nil to avoid breaking arithmetic expressions.
		return nil, nil
	case *parser.CastExpr:
		return evalCast(e, row, schema, ctx)
	case *parser.CaseExpr:
		return evalCase(e, row, schema, ctx)
	case *parser.JsonPathExpr:
		return evalJsonPath(e, row, schema, ctx)
	case *parser.JSONAccess:
		return evalJSONAccess(e, row, schema, ctx)
	case *parser.BetweenExpr:
		return evalBetweenExpr(e, row, schema, ctx)
	case *parser.ExistsExpr:
		return evalExistsExpr(e, row, schema, ctx)
	case *parser.ComparisonSubqueryExpr:
		return evalComparisonSubquery(e, row, schema, ctx)
	case *parser.InExpr:
		return evalInExpr(e, row, schema, ctx)
	case *parser.SubqueryExpr:
		return executeSubquery(e, row, schema, ctx)
	case *parser.WindowFunctionExpr:
		// Resolve window function to the pre-computed column value
		if ctx != nil && ctx.WindowCols != nil {
			if colName, ok := ctx.WindowCols[e]; ok {
				return resolveColumn(row, schema, colName, ctx.ColumnIndex)
			}
		}
		return nil, nil
	default:
		return nil, fmt.Errorf("unsupported expression type: %T", expr)
	}
}

func evalInExpr(e *parser.InExpr, row storage.Row, schema *storage.TableSchema, ctx *ExecutionContext) (interface{}, error) {
	leftVal, err := evalOperand(e.Left, row, schema, ctx)
	if err != nil {
		return false, err
	}

	for _, right := range e.Right {
		// Handle subquery: execute it and compare against all results
		if sub, ok := right.(*parser.SubqueryExpr); ok {
			var cmdStmt parser.Statement
			if sel, ok := sub.Query.(*parser.SelectStatement); ok {
				subCopy := *sel
				if row != nil && schema != nil && subCopy.Where != nil {
					subCopy.Where = injectOuterColumns(subCopy.Where, row, schema)
				}
				cmdStmt = &subCopy
			} else {
				cmdStmt = sub.Query
			}
			cmd, err := CommandFactory(cmdStmt)
			if err != nil {
				return false, err
			}
			res, err := cmd.Execute(ctx)
			if err != nil {
				return false, err
			}
			for _, r := range res.Rows {
				if len(r) == 0 {
					continue
				}
				rightVal := r[0]
				// Try numeric conversion if types don't match
				if lf, lok := toFloat(leftVal); lok {
					if rf, rok := toFloat(rightVal); rok {
						cmp, _ := compareOrdered(lf, rf, "=")
						if cmp {
							if e.Not {
								return false, nil
							}
							return true, nil
						}
						continue
					}
				}
				cmp, err := compareValues(leftVal, rightVal, "=")
				if err != nil {
					continue
				}
				if cmp {
					if e.Not {
						return false, nil
					}
					return true, nil
				}
			}
			if e.Not {
				return true, nil
			}
			return false, nil
		}

		rightVal, err := evalOperand(right, row, schema, ctx)
		if err != nil {
			return false, err
		}
		cmp, err := compareValues(leftVal, rightVal, "=")
		if err != nil {
			return false, err
		}
		if cmp {
			if e.Not {
				return false, nil
			}
			return true, nil
		}
	}

	if e.Not {
		return true, nil
	}
	return false, nil
}

func evalCast(e *parser.CastExpr, row storage.Row, schema *storage.TableSchema, ctx *ExecutionContext) (interface{}, error) {
	val, err := evalOperand(e.Expr, row, schema, ctx)
	if err != nil {
		return nil, err
	}
	if val == nil {
		return nil, nil
	}

	switch strings.ToUpper(e.TargetType) {
	case "INT":
		if i, ok := toInt64(val); ok {
			return i, nil
		}
		if f, ok := toFloat(val); ok {
			return int64(f), nil
		}
		if s, ok := val.(string); ok {
			if i, err := strconv.ParseInt(s, 10, 64); err == nil {
				return i, nil
			}
		}
	case "FLOAT":
		if f, ok := toFloat(val); ok {
			return f, nil
		}
		if s, ok := val.(string); ok {
			if f, err := strconv.ParseFloat(s, 64); err == nil {
				return f, nil
			}
		}
	case "TEXT", "VARCHAR":
		return valueToString(val), nil
	case "BOOL":
		if b, ok := val.(bool); ok {
			return b, nil
		}
		s := strings.ToUpper(valueToString(val))
		if s == "TRUE" || s == "1" {
			return true, nil
		}
		if s == "FALSE" || s == "0" {
			return false, nil
		}
	}

	return nil, fmt.Errorf("cannot cast %T to %s", val, e.TargetType)
}

func evalCase(e *parser.CaseExpr, row storage.Row, schema *storage.TableSchema, ctx *ExecutionContext) (interface{}, error) {
	var baseVal interface{}
	var err error
	if e.Base != nil {
		baseVal, err = evalOperand(e.Base, row, schema, ctx)
		if err != nil {
			return nil, err
		}
	}

	for _, when := range e.Whens {
		if baseVal != nil {
			whenVal, err := evalOperand(when.Condition, row, schema, ctx)
			if err != nil {
				return nil, err
			}
			if CompareValues(baseVal, whenVal) == 0 {
				return evalOperand(when.Result, row, schema, ctx)
			}
		} else {
			match, err := evalExpr(when.Condition, row, schema, ctx)
			if err != nil {
				return nil, err
			}
			if match {
				return evalOperand(when.Result, row, schema, ctx)
			}
		}
	}

	if e.Else != nil {
		return evalOperand(e.Else, row, schema, ctx)
	}

	return nil, nil
}

func evalBetweenExpr(e *parser.BetweenExpr, row storage.Row, schema *storage.TableSchema, ctx *ExecutionContext) (bool, error) {
	val, err := evalOperand(e.Expr, row, schema, ctx)
	if err != nil {
		return false, err
	}
	lower, err := evalOperand(e.Lower, row, schema, ctx)
	if err != nil {
		return false, err
	}
	upper, err := evalOperand(e.Upper, row, schema, ctx)
	if err != nil {
		return false, err
	}

	cmpLower, err := compareValues(val, lower, ">=")
	if err != nil {
		return false, err
	}
	cmpUpper, err := compareValues(val, upper, "<=")
	if err != nil {
		return false, err
	}

	result := cmpLower && cmpUpper
	if e.Not {
		result = !result
	}
	return result, nil
}

func evalExistsExpr(e *parser.ExistsExpr, row storage.Row, schema *storage.TableSchema, ctx *ExecutionContext) (bool, error) {
	if e.Select == nil {
		return false, fmt.Errorf("EXISTS: subquery is nil")
	}

	var cmdStmt parser.Statement
	if sel, ok := e.Select.(*parser.SelectStatement); ok {
		subCopy := *sel
		if row != nil && schema != nil && subCopy.Where != nil && subCopy.TableName != schema.Name {
			subCopy.Where = injectOuterColumns(subCopy.Where, row, schema)
		}
		cmdStmt = &subCopy
	} else {
		cmdStmt = e.Select
	}

	cmd, err := CommandFactory(cmdStmt)
	if err != nil {
		return false, fmt.Errorf("EXISTS: %w", err)
	}

	res, err := cmd.Execute(ctx)
	if err != nil {
		return false, fmt.Errorf("EXISTS: %w", err)
	}

	exists := len(res.Rows) > 0
	if e.Not {
		exists = !exists
	}
	return exists, nil
}

func evalComparisonSubquery(e *parser.ComparisonSubqueryExpr, row storage.Row, schema *storage.TableSchema, ctx *ExecutionContext) (bool, error) {
	leftVal, err := evalOperand(e.Left, row, schema, ctx)
	if err != nil {
		return false, err
	}

	var cmdStmt parser.Statement
	if sel, ok := e.Subquery.(*parser.SelectStatement); ok {
		subCopy := *sel
		if row != nil && schema != nil && subCopy.Where != nil {
			subCopy.Where = injectOuterColumns(subCopy.Where, row, schema)
		}
		cmdStmt = &subCopy
	} else {
		cmdStmt = e.Subquery
	}

	cmd, err := CommandFactory(cmdStmt)
	if err != nil {
		return false, err
	}
	res, err := cmd.Execute(ctx)
	if err != nil {
		return false, err
	}

	values := make([]interface{}, 0, len(res.Rows))
	for _, r := range res.Rows {
		if len(r) > 0 {
			val, err := convertStringToValue(r[0], storage.ColumnSchema{Type: "TEXT"})
			if err == nil {
				values = append(values, val)
			} else {
				values = append(values, r[0])
			}
		}
	}

	switch e.Quantifier {
	case "ALL":
		for _, v := range values {
			cmp, err := compareValues(leftVal, v, e.Operator)
			if err != nil {
				return false, err
			}
			if !cmp {
				return false, nil
			}
		}
		return true, nil
	case "ANY", "SOME":
		for _, v := range values {
			cmp, err := compareValues(leftVal, v, e.Operator)
			if err != nil {
				return false, err
			}
			if cmp {
				return true, nil
			}
		}
		return false, nil
	}
	return false, fmt.Errorf("unknown quantifier: %s", e.Quantifier)
}

// evaluateCheckExpr evaluates a CHECK constraint expression against a row.
func evaluateCheckExpr(exprStr string, row storage.Row, schema *storage.TableSchema) (bool, error) {
	if exprStr == "" {
		return true, nil
	}
	expr, err := parser.ParseExpression(exprStr)
	if err != nil {
		return false, fmt.Errorf("failed to parse CHECK expression: %w", err)
	}
	return evalCheckExprAST(expr, row, schema)
}

// evalCheckExprAST evaluates a parsed CHECK expression against a row.
func evalCheckExprAST(expr parser.Expression, row storage.Row, schema *storage.TableSchema) (bool, error) {
	switch e := expr.(type) {
	case *parser.BinaryExpr:
		left, err := evalOperand(e.Left, row, schema, nil)
		if err != nil {
			return false, err
		}
		right, err := evalOperand(e.Right, row, schema, nil)
		if err != nil {
			return false, err
		}
		return compareValues(left, right, e.Operator)
	case *parser.AndExpr:
		left, err := evalCheckExprAST(e.Left, row, schema)
		if err != nil {
			return false, err
		}
		right, err := evalCheckExprAST(e.Right, row, schema)
		if err != nil {
			return false, err
		}
		return left && right, nil
	case *parser.OrExpr:
		left, err := evalCheckExprAST(e.Left, row, schema)
		if err != nil {
			return false, err
		}
		right, err := evalCheckExprAST(e.Right, row, schema)
		if err != nil {
			return false, err
		}
		return left || right, nil
	case *parser.NotExpr:
		val, err := evalCheckExprAST(e.Expr, row, schema)
		if err != nil {
			return false, err
		}
		return !val, nil
	case *parser.InExpr:
		leftVal, err := evalOperand(e.Left, row, schema, nil)
		if err != nil {
			return false, err
		}
		for _, right := range e.Right {
			rightVal, err := evalOperand(right, row, schema, nil)
			if err != nil {
				return false, err
			}
			cmp, err := compareValues(leftVal, rightVal, "=")
			if err != nil {
				continue
			}
			if cmp {
				if e.Not {
					return false, nil
				}
				return true, nil
			}
		}
		if e.Not {
			return true, nil
		}
		return false, nil
	case *parser.BetweenExpr:
		return evalBetweenExpr(e, row, schema, nil)
	case *parser.FunctionCall:
		val, err := evalFunctionCall(e, row, schema, nil)
		if err != nil {
			return false, err
		}
		if b, ok := val.(bool); ok {
			return b, nil
		}
		return false, fmt.Errorf("CHECK function must return bool, got %T", val)
	default:
		return false, fmt.Errorf("unsupported CHECK expression type: %T", expr)
	}
}
