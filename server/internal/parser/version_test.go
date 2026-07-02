package parser_test

import (
	"testing"
	"vaultdb/internal/parser"
)

func TestVersionAsColumnName(t *testing.T) {
	tests := []struct {
		name  string
		query string
	}{
		{"CREATE TABLE with version column", "CREATE TABLE deals (id INT PRIMARY KEY, version INT);"},
		{"SELECT with version column", "SELECT * FROM deals WHERE version = 1;"},
		{"INSERT with version column", "INSERT INTO deals (id, version) VALUES (1, 1);"},
		{"UPDATE with version column", "UPDATE deals SET version = 2 WHERE id = 1;"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := parser.Parse(tt.query)
			if err != nil {
				t.Errorf("Parse(%q) failed: %v", tt.query, err)
			}
		})
	}
}
