package main

import (
	"fmt"
	"math/rand"
	"strings"
)

type Generator struct {
	schema Schema
	rng    *rand.Rand
}

func NewGenerator(schema Schema, seed int64) *Generator {
	return &Generator{
		schema: schema,
		rng:    rand.New(rand.NewSource(seed)),
	}
}

func (g *Generator) Generate() string {
	switch g.rng.Intn(10) {
	case 0, 1, 2:
		return g.generateSelect()
	case 3:
		return g.generateSelectJoin()
	case 4:
		return g.generateSelectAggregate()
	case 5:
		return g.generateSelectSubquery()
	case 6:
		return g.generateInsert()
	case 7:
		return g.generateUpdate()
	case 8:
		return g.generateDelete()
	case 9:
		return g.generateDDL()
	}
	return g.generateSelect()
}

func (g *Generator) pickTable() Table {
	return g.schema.Tables[g.rng.Intn(len(g.schema.Tables))]
}

func (g *Generator) pickColumn(table Table) Column {
	return table.Columns[g.rng.Intn(len(table.Columns))]
}

func (g *Generator) pickColumns(table Table, max int) []Column {
	n := g.rng.Intn(max) + 1
	if n > len(table.Columns) {
		n = len(table.Columns)
	}

	shuffled := make([]Column, len(table.Columns))
	copy(shuffled, table.Columns)
	g.rng.Shuffle(len(shuffled), func(i, j int) {
		shuffled[i], shuffled[j] = shuffled[j], shuffled[i]
	})

	return shuffled[:n]
}

func (g *Generator) pickNumericColumn(table Table) *Column {
	var numericCols []Column
	for _, c := range table.Columns {
		if c.Type == TypeInt || c.Type == TypeFloat {
			numericCols = append(numericCols, c)
		}
	}
	if len(numericCols) == 0 {
		return nil
	}
	return &numericCols[g.rng.Intn(len(numericCols))]
}

func (g *Generator) generateValue(typ ColumnType) string {
	switch typ {
	case TypeInt:
		return fmt.Sprintf("%d", g.rng.Intn(10000)-5000)
	case TypeFloat:
		return fmt.Sprintf("%.2f", g.rng.Float64()*1000-500)
	case TypeText:
		return g.generateString()
	case TypeBool:
		if g.rng.Intn(2) == 0 {
			return "TRUE"
		}
		return "FALSE"
	case TypeBlob:
		return fmt.Sprintf("X'%x'", g.rng.Intn(256))
	case TypeTimestamp:
		year := 2020 + g.rng.Intn(6)
		month := g.rng.Intn(12) + 1
		day := g.rng.Intn(28) + 1
		hour := g.rng.Intn(24)
		min := g.rng.Intn(60)
		sec := g.rng.Intn(60)
		return fmt.Sprintf("'%04d-%02d-%02d %02d:%02d:%02d'", year, month, day, hour, min, sec)
	default:
		return "NULL"
	}
}

func (g *Generator) generateString() string {
	words := []string{
		"hello", "world", "test", "data", "alpha", "beta", "gamma",
		"delta", "epsilon", "zeta", "eta", "theta", "iota", "kappa",
		"lambda", "mu", "nu", "xi", "omicron", "pi", "rho", "sigma",
		"tau", "upsilon", "phi", "chi", "psi", "omega", "foo", "bar",
		"baz", "qux", "quux", "corge", "grault", "garply", "waldo",
		"fred", "plugh", "xyzzy", "thud", "wibble", "wobble", "wubble",
	}
	word := words[g.rng.Intn(len(words))]

	if g.rng.Intn(3) == 0 {
		word = strings.ToUpper(word[:1]) + word[1:]
	}

	if g.rng.Intn(4) == 0 {
		word += "'s value"
	}

	return fmt.Sprintf("'%s'", strings.ReplaceAll(word, "'", "''"))
}

func (g *Generator) generateWhere(table Table) string {
	if g.rng.Intn(3) == 0 {
		return ""
	}

	conditions := g.rng.Intn(3) + 1
	var parts []string
	for i := 0; i < conditions; i++ {
		col := g.pickColumn(table)
		cond := g.generateCondition(col)
		parts = append(parts, cond)
	}

	return " WHERE " + strings.Join(parts, " AND ")
}

func (g *Generator) generateCondition(col Column) string {
	switch col.Type {
	case TypeInt, TypeFloat:
		return g.generateNumericCondition(col)
	case TypeText:
		return g.generateTextCondition(col)
	case TypeBool:
		if g.rng.Intn(2) == 0 {
			return fmt.Sprintf("%s = TRUE", col.Name)
		}
		return fmt.Sprintf("%s = FALSE", col.Name)
	case TypeTimestamp:
		return g.generateTimestampCondition(col)
	case TypeBlob:
		return fmt.Sprintf("%s IS NOT NULL", col.Name)
	default:
		return fmt.Sprintf("%s IS NOT NULL", col.Name)
	}
}

func (g *Generator) generateNumericCondition(col Column) string {
	op := []string{"=", "!=", "<", ">", "<=", ">="}[g.rng.Intn(6)]
	val := g.generateValue(col.Type)
	return fmt.Sprintf("%s %s %s", col.Name, op, val)
}

func (g *Generator) generateTextCondition(col Column) string {
	switch g.rng.Intn(4) {
	case 0:
		return fmt.Sprintf("%s = %s", col.Name, g.generateString())
	case 1:
		return fmt.Sprintf("%s != %s", col.Name, g.generateString())
	case 2:
		return fmt.Sprintf("%s LIKE %s", col.Name, g.generateLikePattern())
	case 3:
		return fmt.Sprintf("%s IS NOT NULL", col.Name)
	default:
		return fmt.Sprintf("%s IS NOT NULL", col.Name)
	}
}

func (g *Generator) generateTimestampCondition(col Column) string {
	switch g.rng.Intn(3) {
	case 0:
		return fmt.Sprintf("%s > %s", col.Name, g.generateValue(TypeTimestamp))
	case 1:
		return fmt.Sprintf("%s < %s", col.Name, g.generateValue(TypeTimestamp))
	case 2:
		return fmt.Sprintf("%s IS NOT NULL", col.Name)
	default:
		return fmt.Sprintf("%s IS NOT NULL", col.Name)
	}
}

func (g *Generator) generateLikePattern() string {
	patterns := []string{
		"'%hello%'",
		"'test%'",
		"'%data'",
		"'a%'",
		"'%b'",
		"'%match%'",
	}
	return patterns[g.rng.Intn(len(patterns))]
}

func (g *Generator) generateOrderBy(table Table) string {
	if g.rng.Intn(3) == 0 {
		return ""
	}

	n := g.rng.Intn(2) + 1
	var parts []string
	for i := 0; i < n; i++ {
		col := g.pickColumn(table)
		dir := "ASC"
		if g.rng.Intn(3) == 0 {
			dir = "DESC"
		}
		parts = append(parts, fmt.Sprintf("%s %s", col.Name, dir))
	}
	return " ORDER BY " + strings.Join(parts, ", ")
}

func (g *Generator) generateLimit() string {
	if g.rng.Intn(3) == 0 {
		return ""
	}
	limit := g.rng.Intn(100) + 1
	offset := 0
	if g.rng.Intn(3) == 0 {
		offset = g.rng.Intn(100)
		return fmt.Sprintf(" LIMIT %d OFFSET %d", limit, offset)
	}
	return fmt.Sprintf(" LIMIT %d", limit)
}

func (g *Generator) generateSelect() string {
	table := g.pickTable()
	columns := g.pickColumns(table, 5)

	var colNames []string
	for _, c := range columns {
		if g.rng.Intn(10) == 0 {
			colNames = append(colNames, fmt.Sprintf("%s AS alias_%d", c.Name, g.rng.Intn(1000)))
		} else {
			colNames = append(colNames, c.Name)
		}
	}

	if g.rng.Intn(4) == 0 {
		colNames = []string{"*"}
	}

	where := g.generateWhere(table)
	orderBy := g.generateOrderBy(table)
	limit := g.generateLimit()

	return fmt.Sprintf("SELECT %s FROM %s%s%s%s;",
		strings.Join(colNames, ", "),
		table.Name,
		where,
		orderBy,
		limit,
	)
}

func (g *Generator) generateSelectJoin() string {
	if len(g.schema.ForeignKeys) == 0 || len(g.schema.Tables) < 2 {
		return g.generateSelect()
	}

	fk := g.schema.ForeignKeys[g.rng.Intn(len(g.schema.ForeignKeys))]

	leftTable := g.schema.TableByName(fk.FromTable)
	rightTable := g.schema.TableByName(fk.ToTable)
	if leftTable == nil || rightTable == nil {
		return g.generateSelect()
	}

	joinType := "INNER JOIN"
	if g.rng.Intn(3) == 0 {
		joinType = "LEFT JOIN"
	}

	leftCols := g.pickColumns(*leftTable, 3)
	rightCols := g.pickColumns(*rightTable, 3)

	var colNames []string
	for _, c := range leftCols {
		colNames = append(colNames, fmt.Sprintf("%s.%s", leftTable.Name, c.Name))
	}
	for _, c := range rightCols {
		if g.rng.Intn(2) == 0 {
			colNames = append(colNames, fmt.Sprintf("%s.%s AS %s_%s", rightTable.Name, c.Name, rightTable.Name, c.Name))
		} else {
			colNames = append(colNames, fmt.Sprintf("%s.%s", rightTable.Name, c.Name))
		}
	}

	onCondition := fmt.Sprintf("%s.%s = %s.%s", leftTable.Name, fk.FromColumn, rightTable.Name, fk.ToColumn)

	where := ""
	if g.rng.Intn(2) == 0 {
		col := g.pickColumn(*leftTable)
		where = " WHERE " + g.generateCondition(col)
	}

	orderBy := g.generateOrderBy(*leftTable)
	limit := g.generateLimit()

	return fmt.Sprintf("SELECT %s FROM %s %s %s ON %s%s%s%s;",
		strings.Join(colNames, ", "),
		leftTable.Name,
		joinType,
		rightTable.Name,
		onCondition,
		where,
		orderBy,
		limit,
	)
}

func (g *Generator) generateSelectAggregate() string {
	table := g.pickTable()
	numericCol := g.pickNumericColumn(table)
	if numericCol == nil {
		return g.generateSelect()
	}

	aggregates := []struct {
		fn  string
		col string
	}{
		{"COUNT", "*"},
		{"COUNT", numericCol.Name},
		{"SUM", numericCol.Name},
		{"AVG", numericCol.Name},
		{"MIN", numericCol.Name},
		{"MAX", numericCol.Name},
	}

	agg := aggregates[g.rng.Intn(len(aggregates))]

	groupByCols := g.pickColumns(table, 3)
	var groupByNames []string
	for _, c := range groupByCols {
		groupByNames = append(groupByNames, c.Name)
	}

	var selectParts []string
	if agg.fn == "COUNT" && agg.col == "*" {
		selectParts = append(selectParts, fmt.Sprintf("%s(*) AS total", agg.fn))
	} else {
		selectParts = append(selectParts, fmt.Sprintf("%s(%s) AS %s_%s", agg.fn, agg.col, strings.ToLower(agg.fn), agg.col))
	}
	selectParts = append(selectParts, groupByNames...)

	where := g.generateWhere(table)
	orderBy := ""
	if g.rng.Intn(2) == 0 {
		if agg.fn == "COUNT" && agg.col == "*" {
			orderBy = " ORDER BY total"
		} else {
			orderBy = fmt.Sprintf(" ORDER BY %s_%s", strings.ToLower(agg.fn), agg.col)
		}
		if g.rng.Intn(2) == 0 {
			orderBy += " DESC"
		}
	}
	limit := g.generateLimit()

	groupBy := ""
	if len(groupByNames) > 0 {
		groupBy = " GROUP BY " + strings.Join(groupByNames, ", ")
	}

	return fmt.Sprintf("SELECT %s FROM %s%s%s%s%s;",
		strings.Join(selectParts, ", "),
		table.Name,
		where,
		groupBy,
		orderBy,
		limit,
	)
}

func (g *Generator) generateSelectSubquery() string {
	table := g.pickTable()
	outerCol := g.pickColumn(table)

	switch g.rng.Intn(3) {
	case 0:
		return g.generateInSubquery(table, outerCol)
	case 1:
		return g.generateExistsSubquery(table)
	case 2:
		return g.generateScalarSubquery(table, outerCol)
	default:
		return g.generateInSubquery(table, outerCol)
	}
}

func (g *Generator) generateInSubquery(table Table, col Column) string {
	if len(g.schema.Tables) < 2 {
		return g.generateSelect()
	}

	var otherTable Table
	for {
		otherTable = g.pickTable()
		if otherTable.Name != table.Name {
			break
		}
		if len(g.schema.Tables) == 1 {
			break
		}
	}

	var otherCol *Column
	for _, c := range otherTable.Columns {
		if c.Type == col.Type {
			otherCol = &c
			break
		}
	}
	if otherCol == nil {
		otherCol = &otherTable.Columns[0]
	}

	var extraConditions string
	if g.rng.Intn(3) != 0 {
		extraCol := g.pickColumn(table)
		cond := g.generateCondition(extraCol)
		extraConditions = " AND " + cond
	}

	return fmt.Sprintf("SELECT * FROM %s WHERE %s IN (SELECT %s FROM %s)%s;",
		table.Name,
		col.Name,
		otherCol.Name,
		otherTable.Name,
		extraConditions,
	)
}

func (g *Generator) generateExistsSubquery(table Table) string {
	if len(g.schema.ForeignKeys) == 0 {
		return g.generateSelect()
	}

	var fk *ForeignKey
	for i := range g.schema.ForeignKeys {
		if g.schema.ForeignKeys[i].FromTable == table.Name || g.schema.ForeignKeys[i].ToTable == table.Name {
			fk = &g.schema.ForeignKeys[i]
			break
		}
	}
	if fk == nil {
		return g.generateSelect()
	}

	subTable := g.schema.TableByName(fk.ToTable)
	if subTable == nil || subTable.Name == table.Name {
		subTable = g.schema.TableByName(fk.FromTable)
	}
	if subTable == nil || subTable.Name == table.Name {
		return g.generateSelect()
	}

	var extraConditions string
	if g.rng.Intn(3) != 0 {
		extraCol := g.pickColumn(table)
		cond := g.generateCondition(extraCol)
		extraConditions = " AND " + cond
	}

	return fmt.Sprintf("SELECT * FROM %s WHERE EXISTS (SELECT 1 FROM %s WHERE %s.%s = %s.%s)%s;",
		table.Name,
		subTable.Name,
		table.Name, fk.FromColumn,
		subTable.Name, fk.ToColumn,
		extraConditions,
	)
}

func (g *Generator) generateScalarSubquery(table Table, col Column) string {
	if len(g.schema.Tables) < 2 {
		return g.generateSelect()
	}

	var otherTable Table
	for {
		otherTable = g.pickTable()
		if otherTable.Name != table.Name {
			break
		}
		if len(g.schema.Tables) == 1 {
			break
		}
	}

	otherCol := g.pickNumericColumn(otherTable)
	if otherCol == nil {
		return g.generateSelect()
	}

	op := []string{"=", ">", "<", ">=", "<="}[g.rng.Intn(5)]

	return fmt.Sprintf("SELECT * FROM %s WHERE %s %s (SELECT %s(%s) FROM %s);",
		table.Name,
		col.Name,
		op,
		[]string{"COUNT", "SUM", "AVG", "MIN", "MAX"}[g.rng.Intn(5)],
		otherCol.Name,
		otherTable.Name,
	)
}

func (g *Generator) generateInsert() string {
	table := g.pickTable()

	var colNames []string
	var values []string
	for _, col := range table.Columns {
		if col.Nullable && g.rng.Intn(4) == 0 {
			continue
		}
		colNames = append(colNames, col.Name)
		values = append(values, g.generateValue(col.Type))
	}

	return fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s);",
		table.Name,
		strings.Join(colNames, ", "),
		strings.Join(values, ", "),
	)
}

func (g *Generator) generateUpdate() string {
	table := g.pickTable()

	nSets := g.rng.Intn(3) + 1
	if nSets > len(table.Columns) {
		nSets = len(table.Columns)
	}

	shuffled := make([]Column, len(table.Columns))
	copy(shuffled, table.Columns)
	g.rng.Shuffle(len(shuffled), func(i, j int) {
		shuffled[i], shuffled[j] = shuffled[j], shuffled[i]
	})

	var setClauses []string
	for i := 0; i < nSets; i++ {
		col := shuffled[i]
		val := g.generateValue(col.Type)
		setClauses = append(setClauses, fmt.Sprintf("%s = %s", col.Name, val))
	}

	where := g.generateWhere(table)

	return fmt.Sprintf("UPDATE %s SET %s%s;",
		table.Name,
		strings.Join(setClauses, ", "),
		where,
	)
}

func (g *Generator) generateDelete() string {
	table := g.pickTable()
	where := g.generateWhere(table)

	if where == "" {
		if g.rng.Intn(2) == 0 {
			col := g.pickColumn(table)
			where = " WHERE " + g.generateCondition(col)
		} else {
			where = " WHERE id > 0"
		}
	}

	return fmt.Sprintf("DELETE FROM %s%s;", table.Name, where)
}

func (g *Generator) generateDDL() string {
	switch g.rng.Intn(3) {
	case 0:
		return g.generateCreateTable()
	case 1:
		return g.generateCreateIndex()
	case 2:
		return g.generateShow()
	default:
		return g.generateShow()
	}
}

func (g *Generator) generateCreateTable() string {
	nCols := g.rng.Intn(5) + 2
	tableName := fmt.Sprintf("fuzz_table_%d", g.rng.Intn(10000))

	colTypes := []ColumnType{TypeInt, TypeText, TypeFloat, TypeBool, TypeBlob, TypeTimestamp}
	keywords := []string{
		"data", "info", "meta", "temp", "log", "event", "record",
		"entry", "item", "task", "job", "proc", "sys", "val",
	}

	var columns []string
	for i := 0; i < nCols; i++ {
		colType := colTypes[g.rng.Intn(len(colTypes))]
		keyword := keywords[g.rng.Intn(len(keywords))]
		colName := fmt.Sprintf("col_%s_%d", keyword, i)

		nullable := ""
		if g.rng.Intn(3) == 0 {
			nullable = " NULL"
		} else {
			nullable = " NOT NULL"
		}

		defaultVal := ""
		if g.rng.Intn(4) == 0 {
			defaultVal = fmt.Sprintf(" DEFAULT %s", g.generateValue(colType))
		}

		columns = append(columns, fmt.Sprintf("  %s %s%s%s", colName, colType, nullable, defaultVal))
	}

	if g.rng.Intn(3) == 0 && len(columns) > 0 {
		primaryKey := g.rng.Intn(len(columns))
		lines := make([]string, len(columns))
		copy(lines, columns)
		lines[primaryKey] = strings.Replace(lines[primaryKey], " NOT NULL", "", 1) + " PRIMARY KEY"
		columns = lines
	}

	return fmt.Sprintf("CREATE TABLE %s (\n%s\n);",
		tableName,
		strings.Join(columns, ",\n"),
	)
}

func (g *Generator) generateCreateIndex() string {
	table := g.pickTable()
	col := g.pickColumn(table)

	indexName := fmt.Sprintf("idx_%s_%s_%d", table.Name, col.Name, g.rng.Intn(10000))
	unique := ""
	if g.rng.Intn(4) == 0 {
		unique = "UNIQUE "
	}

	return fmt.Sprintf("CREATE %sINDEX %s ON %s(%s);",
		unique,
		indexName,
		table.Name,
		col.Name,
	)
}

func (g *Generator) generateShow() string {
	switch g.rng.Intn(3) {
	case 0:
		return "SHOW TABLES;"
	case 1:
		return "SHOW DATABASES;"
	case 2:
		table := g.pickTable()
		return fmt.Sprintf("SHOW CREATE TABLE %s;", table.Name)
	default:
		return "SHOW TABLES;"
	}
}
