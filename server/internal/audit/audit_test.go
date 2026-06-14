package audit

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestAudit(t *testing.T) {
	tests := []struct {
		name string
		fn   func(t *testing.T)
	}{
		{"new_logger_empty", testNewLoggerEmpty},
		{"new_logger_file", testNewLoggerFile},
		{"close", testClose},
	}
	for _, tt := range tests {
		t.Run(tt.name, tt.fn)
	}
}

func testNewLoggerEmpty(t *testing.T) {
	l, err := NewLogger("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if l.logger == nil {
		t.Fatal("expected non-nil logger")
	}
	if l.file != nil {
		t.Error("expected nil file for empty filename")
	}
}

func testNewLoggerFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.log")

	l, err := NewLogger(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	event := Event{
		EventType: EventDDL,
		User:      "testuser",
		Database:  "testdb",
		Operation: "CREATE TABLE",
		SQL:       "CREATE TABLE t (id INT)",
		Success:   true,
	}

	l.Log(event)
	l.Close()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read log file: %v", err)
	}

	if len(data) == 0 {
		t.Fatal("expected non-empty log file after Log()")
	}

	var outer map[string]interface{}
	if err := json.Unmarshal(data, &outer); err != nil {
		t.Fatalf("failed to parse outer JSON log: %v\nraw: %s", err, data)
	}

	msgStr, ok := outer["msg"].(string)
	if !ok {
		t.Fatalf("expected 'msg' field to be a string, got %T: %v", outer["msg"], outer["msg"])
	}

	var parsed map[string]interface{}
	if err := json.Unmarshal([]byte(msgStr), &parsed); err != nil {
		t.Fatalf("failed to parse inner event JSON: %v\nmsg: %s", err, msgStr)
	}

	if parsed["event_type"] != "DDL" {
		t.Errorf("expected event_type=DDL, got %v", parsed["event_type"])
	}
	if parsed["user"] != "testuser" {
		t.Errorf("expected user=testuser, got %v", parsed["user"])
	}
	if parsed["success"] != true {
		t.Errorf("expected success=true, got %v", parsed["success"])
	}
}

func testClose(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.log")

	l, err := NewLogger(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if err := l.Close(); err != nil {
		t.Fatalf("unexpected error on Close: %v", err)
	}

	if _, err := l.file.WriteString("should fail"); err == nil {
		t.Error("expected error writing to closed file")
	}

	event := Event{
		EventType: EventSystem,
		Operation: "shutdown",
		Timestamp: time.Now(),
	}
	l.Log(event)

	_ = l.Close()
}
