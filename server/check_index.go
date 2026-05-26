package main

import (
	"encoding/json"
	"fmt"
	"net"
)

type Request struct {
	ID    string `json:"id"`
	Query string `json:"query"`
}

type Response struct {
	ID      string `json:"id"`
	Status  string `json:"status"`
	Message string `json:"message"`
}

func main() {
	conn, _ := net.Dial("tcp", "127.0.0.1:5432")
	defer conn.Close()

	mustExecute(conn, "USE bench_db;")
	mustExecute(conn, "EXPLAIN ANALYZE SELECT * FROM users WHERE id = 100;")
}

func mustExecute(conn net.Conn, query string) {
	req := Request{ID: "check", Query: query}
	bytes, _ := json.Marshal(req)
	conn.Write(append(bytes, '\n'))

	buf := make([]byte, 8192)
	n, _ := conn.Read(buf)
	var resp Response
	json.Unmarshal(buf[:n], &resp)
	fmt.Printf("Query: %s\nResponse: %s\n\n", query, resp.Message)
}
