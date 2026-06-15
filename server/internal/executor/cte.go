package executor

import (
	"fmt"
	"strings"
	"vaultdb/internal/parser"
)

const maxCTEIterations = 100

// CTEScope — область видимости CTE для конкретного запроса.
type CTEScope struct {
	ctes   map[string]*CTEDefinition
	parent *CTEScope
}

// CTEDefinition — определение CTE.
type CTEDefinition struct {
	Name    string
	Columns []string
	Query   *parser.SelectStatement
	Result  *Result // закэшированный результат
}

// NewCTEScope создаёт новую область видимости CTE.
func NewCTEScope() *CTEScope {
	return &CTEScope{
		ctes: make(map[string]*CTEDefinition),
	}
}

// PushScope добавляет вложенную область видимости.
func (s *CTEScope) PushScope() *CTEScope {
	return &CTEScope{
		ctes:   make(map[string]*CTEDefinition),
		parent: s,
	}
}

// RegisterCTE регистрирует CTE в текущей области видимости.
func (s *CTEScope) RegisterCTE(cte *CTEDefinition) {
	s.ctes[cte.Name] = cte
}

// ResolveCTE ищет CTE по имени в цепочке областей видимости.
func (s *CTEScope) ResolveCTE(name string) (*CTEDefinition, bool) {
	if cte, ok := s.ctes[name]; ok {
		return cte, true
	}
	if s.parent != nil {
		return s.parent.ResolveCTE(name)
	}
	return nil, false
}

// ExecuteCTE выполняет CTE и кэширует результат.
func (s *CTEScope) ExecuteCTE(cte *CTEDefinition, ctx *ExecutionContext) (*Result, error) {
	if cte.Result != nil {
		return cte.Result, nil
	}

	cmd, err := CommandFactory(cte.Query)
	if err != nil {
		return nil, fmt.Errorf("CTE '%s': %w", cte.Name, err)
	}

	res, err := cmd.Execute(ctx)
	if err != nil {
		return nil, fmt.Errorf("CTE '%s': %w", cte.Name, err)
	}

	cte.Result = res
	return res, nil
}

// ExecuteCTEStatement выполняет CTEStatement.
func ExecuteCTEStatement(stmt *parser.CTEStatement, ctx *ExecutionContext) (*Result, error) {
	scope := NewCTEScope()

	for i := range stmt.CTEs {
		scope.RegisterCTE(&CTEDefinition{
			Name:    stmt.CTEs[i].Name,
			Columns: stmt.CTEs[i].Columns,
			Query:   stmt.CTEs[i].Query,
		})
	}

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
	}

	if selectStmt, ok := stmt.Body.(*parser.SelectStatement); ok {
		if selectStmt.TableName != "" {
			if cte, found := scope.ResolveCTE(selectStmt.TableName); found {
				res, err := scope.ExecuteCTE(cte, ctx)
				if err != nil {
					return nil, err
				}
				return res, nil
			}
		}
		cmd := &SelectCommand{stmt: selectStmt}
		return cmd.Execute(ctx)
	}

	cmd, err := CommandFactory(stmt.Body)
	if err != nil {
		return nil, err
	}
	return cmd.Execute(ctx)
}

func executeRecursiveCTE(cte *parser.CTEDefinition, scope *CTEScope, ctx *ExecutionContext) (*Result, error) {
	query := *cte.Query
	cmd, err := CommandFactory(&query)
	if err != nil {
		return nil, err
	}
	res, err := cmd.Execute(ctx)
	if err != nil {
		return nil, err
	}

	allRows := make([][]string, len(res.Rows))
	copy(allRows, res.Rows)

	visited := make(map[string]bool)
	for _, row := range allRows {
		visited[rowKeyStr(row)] = true
	}

	maxIterations := maxCTEIterations
	for iter := 0; iter < maxIterations; iter++ {
		prevCount := len(allRows)

		tmpCTE := &CTEDefinition{
			Name:    cte.Name,
			Columns: cte.Columns,
			Query:   cte.Query,
			Result: &Result{
				Type:    "rows",
				Columns: res.Columns,
				Rows:    allRows,
			},
		}

		tmpScope := scope.PushScope()
		tmpScope.RegisterCTE(tmpCTE)

		recursiveQuery := *cte.Query
		cmd, err = CommandFactory(&recursiveQuery)
		if err != nil {
			return nil, fmt.Errorf("recursive CTE: %w", err)
		}
		iterRes, err := cmd.Execute(ctx)
		if err != nil {
			return nil, fmt.Errorf("recursive CTE: %w", err)
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

	return &Result{
		Type:    "rows",
		Columns: res.Columns,
		Rows:    allRows,
	}, nil
}

func rowKeyStr(row []string) string {
	parts := make([]string, len(row))
	copy(parts, row)
	return strings.Join(parts, "\x00")
}

// ExecuteSelectWithCTE выполняет SELECT с CTE.
func ExecuteSelectWithCTE(stmt *parser.SelectStatement, ctx *ExecutionContext) (*Result, error) {
	if len(stmt.CTEs) == 0 {
		// Нет CTE — выполняем обычный SELECT
		cmd := &SelectCommand{stmt: stmt}
		return cmd.Execute(ctx)
	}

	scope := NewCTEScope()

	// Регистрируем CTE
	for i := range stmt.CTEs {
		scope.RegisterCTE(&CTEDefinition{
			Name:    stmt.CTEs[i].Name,
			Columns: stmt.CTEs[i].Columns,
			Query:   stmt.CTEs[i].Query,
		})
	}

	// Проверяем, ссылается ли SELECT на CTE
	if stmt.TableName != "" {
		if cte, found := scope.ResolveCTE(stmt.TableName); found {
			res, err := scope.ExecuteCTE(cte, ctx)
			if err != nil {
				return nil, err
			}
			return res, nil
		}
	}

	// Обычный SELECT
	cmd := &SelectCommand{stmt: stmt}
	return cmd.Execute(ctx)
}

// FormatCTEResult форматирует результат CTE для вывода.
func FormatCTEResult(res *Result) *Result {
	if res == nil {
		return &Result{Type: "message", Message: "CTE executed successfully"}
	}
	return res
}
