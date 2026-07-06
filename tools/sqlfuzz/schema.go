package main

import (
	"encoding/json"
	"fmt"
	"os"
)

type ColumnType string

const (
	TypeInt       ColumnType = "INT"
	TypeText      ColumnType = "TEXT"
	TypeFloat     ColumnType = "FLOAT"
	TypeBool      ColumnType = "BOOL"
	TypeBlob      ColumnType = "BLOB"
	TypeTimestamp ColumnType = "TIMESTAMP"
)

type Column struct {
	Name     string     `json:"name"`
	Type     ColumnType `json:"type"`
	Nullable bool       `json:"nullable,omitempty"`
}

type Table struct {
	Name    string   `json:"name"`
	Columns []Column `json:"columns"`
}

type ForeignKey struct {
	FromTable  string `json:"from_table"`
	FromColumn string `json:"from_column"`
	ToTable    string `json:"to_table"`
	ToColumn   string `json:"to_column"`
}

type Schema struct {
	Tables      []Table      `json:"tables"`
	ForeignKeys []ForeignKey `json:"foreign_keys,omitempty"`
}

func DefaultSchema() Schema {
	return Schema{
		Tables: []Table{
			{
				Name: "users",
				Columns: []Column{
					{Name: "id", Type: TypeInt},
					{Name: "username", Type: TypeText},
					{Name: "email", Type: TypeText},
					{Name: "age", Type: TypeInt, Nullable: true},
					{Name: "active", Type: TypeBool},
					{Name: "created_at", Type: TypeTimestamp},
				},
			},
			{
				Name: "orders",
				Columns: []Column{
					{Name: "id", Type: TypeInt},
					{Name: "user_id", Type: TypeInt},
					{Name: "product_name", Type: TypeText},
					{Name: "quantity", Type: TypeInt},
					{Name: "price", Type: TypeFloat},
					{Name: "shipped", Type: TypeBool},
					{Name: "order_date", Type: TypeTimestamp},
				},
			},
			{
				Name: "products",
				Columns: []Column{
					{Name: "id", Type: TypeInt},
					{Name: "name", Type: TypeText},
					{Name: "description", Type: TypeText, Nullable: true},
					{Name: "stock", Type: TypeInt},
					{Name: "price", Type: TypeFloat},
					{Name: "category", Type: TypeText},
				},
			},
			{
				Name: "reviews",
				Columns: []Column{
					{Name: "id", Type: TypeInt},
					{Name: "user_id", Type: TypeInt},
					{Name: "product_id", Type: TypeInt},
					{Name: "rating", Type: TypeInt},
					{Name: "comment", Type: TypeText, Nullable: true},
					{Name: "created_at", Type: TypeTimestamp},
				},
			},
			{
				Name: "logs",
				Columns: []Column{
					{Name: "id", Type: TypeInt},
					{Name: "level", Type: TypeText},
					{Name: "message", Type: TypeText},
					{Name: "user_id", Type: TypeInt, Nullable: true},
					{Name: "timestamp", Type: TypeTimestamp},
				},
			},
		},
		ForeignKeys: []ForeignKey{
			{FromTable: "orders", FromColumn: "user_id", ToTable: "users", ToColumn: "id"},
			{FromTable: "reviews", FromColumn: "user_id", ToTable: "users", ToColumn: "id"},
			{FromTable: "reviews", FromColumn: "product_id", ToTable: "products", ToColumn: "id"},
			{FromTable: "logs", FromColumn: "user_id", ToTable: "users", ToColumn: "id"},
		},
	}
}

func LoadSchema(path string) (Schema, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Schema{}, fmt.Errorf("read schema file: %w", err)
	}

	var schema Schema
	if err := json.Unmarshal(data, &schema); err != nil {
		return Schema{}, fmt.Errorf("parse schema file: %w", err)
	}

	if len(schema.Tables) == 0 {
		return Schema{}, fmt.Errorf("schema has no tables")
	}

	return schema, nil
}

func (s Schema) TableByName(name string) *Table {
	for i := range s.Tables {
		if s.Tables[i].Name == name {
			return &s.Tables[i]
		}
	}
	return nil
}

func (s Schema) ForeignKeysFor(table string) []ForeignKey {
	var result []ForeignKey
	for _, fk := range s.ForeignKeys {
		if fk.FromTable == table || fk.ToTable == table {
			result = append(result, fk)
		}
	}
	return result
}

func (t Table) ColumnNames() []string {
	names := make([]string, len(t.Columns))
	for i, c := range t.Columns {
		names[i] = c.Name
	}
	return names
}

func (t Table) ColumnByName(name string) *Column {
	for i := range t.Columns {
		if t.Columns[i].Name == name {
			return &t.Columns[i]
		}
	}
	return nil
}
