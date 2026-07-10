package index

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
)

// GINIndex — Generalized Inverted Index для полноtextового поиска и JSON.
type GINIndex struct {
	mu        sync.RWMutex
	name      string
	column    string
	colIndex  int
	indexType string // "text" или "jsonb"

	// Inverted index: токен → []int (позиции строк)
	data map[string][]int
	// Reverse mapping: row position → tokens
	reverse map[int][]string
	// JSONB: позиция строки → значение (для contains/contained_by)
	jsonValues map[int]string
}

func NewGINIndex(name, column string, colIndex int) *GINIndex {
	return &GINIndex{
		name:       name,
		column:     column,
		colIndex:   colIndex,
		indexType:  "text",
		data:       make(map[string][]int),
		reverse:    make(map[int][]string),
		jsonValues: make(map[int]string),
	}
}

func NewGINJSONBIndex(name, column string, colIndex int) *GINIndex {
	return &GINIndex{
		name:       name,
		column:     column,
		colIndex:   colIndex,
		indexType:  "jsonb",
		data:       make(map[string][]int),
		reverse:    make(map[int][]string),
		jsonValues: make(map[int]string),
	}
}

func (g *GINIndex) Type() string   { return "gin" }
func (g *GINIndex) Name() string   { return g.name }
func (g *GINIndex) Column() string { return g.column }
func (g *GINIndex) ColIndex() int  { return g.colIndex }

// Columns returns nil — GIN index does not support index-only scan.
func (g *GINIndex) Columns() []string { return nil }

// HasStoredColumns returns false — GIN index does not store columns.
func (g *GINIndex) HasStoredColumns() bool { return false }

// GetStoredColumns returns nil — GIN index does not store columns.
func (g *GINIndex) GetStoredColumns(rowPos int) (map[string]interface{}, bool) {
	return nil, false
}

func (g *GINIndex) RenameColumn(old, new string) {
	g.mu.Lock()
	g.column = new
	g.mu.Unlock()
}

func (g *GINIndex) Lookup(value string) ([]int, bool) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	tokens := tokenize(strings.ToLower(value))
	if len(tokens) == 0 {
		return nil, false
	}
	result := make(map[int]bool)
	first := true
	for _, token := range tokens {
		positions := g.data[token]
		if first {
			for _, pos := range positions {
				result[pos] = true
			}
			first = false
		} else {
			for pos := range result {
				found := false
				for _, p := range positions {
					if p == pos {
						found = true
						break
					}
				}
				if !found {
					delete(result, pos)
				}
			}
		}
	}
	ids := make([]int, 0, len(result))
	for id := range result {
		ids = append(ids, id)
	}
	return ids, len(ids) > 0
}

func (g *GINIndex) Insert(value string, rowPos int) {
	g.Add(rowPos, value)
}

func (g *GINIndex) Delete(rowPos int) {
	g.Remove(rowPos)
}

func (g *GINIndex) Rebuild(rows []IndexableRow) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.data = make(map[string][]int)
	g.reverse = make(map[int][]string)
	g.jsonValues = make(map[int]string)
	for i, row := range rows {
		if row.DeletedTx == 0 {
			g.addLocked(i, row.Data[g.colIndex])
		}
	}
}

func (g *GINIndex) Add(rowID int, value interface{}) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.addLocked(rowID, value)
}

func (g *GINIndex) addLocked(rowID int, value interface{}) {
	if g.indexType == "jsonb" {
		s := strings.ToLower(strings.TrimSpace(fmt.Sprintf("%v", value)))
		g.jsonValues[rowID] = s
		tokens := tokenizeJSONB(s)
		g.reverse[rowID] = tokens
		for _, token := range tokens {
			g.data[token] = append(g.data[token], rowID)
		}
	} else {
		s := valueToString(value)
		lower := strings.ToLower(s)
		tokens := tokenize(lower)
		g.reverse[rowID] = tokens
		for _, token := range tokens {
			g.data[token] = append(g.data[token], rowID)
		}
	}
}

func (g *GINIndex) Remove(rowID int) {
	g.mu.Lock()
	defer g.mu.Unlock()

	if tokens, ok := g.reverse[rowID]; ok {
		for _, token := range tokens {
			positions := g.data[token]
			for i, pos := range positions {
				if pos == rowID {
					g.data[token] = append(positions[:i], positions[i+1:]...)
					break
				}
			}
			if len(g.data[token]) == 0 {
				delete(g.data, token)
			}
		}
		delete(g.reverse, rowID)
	}
	delete(g.jsonValues, rowID)
}

func (g *GINIndex) Search(query string) []int {
	g.mu.RLock()
	defer g.mu.RUnlock()

	var queryTokens []string
	if g.indexType == "jsonb" {
		queryTokens = tokenizeJSONB(strings.ToLower(query))
	} else {
		queryTokens = tokenize(strings.ToLower(query))
	}

	if len(queryTokens) == 0 {
		return nil
	}

	result := make(map[int]bool)
	first := true
	for _, token := range queryTokens {
		positions := g.data[token]
		if first {
			for _, pos := range positions {
				result[pos] = true
			}
			first = false
		} else {
			for pos := range result {
				found := false
				for _, p := range positions {
					if p == pos {
						found = true
						break
					}
				}
				if !found {
					delete(result, pos)
				}
			}
		}
	}

	ids := make([]int, 0, len(result))
	for id := range result {
		ids = append(ids, id)
	}
	return ids
}

func (g *GINIndex) SearchJSONBContains(query string) []int {
	g.mu.RLock()
	defer g.mu.RUnlock()

	if g.indexType != "jsonb" {
		return nil
	}

	queryTokens := tokenizeJSONB(strings.ToLower(query))
	if len(queryTokens) == 0 {
		return nil
	}

	result := make(map[int]bool)
	for rowID, storedValue := range g.jsonValues {
		storedTokens := tokenizeJSONB(strings.ToLower(storedValue))
		storedSet := make(map[string]bool)
		for _, t := range storedTokens {
			storedSet[t] = true
		}
		allFound := true
		for _, qt := range queryTokens {
			if !storedSet[qt] {
				allFound = false
				break
			}
		}
		if allFound {
			result[rowID] = true
		}
	}

	ids := make([]int, 0, len(result))
	for id := range result {
		ids = append(ids, id)
	}
	return ids
}

func (g *GINIndex) SearchJSONBHasKey(key string) []int {
	g.mu.RLock()
	defer g.mu.RUnlock()

	if g.indexType != "jsonb" {
		return nil
	}

	key = strings.ToLower(key)
	token := "key:" + key
	positions := g.data[token]
	ids := make([]int, len(positions))
	copy(ids, positions)
	return ids
}

func tokenizeJSONB(s string) []string {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "{") && !strings.HasPrefix(s, "[") {
		return tokenize(s)
	}

	var tokens []string
	var data interface{}
	if err := json.Unmarshal([]byte(s), &data); err != nil {
		return tokenize(s)
	}

	var extract func(v interface{})
	extract = func(v interface{}) {
		switch val := v.(type) {
		case map[string]interface{}:
			for k, v := range val {
				tokens = append(tokens, "key:"+strings.ToLower(k))
				tokens = append(tokens, strings.ToLower(fmt.Sprintf("%v", v)))
				extract(v)
			}
		case []interface{}:
			for _, item := range val {
				tokens = append(tokens, strings.ToLower(fmt.Sprintf("%v", item)))
				extract(item)
			}
		default:
			tokens = append(tokens, strings.ToLower(fmt.Sprintf("%v", val)))
		}
	}
	extract(data)
	return tokens
}

func tokenize(s string) []string {
	return strings.FieldsFunc(s, func(r rune) bool {
		return r == ' ' || r == ',' || r == '.' || r == '!' || r == '?' || r == ';' || r == ':'
	})
}

func valueToString(v interface{}) string {
	if v == nil {
		return ""
	}
	return strings.TrimSpace(strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(
		strings.ReplaceAll(fmt.Sprintf("%v", v), "[", ""), "]", ""), "\"", "")))
}

func (g *GINIndex) Save(path string) error {
	g.mu.RLock()
	defer g.mu.RUnlock()

	data := map[string]interface{}{
		"name":       g.name,
		"column":     g.column,
		"colIndex":   g.colIndex,
		"indexType":  g.indexType,
		"data":       g.data,
		"reverse":    g.reverse,
		"jsonValues": g.jsonValues,
	}
	bytes, err := json.Marshal(data)
	if err != nil {
		return err
	}
	return os.WriteFile(path, bytes, 0644) //nolint:gosec // index metadata, not sensitive
}

func (g *GINIndex) Load(path string) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	bytes, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var data map[string]interface{}
	if err := json.Unmarshal(bytes, &data); err != nil {
		return err
	}
	if name, ok := data["name"].(string); ok {
		g.name = name
	}
	if col, ok := data["column"].(string); ok {
		g.column = col
	}
	if ci, ok := data["colIndex"].(float64); ok {
		g.colIndex = int(ci)
	}
	if it, ok := data["indexType"].(string); ok {
		g.indexType = it
	}
	return nil
}
