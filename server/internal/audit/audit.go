package audit

import (
	"encoding/json"
	"log/slog"
	"os"
	"time"
)

// EventType тип события аудита.
type EventType string

const (
	EventDDL         EventType = "DDL"
	EventDML         EventType = "DML"
	EventAuth        EventType = "AUTH"
	EventSystem      EventType = "SYSTEM"
)

// Event — событие аудита.
type Event struct {
	Timestamp time.Time `json:"timestamp"`
	EventType EventType `json:"event_type"`
	User      string    `json:"user,omitempty"`
	Database  string    `json:"database,omitempty"`
	Table     string    `json:"table,omitempty"`
	Operation string    `json:"operation"`
	SQL       string    `json:"sql,omitempty"`
	Success   bool      `json:"success"`
	Error     string    `json:"error,omitempty"`
	RowsAffected int    `json:"rows_affected,omitempty"`
	DurationMs float64  `json:"duration_ms,omitempty"`
}

// Logger — логгер аудита.
type Logger struct {
	logger *slog.Logger
	file   *os.File
}

// NewLogger создаёт логгер аудита.
// Caller must call Close() on shutdown to release the underlying file.
func NewLogger(filename string) (*Logger, error) {
	if filename == "" {
		return &Logger{logger: slog.Default()}, nil
	}

	file, err := os.OpenFile(filename, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return nil, err
	}

	logger := slog.New(slog.NewJSONHandler(file, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))

	return &Logger{
		logger: logger,
		file:   file,
	}, nil
}

// Log записывает событие аудита.
func (l *Logger) Log(event Event) {
	event.Timestamp = time.Now().UTC()

	data, err := json.Marshal(event)
	if err != nil {
		l.logger.Error("failed to marshal audit event", "error", err)
		return
	}

	l.logger.Info(string(data))
}

// Close закрывает файл лога.
func (l *Logger) Close() error {
	if l.file != nil {
		return l.file.Close()
	}
	return nil
}
