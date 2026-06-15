package main

import (
	"encoding/json"
	"fmt"
	"log"
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
	conn, err := net.Dial("tcp", "127.0.0.1:5432")
	if err != nil {
		log.Fatal(err)
	}
	defer conn.Close()

	mustExecute(conn, "USE bench_db;")
	mustExecute(conn, "EXPLAIN ANALYZE SELECT * FROM users WHERE id = 100;")
}

func mustExecute(conn net.Conn, query string) {
	req := Request{ID: "check", Query: query}
	bytes, err := json.Marshal(req)
	if err != nil {
		log.Fatalf("marshal request: %v", err)
	}
	if _, err := conn.Write(append(bytes, '\n')); err != nil {
		log.Fatalf("write to conn: %v", err)
	}

	buf := make([]byte, 8192)
	n, err := conn.Read(buf)
	if err != nil {
		log.Fatalf("read from conn: %v", err)
	}
	var resp Response
	if err := json.Unmarshal(buf[:n], &resp); err != nil {
		log.Fatalf("unmarshal response: %v", err)
	}
	fmt.Printf("Query: %s\nResponse: %s\n\n", query, resp.Message)
}
