package executor

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"vaultdb/internal/parser"
	"vaultdb/internal/storage"
)

// MaxCopyRows is the default maximum number of rows that COPY FROM will import.
// This prevents loading unbounded data into memory.
const MaxCopyRows = 1_000_000

// validateCopyPath checks that the COPY filename is safe.
// It rejects absolute paths, path traversal, and paths outside the data directory.
func validateCopyPath(filename string, dataDir string) (string, error) {
	cleaned := filepath.Clean(filename)
	if filepath.IsAbs(cleaned) {
		return "", fmt.Errorf("COPY filename must not be absolute: %s", filename)
	}
	if strings.Contains(cleaned, "..") {
		return "", fmt.Errorf("COPY filename must not contain path traversal: %s", filename)
	}
	absPath := filepath.Join(dataDir, cleaned)
	absDataDir := filepath.Clean(dataDir)
	if !strings.HasPrefix(absPath, absDataDir+string(os.PathSeparator)) && absPath != absDataDir {
		return "", fmt.Errorf("COPY filename escapes data directory: %s", filename)
	}
	return absPath, nil
}

type CopyFromCommand struct {
	stmt *parser.CopyStatement
}

func (c *CopyFromCommand) Execute(ctx *ExecutionContext) (*Result, error) {
	dbName, err := requireCurrentDB(ctx)
	if err != nil {
		return nil, err
	}

	if !ctx.Storage.TableExists(dbName, c.stmt.TableName) {
		return nil, fmt.Errorf("table '%s' does not exist", c.stmt.TableName)
	}

	schema, err := ctx.Storage.GetTableSchema(dbName, c.stmt.TableName)
	if err != nil {
		return nil, err
	}

	var reader io.Reader
	var closeFn func()

	if c.stmt.Filename == "STDIN" {
		return nil, fmt.Errorf("COPY FROM STDIN is not yet supported in this implementation")
	}

	absPath, err := validateCopyPath(c.stmt.Filename, ctx.Storage.DataDir())
	if err != nil {
		return nil, err
	}

	f, err := os.Open(absPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open file '%s': %w", c.stmt.Filename, err)
	}
	defer f.Close()
	reader = f
	closeFn = func() { f.Close() }

	if closeFn != nil {
		defer closeFn()
	}

	rows, err := readCopyData(reader, c.stmt.Options, schema)
	if err != nil {
		return nil, err
	}

	if len(rows) == 0 {
		return &Result{Type: "affected", Affected: 0, Message: "COPY: 0 rows imported"}, nil
	}

	if len(rows) > MaxCopyRows {
		return nil, fmt.Errorf("COPY FROM exceeds maximum row limit (%d rows, max %d)", len(rows), MaxCopyRows)
	}

	affected, err := ctx.Storage.InsertRows(dbName, c.stmt.TableName, rows)
	if err != nil {
		return nil, fmt.Errorf("COPY FROM failed during insert: %w", err)
	}

	notifyMutation(ctx, dbName, c.stmt.TableName)

	return &Result{
		Type:    "affected",
		Affected: affected,
		Message: fmt.Sprintf("COPY: %d rows imported", affected),
	}, nil
}

type CopyToCommand struct {
	stmt *parser.CopyStatement
}

func (c *CopyToCommand) Execute(ctx *ExecutionContext) (*Result, error) {
	dbName, err := requireCurrentDB(ctx)
	if err != nil {
		return nil, err
	}

	if !ctx.Storage.TableExists(dbName, c.stmt.TableName) {
		return nil, fmt.Errorf("table '%s' does not exist", c.stmt.TableName)
	}

	schema, err := ctx.Storage.GetTableSchema(dbName, c.stmt.TableName)
	if err != nil {
		return nil, err
	}

	rows, err := ctx.Storage.ReadCurrentRows(dbName, c.stmt.TableName)
	if err != nil {
		return nil, err
	}

	var writer io.Writer
	var closeFn func()

	if c.stmt.Filename == "STDOUT" {
		return nil, fmt.Errorf("COPY TO STDOUT is not yet supported in this implementation")
	}

	absPath, err := validateCopyPath(c.stmt.Filename, ctx.Storage.DataDir())
	if err != nil {
		return nil, err
	}

	f, err := os.Create(absPath)
	if err != nil {
		return nil, fmt.Errorf("failed to create file '%s': %w", c.stmt.Filename, err)
	}
	defer f.Close()
	writer = f
	closeFn = func() { f.Close() }

	if closeFn != nil {
		defer closeFn()
	}

	count, err := writeCopyData(writer, rows, c.stmt.Options, schema)
	if err != nil {
		return nil, fmt.Errorf("COPY TO failed during write: %w", err)
	}

	return &Result{
		Type:    "affected",
		Affected: count,
		Message: fmt.Sprintf("COPY: %d rows exported", count),
	}, nil
}

func readCopyData(reader io.Reader, opts parser.CopyOptions, schema *storage.TableSchema) ([]storage.Row, error) {
	switch opts.Format {
	case "CSV":
		return readCSVData(reader, opts, schema)
	case "JSON", "JSONL":
		return readJSONLData(reader, schema)
	default:
		return nil, fmt.Errorf("unsupported COPY format: %s", opts.Format)
	}
}

func readCSVData(reader io.Reader, opts parser.CopyOptions, schema *storage.TableSchema) ([]storage.Row, error) {
	scanner := bufio.NewScanner(reader)
	var rows []storage.Row
	lineNum := 0
	headerSkipped := false

	for scanner.Scan() {
		lineNum++
		line := scanner.Text()

		if strings.TrimSpace(line) == "" {
			continue
		}

		if opts.Header && !headerSkipped {
			headerSkipped = true
			continue
		}

		fields := parseCSVLine(line, opts.Delimiter)
		row, err := convertFieldsToRow(fields, schema)
		if err != nil {
			return nil, fmt.Errorf("error at line %d: %w", lineNum, err)
		}
		rows = append(rows, row)
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("error reading CSV: %w", err)
	}

	return rows, nil
}

func readJSONLData(reader io.Reader, schema *storage.TableSchema) ([]storage.Row, error) {
	scanner := bufio.NewScanner(reader)
	var rows []storage.Row
	lineNum := 0

	for scanner.Scan() {
		lineNum++
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		var obj map[string]interface{}
		if err := json.Unmarshal([]byte(line), &obj); err != nil {
			return nil, fmt.Errorf("invalid JSON at line %d: %w", lineNum, err)
		}

		row := make(storage.Row, len(schema.Columns))
		for i, col := range schema.Columns {
			val, ok := obj[col.Name]
			if !ok {
				// Try case-insensitive match
				for k, v := range obj {
					if strings.EqualFold(k, col.Name) {
						val = v
						ok = true
						break
					}
				}
			}
			if !ok {
				row[i] = nil
				continue
			}
			converted, err := convertJSONValue(val, col.Type)
			if err != nil {
				return nil, fmt.Errorf("column '%s' at line %d: %w", col.Name, lineNum, err)
			}
			row[i] = converted
		}
		rows = append(rows, row)
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("error reading JSONL: %w", err)
	}

	return rows, nil
}

func writeCopyData(writer io.Writer, rows []storage.Row, opts parser.CopyOptions, schema *storage.TableSchema) (int, error) {
	switch opts.Format {
	case "CSV":
		return writeCSVData(writer, rows, opts, schema)
	case "JSON", "JSONL":
		return writeJSONLData(writer, rows, schema)
	default:
		return 0, fmt.Errorf("unsupported COPY format: %s", opts.Format)
	}
}

func writeCSVData(writer io.Writer, rows []storage.Row, opts parser.CopyOptions, schema *storage.TableSchema) (int, error) {
	delim := opts.Delimiter
	if delim == "" {
		delim = ","
	}

	if opts.Header {
		names := make([]string, len(schema.Columns))
		for i, col := range schema.Columns {
			names[i] = escapeCSVField(col.Name, delim)
		}
		fmt.Fprintln(writer, strings.Join(names, delim))
	}

	for _, row := range rows {
		fields := make([]string, len(row))
		for i, val := range row {
			fields[i] = escapeCSVField(valueToStringCopy(val), delim)
		}
		fmt.Fprintln(writer, strings.Join(fields, delim))
	}

	return len(rows), nil
}

func writeJSONLData(writer io.Writer, rows []storage.Row, schema *storage.TableSchema) (int, error) {
	for _, row := range rows {
		obj := make(map[string]interface{}, len(schema.Columns))
		for i, col := range schema.Columns {
			obj[col.Name] = row[i]
		}
		data, err := json.Marshal(obj)
		if err != nil {
			return 0, fmt.Errorf("failed to marshal row to JSON: %w", err)
		}
		fmt.Fprintln(writer, string(data))
	}

	return len(rows), nil
}

func parseCSVLine(line string, delimiter string) []string {
	var fields []string
	var current strings.Builder
	inQuotes := false
	delimRunes := []rune(delimiter)
	delimChar := rune(0)
	if len(delimRunes) > 0 {
		delimChar = delimRunes[0]
	}

	for i := 0; i < len(line); i++ {
		ch := rune(line[i])

		if inQuotes {
			if ch == '"' {
				// Check for escaped quote ""
				if i+1 < len(line) && rune(line[i+1]) == '"' {
					current.WriteRune('"')
					i++ // skip next quote
				} else {
					inQuotes = false
				}
			} else {
				current.WriteRune(ch)
			}
		} else {
			if ch == '"' {
				inQuotes = true
			} else if ch == delimChar {
				fields = append(fields, current.String())
				current.Reset()
			} else {
				current.WriteRune(ch)
			}
		}
	}
	fields = append(fields, current.String())
	return fields
}

func escapeCSVField(field string, delimiter string) string {
	needsQuoting := false
	delimChar := rune(0)
	if len(delimiter) > 0 {
		delimChar = rune(delimiter[0])
	}

	for _, ch := range field {
		if ch == '"' || ch == delimChar || ch == '\n' || ch == '\r' {
			needsQuoting = true
			break
		}
	}

	if needsQuoting {
		escaped := strings.ReplaceAll(field, `"`, `""`)
		return `"` + escaped + `"`
	}
	return field
}

func convertFieldsToRow(fields []string, schema *storage.TableSchema) (storage.Row, error) {
	row := make(storage.Row, len(schema.Columns))

	for i, col := range schema.Columns {
		if i >= len(fields) {
			row[i] = nil
			continue
		}
		val, err := coerceCSVValue(fields[i], col.Type)
		if err != nil {
			return nil, fmt.Errorf("column '%s': %w", col.Name, err)
		}
		row[i] = val
	}

	return row, nil
}

func coerceCSVValue(s string, colType string) (interface{}, error) {
	s = strings.TrimSpace(s)

	if s == "" || strings.ToUpper(s) == "NULL" {
		return nil, nil
	}

	switch strings.ToUpper(colType) {
	case "INT", "INTEGER", "BIGINT", "SERIAL", "IDENTITY":
		var n int64
		_, err := fmt.Sscanf(s, "%d", &n)
		if err != nil {
			return nil, fmt.Errorf("cannot convert '%s' to integer", s)
		}
		return n, nil
	case "FLOAT", "DOUBLE", "DECIMAL", "NUMERIC":
		var f float64
		_, err := fmt.Sscanf(s, "%f", &f)
		if err != nil {
			return nil, fmt.Errorf("cannot convert '%s' to float", s)
		}
		return f, nil
	case "BOOL", "BOOLEAN":
		lower := strings.ToLower(s)
		if lower == "true" || lower == "t" || lower == "1" || lower == "yes" {
			return true, nil
		}
		if lower == "false" || lower == "f" || lower == "0" || lower == "no" {
			return false, nil
		}
		return nil, fmt.Errorf("cannot convert '%s' to boolean", s)
	case "JSON", "JSONB":
		return s, nil
	default:
		// TEXT, VARCHAR, CHAR, UUID, DATE, TIME, TIMESTAMP, TIMESTAMPTZ, etc.
		return s, nil
	}
}

func convertJSONValue(val interface{}, colType string) (interface{}, error) {
	switch strings.ToUpper(colType) {
	case "INT", "INTEGER", "BIGINT", "SERIAL", "IDENTITY":
		switch v := val.(type) {
		case float64:
			return int64(v), nil
		case int:
			return int64(v), nil
		case int64:
			return v, nil
		case string:
			var n int64
			_, err := fmt.Sscanf(v, "%d", &n)
			return n, err
		default:
			return nil, fmt.Errorf("cannot convert %T to integer", val)
		}
	case "FLOAT", "DOUBLE", "DECIMAL", "NUMERIC":
		switch v := val.(type) {
		case float64:
			return v, nil
		case string:
			var f float64
			_, err := fmt.Sscanf(v, "%f", &f)
			return f, err
		default:
			return nil, fmt.Errorf("cannot convert %T to float", val)
		}
	case "BOOL", "BOOLEAN":
		switch v := val.(type) {
		case bool:
			return v, nil
		default:
			return nil, fmt.Errorf("cannot convert %T to boolean", val)
		}
	default:
		return val, nil
	}
}

func valueToStringCopy(val interface{}) string {
	if val == nil {
		return ""
	}
	switch v := val.(type) {
	case int64:
		return fmt.Sprintf("%d", v)
	case float64:
		return fmt.Sprintf("%g", v)
	case bool:
		if v {
			return "true"
		}
		return "false"
	case string:
		return v
	case []interface{}:
		data, _ := json.Marshal(v)
		return string(data)
	case map[string]interface{}:
		data, _ := json.Marshal(v)
		return string(data)
	default:
		return fmt.Sprintf("%v", v)
	}
}
