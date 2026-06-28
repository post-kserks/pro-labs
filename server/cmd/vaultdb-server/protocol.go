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
// перед отправкой клиенту. Whitelist подход: безопасные сообщения проходят,
// всё остальное заменяется на generic "internal error".
func sanitizeErrorMessage(msg string) string {
	lower := strings.ToLower(msg)

	// Безопасные паттерны — можно показать клиенту
	safePatterns := []string{
		"no active database",
		"does not exist",
		"already exists",
		"duplicate primary key",
		"column",
		"unknown column",
		"unknown statement",
		"unauthorized",
		"rate limit",
		"too many",
		"overflow",
		"query timeout",
		"mismatch",
		"invalid",
		"expected",
		"unsupported",
		"empty",
		"savepoint",
		"transaction",
		"not supported",
		"missing",
		"must not",
		"out of range",
		"cannot",
		"permission",
		"aggregate",
	}

	for _, pattern := range safePatterns {
		if strings.Contains(lower, pattern) {
			if len(msg) > 200 {
				return msg[:200] + "..."
			}
			return msg
		}
	}

	// Всё остальное — generic ошибка без деталей
	return "internal error"
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
