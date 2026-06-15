package storage

import (
	"fmt"
	"strconv"
	"strings"
)

func validateConstraints(schema *TableSchema, newRows [][]interface{}, data *tableDataDisk) error {
	existingRows := make([]Row, 0, len(data.Rows))
	for _, vr := range data.Rows {
		if vr.DeletedTx == 0 {
			row := make(Row, len(vr.Data))
			for i, v := range vr.Data {
				row[i] = v
			}
			existingRows = append(existingRows, row)
		}
	}
	return validateConstraintsRaw(schema, newRows, existingRows, nil)
}

func existingRowsFromData(data *tableDataDisk) []Row {
	rows := make([]Row, 0, len(data.Rows))
	for _, vr := range data.Rows {
		if vr.DeletedTx == 0 {
			row := make(Row, len(vr.Data))
			for i, v := range vr.Data {
				row[i] = v
			}
			rows = append(rows, row)
		}
	}
	return rows
}

func validateConstraintsRaw(schema *TableSchema, newRows [][]interface{}, existingRows []Row, readRef refTableReader) error {
	for _, constraint := range schema.Constraints {
		switch constraint.Type {
		case "UNIQUE":
			colIndices := make([]int, 0, len(constraint.Columns))
			for _, colName := range constraint.Columns {
				for ci, col := range schema.Columns {
					if col.Name == colName {
						colIndices = append(colIndices, ci)
						break
					}
				}
			}
			if len(colIndices) == 0 {
				break
			}
			seen := make(map[string]bool, len(existingRows)+len(newRows))
			for _, existingRow := range existingRows {
				seen[uniqueKey(existingRow, colIndices)] = true
			}
			for _, newRow := range newRows {
				key := uniqueKey(interfaceSliceToRow(newRow), colIndices)
				if seen[key] {
					return fmt.Errorf("UNIQUE constraint '%s' violated", constraint.Name)
				}
				seen[key] = true
			}
		case "CHECK":
			if constraint.Expr == "" {
				continue
			}
			for _, newRow := range newRows {
				val, err := evaluateCheckExpr(constraint.Expr, newRow, schema)
				if err != nil {
					return fmt.Errorf("CHECK constraint '%s': %w", constraint.Name, err)
				}
				if !val {
					return fmt.Errorf("CHECK constraint '%s' violated", constraint.Name)
				}
			}
		case "FOREIGN_KEY":
			if len(constraint.Columns) == 0 || constraint.RefTable == "" {
				continue
			}
			if readRef == nil {
				continue
			}
			refRows, err := readRef(schema.Database, constraint.RefTable)
			if err != nil {
				continue
			}
			for _, newRow := range newRows {
				for _, colName := range constraint.Columns {
					colIdx := -1
					for i, col := range schema.Columns {
						if col.Name == colName {
							colIdx = i
							break
						}
					}
					if colIdx < 0 || colIdx >= len(newRow) || newRow[colIdx] == nil {
						continue
					}
					val := fmt.Sprintf("%v", newRow[colIdx])
					if val == "" || val == "0" {
						continue
					}
					found := false
					for _, refRow := range refRows {
						if colIdx < len(refRow) && fmt.Sprintf("%v", refRow[colIdx]) == val {
							found = true
							break
						}
					}
					if !found {
						return fmt.Errorf("FOREIGN KEY constraint '%s' violated: value '%s' not found in '%s.%s'",
							constraint.Name, val, schema.Database, constraint.RefTable)
					}
				}
			}
		}
	}

	for i, col := range schema.Columns {
		for _, newRow := range newRows {
			if col.NotNull && i < len(newRow) && newRow[i] == nil {
				return fmt.Errorf("NOT NULL constraint on column '%s' violated", col.Name)
			}
			if col.PrimaryKey && i < len(newRow) && newRow[i] == nil {
				return fmt.Errorf("PRIMARY KEY constraint on column '%s' violated: NULL value", col.Name)
			}
		}
	}

	for i, col := range schema.Columns {
		if col.Type == "ENUM" && len(col.EnumValues) > 0 {
			for _, row := range newRows {
				if i < len(row) && row[i] != nil {
					val := fmt.Sprintf("%v", row[i])
					valid := false
					for _, ev := range col.EnumValues {
						if val == ev {
							valid = true
							break
						}
					}
					if !valid {
						return fmt.Errorf("invalid ENUM value '%s' for column '%s'", val, col.Name)
					}
				}
			}
		}
	}

	return nil
}

func uniqueKey(row Row, colIndices []int) string {
	var b strings.Builder
	for i, idx := range colIndices {
		if i > 0 {
			b.WriteByte(0)
		}
		if idx < len(row) {
			fmt.Fprintf(&b, "%v", row[idx])
		}
	}
	return b.String()
}

func evaluateCheckExpr(expr string, row []interface{}, schema *TableSchema) (bool, error) {
	expr = strings.TrimSpace(expr)

	operators := []string{">=", "<=", "!=", "<>", ">", "<", "="}
	for _, op := range operators {
		idx := findOperator(expr, op)
		if idx >= 0 {
			leftVal := resolveCheckColumn(strings.TrimSpace(expr[:idx]), row, schema)
			rightVal := resolveCheckValue(strings.TrimSpace(expr[idx+len(op):]), row, schema)
			if leftVal != nil && rightVal != nil {
				lf, lok := toFloat(leftVal)
				rf, rok := toFloat(rightVal)
				if lok && rok {
					switch op {
					case ">=":
						return lf >= rf, nil
					case "<=":
						return lf <= rf, nil
					case ">":
						return lf > rf, nil
					case "<":
						return lf < rf, nil
					case "=", "!=", "<>":
						eq := fmt.Sprintf("%v", leftVal) == fmt.Sprintf("%v", rightVal)
						if op == "!=" || op == "<>" {
							return !eq, nil
						}
						return eq, nil
					}
				}
				switch op {
				case ">=":
					return fmt.Sprintf("%v", leftVal) >= fmt.Sprintf("%v", rightVal), nil
				case "<=":
					return fmt.Sprintf("%v", leftVal) <= fmt.Sprintf("%v", rightVal), nil
				case ">":
					return fmt.Sprintf("%v", leftVal) > fmt.Sprintf("%v", rightVal), nil
				case "<":
					return fmt.Sprintf("%v", leftVal) < fmt.Sprintf("%v", rightVal), nil
				case "=", "!=", "<>":
					eq := fmt.Sprintf("%v", leftVal) == fmt.Sprintf("%v", rightVal)
					if op == "!=" || op == "<>" {
						return !eq, nil
					}
					return eq, nil
				}
			}
			return true, nil
		}
	}
	return true, nil
}

func findOperator(expr, op string) int {
	start := 0
	for {
		idx := strings.Index(expr[start:], op)
		if idx < 0 {
			return -1
		}
		pos := start + idx
		if pos > 0 {
			prev := expr[pos-1]
			if prev == ' ' || prev == '(' || prev == ',' {
				return pos
			}
		} else {
			return pos
		}
		start = pos + len(op)
	}
}

func resolveCheckColumn(name string, row []interface{}, schema *TableSchema) interface{} {
	for i, col := range schema.Columns {
		if col.Name == name && i < len(row) {
			return row[i]
		}
	}
	return nil
}

func resolveCheckValue(val string, row []interface{}, schema *TableSchema) interface{} {
	val = strings.TrimSpace(val)
	if i, err := strconv.ParseInt(val, 10, 64); err == nil {
		return i
	}
	if f, err := strconv.ParseFloat(val, 64); err == nil {
		return f
	}
	val = strings.Trim(val, "'\"")
	return val
}
