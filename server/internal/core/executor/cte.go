package executor

import (
	"fmt"
	"strings"
	"vaultdb/internal/core/parser"
	"vaultdb/internal/core/storage"
)

const maxCTEIterations = 100

// CTEScope — CTE scope for a specific query.
type CTEScope struct {
	ctes   map[string]*CTEDefinition
	parent *CTEScope
}

// CTEDefinition — CTE definition.
type CTEDefinition struct {
	Name    string
	Columns []string
	Query   parser.Statement
	Result  *Result // cached result
}

// NewCTEScope creates a new CTE scope.
func NewCTEScope() *CTEScope {
	return &CTEScope{
		ctes: make(map[string]*CTEDefinition),
	}
}

// PushScope adds a nested scope.
func (s *CTEScope) PushScope() *CTEScope {
	return &CTEScope{
		ctes:   make(map[string]*CTEDefinition),
		parent: s,
	}
}

// RegisterCTE registers a CTE in the current scope.
func (s *CTEScope) RegisterCTE(cte *CTEDefinition) {
	s.ctes[cte.Name] = cte
}

// ResolveCTE looks up a CTE by name in the scope chain.
func (s *CTEScope) ResolveCTE(name string) (*CTEDefinition, bool) {
	if cte, ok := s.ctes[name]; ok {
		return cte, true
	}
	if s.parent != nil {
		return s.parent.ResolveCTE(name)
	}
	return nil, false
}

// ExecuteCTE executes a CTE and caches the result.
func (s *CTEScope) ExecuteCTE(cte *CTEDefinition, ctx *ExecutionContext) (*Result, error) {
	if cte.Result != nil {
		return cte.Result, nil
	}

	res, err := ctx.RunSubquery.RunSubquery(ctx, cte.Query)
	if err != nil {
		return nil, fmt.Errorf("CTE '%s': %w", cte.Name, err)
	}

	cte.Result = res
	return res, nil
}

// ExecuteCTEStatement executes a CTEStatement.
func ExecuteCTEStatement(stmt *parser.CTEStatement, ctx *ExecutionContext) (*Result, error) {
	scope := NewCTEScope()

	for i := range stmt.CTEs {
		scope.RegisterCTE(&CTEDefinition{
			Name:    stmt.CTEs[i].Name,
			Columns: stmt.CTEs[i].Columns,
			Query:   stmt.CTEs[i].Query,
		})
	}

	dbName, _ := requireCurrentDB(ctx)

	if stmt.Recursive {
		for i := range stmt.CTEs {
			cte := &stmt.CTEs[i]
			res, err := executeRecursiveCTE(cte, scope, ctx)
			if err != nil {
				return nil, fmt.Errorf("RECURSIVE CTE '%s': %w", cte.Name, err)
			}
			scope.RegisterCTE(&CTEDefinition{
				Name:    cte.Name,
				Columns: cte.Columns,
				Query:   cte.Query,
				Result:  res,
			})
		}
	} else {
		// Execute non-recursive CTEs in order so cascading references work.
		for i := range stmt.CTEs {
			cte, _ := scope.ResolveCTE(stmt.CTEs[i].Name)
			res, err := scope.ExecuteCTE(cte, ctx)
			if err != nil {
				return nil, fmt.Errorf("CTE '%s': %w", cte.Name, err)
			}
			// Materialize result as temp table so later CTEs can SELECT from it.
			tempTable := cte.Name
			_ = ctx.Storage.DropTable(dbName, tempTable)
			schema := storage.TableSchema{
				Name:     tempTable,
				Database: dbName,
				Columns:  make([]storage.ColumnSchema, len(res.Columns)),
			}
			for j, col := range res.Columns {
				schema.Columns[j] = storage.ColumnSchema{Name: col, Type: "TEXT"}
			}
			if err := ctx.Storage.CreateTable(dbName, schema); err != nil {
				return nil, fmt.Errorf("CTE '%s' temp table: %w", cte.Name, err)
			}
			rows := make([]storage.Row, len(res.Rows))
			for j, r := range res.Rows {
				row := make(storage.Row, len(r))
				for k, v := range r {
					row[k] = v
				}
				rows[j] = row
			}
			if _, err := ctx.Storage.InsertRows(dbName, tempTable, rows); err != nil {
				return nil, fmt.Errorf("CTE '%s' temp insert: %w", cte.Name, err)
			}
		}
		// Cleanup temp tables after body executes.
		defer func() {
			for i := range stmt.CTEs {
				_ = ctx.Storage.DropTable(dbName, stmt.CTEs[i].Name)
			}
		}()
	}

	if selectStmt, ok := stmt.Body.(*parser.SelectStatement); ok {
		if selectStmt.TableName != "" {
			if _, found := scope.ResolveCTE(selectStmt.TableName); found {
				// Check if outer SELECT has aggregation/GROUP BY/HAVING
				hasAggregation := false
				for _, col := range selectStmt.Columns {
					if _, ok := col.Expr.(*parser.AggregateExpr); ok {
						hasAggregation = true
						break
					}
				}
				if !hasAggregation && selectStmt.Having == nil &&
					len(selectStmt.GroupBy) == 0 && len(selectStmt.OrderBy) == 0 &&
					!selectStmt.HasLimit && !selectStmt.HasOffset && selectStmt.Where == nil {
					// Simple CTE reference — return result directly
					cte, _ := scope.ResolveCTE(selectStmt.TableName)
					res, err := scope.ExecuteCTE(cte, ctx)
					if err != nil {
						return nil, err
					}
					return res, nil
				}

				// Has aggregation/clauses — use temp table approach
				cte, _ := scope.ResolveCTE(selectStmt.TableName)
				cteRes, err := scope.ExecuteCTE(cte, ctx)
				if err != nil {
					return nil, err
				}

				tempTable := "_cte_" + selectStmt.TableName
				dbName, _ := requireCurrentDB(ctx)
				schema := storage.TableSchema{
					Name:     tempTable,
					Database: dbName,
					Columns:  make([]storage.ColumnSchema, len(cteRes.Columns)),
				}
				for i, col := range cteRes.Columns {
					schema.Columns[i] = storage.ColumnSchema{Name: col, Type: "TEXT"}
				}
				if err := ctx.Storage.CreateTable(dbName, schema); err != nil {
					return nil, fmt.Errorf("CTE temp table: %w", err)
				}
				defer ctx.Storage.DropTable(dbName, tempTable)

				rows := make([]storage.Row, len(cteRes.Rows))
				for i, r := range cteRes.Rows {
					row := make(storage.Row, len(r))
					for j, v := range r {
						row[j] = v
					}
					rows[i] = row
				}
				if _, err := ctx.Storage.InsertRows(dbName, tempTable, rows); err != nil {
					return nil, fmt.Errorf("CTE temp insert: %w", err)
				}

				selectStmt.TableName = tempTable
				cmd, err := ctx.CreateCommand(selectStmt)
				if err != nil {
					return nil, err
				}
				return cmd.Execute(ctx)
			}
		}
		cmd, err := ctx.CreateCommand(selectStmt)
		if err != nil {
			return nil, err
		}
		return cmd.Execute(ctx)
	}

	res, err := ctx.RunSubquery.RunSubquery(ctx, stmt.Body)
	if err != nil {
		return nil, err
	}
	return res, nil
}

func executeRecursiveCTE(cte *parser.CTEDefinition, scope *CTEScope, ctx *ExecutionContext) (*Result, error) {
	setOp, ok := cte.Query.(*parser.SetOperationStatement)
	if !ok {
		return nil, fmt.Errorf("recursive CTE query must be a UNION ALL")
	}
	if setOp.Op != "UNION ALL" {
		return nil, fmt.Errorf("recursive CTE only supports UNION ALL, got %s", setOp.Op)
	}

	anchorRes, err := ctx.RunSubquery.RunSubquery(ctx, setOp.Left)
	if err != nil {
		return nil, fmt.Errorf("recursive CTE anchor: %w", err)
	}

	columns := cte.Columns
	if len(columns) == 0 && anchorRes != nil {
		columns = anchorRes.Columns
	}

	allRows := make([][]string, len(anchorRes.Rows))
	copy(allRows, anchorRes.Rows)

	visited := make(map[string]bool)
	for _, row := range allRows {
		visited[rowKeyStr(row)] = true
	}

	dbName, err := requireCurrentDB(ctx)
	if err != nil {
		return nil, err
	}

	tmpTable := cte.Name
	for iter := 0; iter < maxCTEIterations; iter++ {
		prevCount := len(allRows)

		schema := storage.TableSchema{
			Name:     tmpTable,
			Database: dbName,
			Columns:  make([]storage.ColumnSchema, len(columns)),
		}
		for i, col := range columns {
			schema.Columns[i] = storage.ColumnSchema{Name: col, Type: "TEXT"}
		}
		_ = ctx.Storage.DropTable(dbName, tmpTable)
		if err := ctx.Storage.CreateTable(dbName, schema); err != nil {
			return nil, fmt.Errorf("recursive CTE temp table: %w", err)
		}

		rows := make([]storage.Row, len(allRows))
		for i, r := range allRows {
			row := make(storage.Row, len(r))
			for j, v := range r {
				row[j] = v
			}
			rows[i] = row
		}
		if _, err := ctx.Storage.InsertRows(dbName, tmpTable, rows); err != nil {
			return nil, fmt.Errorf("recursive CTE temp insert: %w", err)
		}

		// Invalidate result cache so the recursive member re-reads from storage.
		if ctx.Session != nil && asSession(ctx).resultCache != nil {
			func() {
				if rc := ctx.Session.GetResultCache(); rc != nil {
					rc.(*ResultCache).Invalidate(tmpTable)
				}
			}()
		}

		iterRes, err := ctx.RunSubquery.RunSubquery(ctx, setOp.Right)
		if err != nil {
			return nil, fmt.Errorf("recursive CTE recursive member: %w", err)
		}

		newRows := 0
		for _, row := range iterRes.Rows {
			key := rowKeyStr(row)
			if !visited[key] {
				visited[key] = true
				allRows = append(allRows, row)
				newRows++
			}
		}

		if newRows == 0 || len(allRows) == prevCount {
			break
		}
	}

	_ = ctx.Storage.DropTable(dbName, tmpTable)

	return &Result{
		Type:    "rows",
		Columns: columns,
		Rows:    allRows,
	}, nil
}

func rowKeyStr(row []string) string {
	parts := make([]string, len(row))
	copy(parts, row)
	return strings.Join(parts, "\x00")
}

// ExecuteSelectWithCTE executes SELECT with CTE.
func ExecuteSelectWithCTE(stmt *parser.SelectStatement, ctx *ExecutionContext) (*Result, error) {
	if len(stmt.CTEs) == 0 {
		// No CTE — execute regular SELECT
		cmd, err := ctx.CreateCommand(stmt)
		if err != nil {
			return nil, err
		}
		return cmd.Execute(ctx)
	}

	scope := NewCTEScope()

	dbName, _ := requireCurrentDB(ctx)

	// Register CTE
	for i := range stmt.CTEs {
		scope.RegisterCTE(&CTEDefinition{
			Name:    stmt.CTEs[i].Name,
			Columns: stmt.CTEs[i].Columns,
			Query:   stmt.CTEs[i].Query,
		})
	}

	// Execute CTEs in order so cascading references resolve via temp tables.
	for i := range stmt.CTEs {
		cte, _ := scope.ResolveCTE(stmt.CTEs[i].Name)
		res, err := scope.ExecuteCTE(cte, ctx)
		if err != nil {
			return nil, fmt.Errorf("CTE '%s': %w", cte.Name, err)
		}
		tempTable := cte.Name
		_ = ctx.Storage.DropTable(dbName, tempTable)
		schema := storage.TableSchema{
			Name:     tempTable,
			Database: dbName,
			Columns:  make([]storage.ColumnSchema, len(res.Columns)),
		}
		for j, col := range res.Columns {
			schema.Columns[j] = storage.ColumnSchema{Name: col, Type: "TEXT"}
		}
		if err := ctx.Storage.CreateTable(dbName, schema); err != nil {
			return nil, fmt.Errorf("CTE '%s' temp table: %w", cte.Name, err)
		}
		rows := make([]storage.Row, len(res.Rows))
		for j, r := range res.Rows {
			row := make(storage.Row, len(r))
			for k, v := range r {
				row[k] = v
			}
			rows[j] = row
		}
		if _, err := ctx.Storage.InsertRows(dbName, tempTable, rows); err != nil {
			return nil, fmt.Errorf("CTE '%s' temp insert: %w", cte.Name, err)
		}
	}
	defer func() {
		for i := range stmt.CTEs {
			_ = ctx.Storage.DropTable(dbName, stmt.CTEs[i].Name)
		}
	}()

	// Check if SELECT references a CTE
	if stmt.TableName != "" {
		if _, found := scope.ResolveCTE(stmt.TableName); found {
			// Check if outer SELECT has aggregation, GROUP BY, HAVING, or other clauses
			// that need to be applied to the CTE result
			hasAggregation := false
			for _, col := range stmt.Columns {
				if _, ok := col.Expr.(*parser.AggregateExpr); ok {
					hasAggregation = true
					break
				}
			}
			hasAdditionalClauses := hasAggregation || stmt.Having != nil ||
				len(stmt.GroupBy) > 0 || len(stmt.OrderBy) > 0 || stmt.HasLimit || stmt.HasOffset ||
				stmt.Where != nil

			if !hasAdditionalClauses {
				cte, _ := scope.ResolveCTE(stmt.TableName)
				res, err := scope.ExecuteCTE(cte, ctx)
				if err != nil {
					return nil, err
				}
				return res, nil
			}

			// Execute CTE, create temp table, run outer SELECT on it
			cte, _ := scope.ResolveCTE(stmt.TableName)
			cteRes, err := scope.ExecuteCTE(cte, ctx)
			if err != nil {
				return nil, err
			}

			// Create temporary table from CTE result
			tempTable := "_cte_" + stmt.TableName

			// Build schema from CTE result columns
			schema := storage.TableSchema{
				Name:     tempTable,
				Database: dbName,
				Columns:  make([]storage.ColumnSchema, len(cteRes.Columns)),
			}
			for i, col := range cteRes.Columns {
				schema.Columns[i] = storage.ColumnSchema{Name: col, Type: "TEXT"}
			}

			// Create temp table
			if err := ctx.Storage.CreateTable(dbName, schema); err != nil {
				return nil, fmt.Errorf("CTE temp table: %w", err)
			}
			defer ctx.Storage.DropTable(dbName, tempTable)

			// Insert CTE rows into temp table
			rows := make([]storage.Row, len(cteRes.Rows))
			for i, r := range cteRes.Rows {
				row := make(storage.Row, len(r))
				for j, v := range r {
					row[j] = v
				}
				rows[i] = row
			}
			if _, err := ctx.Storage.InsertRows(dbName, tempTable, rows); err != nil {
				return nil, fmt.Errorf("CTE temp insert: %w", err)
			}

			// Modify outer SELECT to use temp table
			stmt.TableName = tempTable
			cmd, err := ctx.CreateCommand(stmt)
			if err != nil {
				return nil, err
			}
			return cmd.Execute(ctx)
		}
	}

	// Regular SELECT
	cmd, err := ctx.CreateCommand(stmt)
	if err != nil {
		return nil, err
	}
	return cmd.Execute(ctx)
}

// FormatCTEResult formats the CTE result for output.
func FormatCTEResult(res *Result) *Result {
	if res == nil {
		return &Result{Type: "message", Message: "CTE executed successfully"}
	}
	return res
}
