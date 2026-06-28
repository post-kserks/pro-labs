package main

import (
	"encoding/json"
	"log/slog"
	"net"
	"strings"

	"vaultdb/internal/executor"
	"vaultdb/internal/protocol"
)

// sendError отправляет ошибку клиенту. Возвращает false, если запись в сокет
// не удалась (клиент отвалился) — в этом случае обрабатывать соединение дальше
// бессмысленно.
func sendError(conn net.Conn, id, message string, logger *slog.Logger) bool {
	resp := protocol.Response{
		ID:      id,
		Status:  "error",
		Type:    "error",
		Columns: []string{},
		Rows:    [][]string{},
		Message: sanitizeErrorMessage(message),
	}
	if err := writeResponse(conn, resp); err != nil {
		logger.Debug("failed to send error response, client disconnected",
			"conn", conn.RemoteAddr(),
			"error", err)
		return false
	}
	return true
}

// sanitizeErrorMessage удаляет внутренние детали из сообщений об ошибках
// перед отправкой клиенту. Сохраняет общее описание, но скрывает пути файлов
// и технические детали реализации.
func sanitizeErrorMessage(msg string) string {
	// Detect filesystem paths: starts with / or contains common path patterns
	lower := strings.ToLower(msg)
	if strings.HasPrefix(msg, "/") ||
		strings.Contains(lower, "/go/src/") ||
		strings.Contains(lower, "\\go\\src\\") ||
		strings.Contains(lower, "/tmp/") ||
		strings.Contains(lower, "heapfile") ||
		strings.Contains(lower, ".go:") {
		return "internal storage error"
	}
	// Если сообщение слишком длинное — обрезаем
	if len(msg) > 200 {
		return msg[:200] + "..."
	}
	return msg
}

func sendResult(conn net.Conn, id string, result *executor.Result) error {
	if result == nil {
		result = &executor.Result{}
	}
	columns := result.Columns
	if columns == nil {
		columns = []string{}
	}

	rows := result.Rows
	if rows == nil {
		rows = [][]string{}
	}

	resp := protocol.Response{
		ID:       id,
		Status:   "ok",
		Type:     result.Type,
		Columns:  columns,
		Rows:     rows,
		Affected: result.Affected,
		Message:  result.Message,
		AsOfNote: result.AsOfNote,
	}
	return writeResponse(conn, resp)
}

func writeResponse(conn net.Conn, response protocol.Response) error {
	bytes, err := json.Marshal(response)
	if err != nil {
		return err
	}
	bytes = append(bytes, '\n')
	_, err = conn.Write(bytes)
	return err
}
