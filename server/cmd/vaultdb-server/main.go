package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net"

	"vaultdb/internal/executor"
	"vaultdb/internal/parser"
	"vaultdb/internal/storage"
)

type Request struct {
	ID    string `json:"id"`
	Query string `json:"query"`
}

type Response struct {
	ID       string     `json:"id"`
	Status   string     `json:"status"`
	Type     string     `json:"type"`
	Columns  []string   `json:"columns"`
	Rows     [][]string `json:"rows"`
	Affected int        `json:"affected"`
	Message  string     `json:"message,omitempty"`
}

func main() {
	host := flag.String("host", "127.0.0.1", "Host to listen on")
	port := flag.Int("port", 5432, "Port to listen on")
	dataDir := flag.String("data", "./data", "Path to data directory")
	flag.Parse()

	store := storage.NewFileStorageEngine(*dataDir)

	addr := fmt.Sprintf("%s:%d", *host, *port)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		log.Fatal(err)
	}

	fmt.Printf("[VaultDB] Server started on %s\n", addr)

	for {
		conn, err := ln.Accept()
		if err != nil {
			log.Printf("accept error: %v", err)
			continue
		}
		go handleConnection(conn, store)
	}
}

func handleConnection(conn net.Conn, store storage.StorageEngine) {
	defer conn.Close()

	session := executor.NewSession(store)
	scanner := bufio.NewScanner(conn)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	for scanner.Scan() {
		line := scanner.Bytes()

		var req Request
		if err := json.Unmarshal(line, &req); err != nil {
			sendError(conn, "", "invalid JSON request")
			continue
		}

		stmt, err := parser.Parse(req.Query)
		if err != nil {
			sendError(conn, req.ID, err.Error())
			continue
		}

		result, err := session.Execute(stmt)
		if err != nil {
			sendError(conn, req.ID, err.Error())
			continue
		}

		if err := sendResult(conn, req.ID, result); err != nil {
			log.Printf("write response failed: %v", err)
			return
		}
	}

	if err := scanner.Err(); err != nil {
		log.Printf("connection scanner error: %v", err)
	}
}

func sendError(conn net.Conn, id, message string) {
	resp := Response{
		ID:      id,
		Status:  "error",
		Type:    "error",
		Columns: []string{},
		Rows:    [][]string{},
		Message: message,
	}
	_ = writeResponse(conn, resp)
}

func sendResult(conn net.Conn, id string, result *executor.Result) error {
	columns := result.Columns
	if columns == nil {
		columns = []string{}
	}

	rows := result.Rows
	if rows == nil {
		rows = [][]string{}
	}

	resp := Response{
		ID:       id,
		Status:   "ok",
		Type:     result.Type,
		Columns:  columns,
		Rows:     rows,
		Affected: result.Affected,
		Message:  result.Message,
	}
	return writeResponse(conn, resp)
}

func writeResponse(conn net.Conn, response Response) error {
	bytes, err := json.Marshal(response)
	if err != nil {
		return err
	}
	bytes = append(bytes, '\n')
	_, err = conn.Write(bytes)
	return err
}
