package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"math/rand"
	"net"
	"sync"
	"time"

	"vaultdb/benchmarks"
)

type Request struct {
	ID    string `json:"id"`
	Query string `json:"query"`
}

type Response struct {
	ID     string `json:"id"`
	Status string `json:"status"`
}

func main() {
	host := flag.String("host", "127.0.0.1", "VaultDB host")
	port := flag.Int("port", 5432, "VaultDB port")
	rows := flag.Int("rows", 1000, "Number of rows to insert")
	conns := flag.Int("conns", 10, "Number of concurrent connections")
	flag.Parse()

	fmt.Printf("Starting benchmark: %d rows, %d connections\n", *rows, *conns)

	// Setup: create database and table
	conn, err := net.Dial("tcp", fmt.Sprintf("%s:%d", *host, *port))
	if err != nil {
		log.Fatalf("failed to connect: %v", err)
	}
	executeIgnoreError(conn, "CREATE DATABASE bench_db;")
	executeIgnoreError(conn, "USE bench_db;")
	executeIgnoreError(conn, "CREATE TABLE users (id INT, name TEXT, age INT);")
	executeIgnoreError(conn, "CREATE INDEX idx_id ON users(id);")
	conn.Close()

	start := time.Now()
	var wg sync.WaitGroup
	rowsPerConn := *rows / *conns
	globalTracker := benchmarks.NewLatencyTracker()

	for i := 0; i < *conns; i++ {
		wg.Add(1)
		go func(connIdx int) {
			defer wg.Done()
			workerTracker := benchmarks.NewLatencyTracker()
			c, err := net.Dial("tcp", fmt.Sprintf("%s:%d", *host, *port))
			if err != nil {
				log.Printf("worker %d failed to connect: %v", connIdx, err)
				return
			}
			defer c.Close()
			mustExecute(c, "USE bench_db;")

			for j := 0; j < rowsPerConn; j++ {
				id := connIdx*rowsPerConn + j
				query := fmt.Sprintf("INSERT INTO users VALUES (%d, 'user_%d', %d);", id, id, rand.Intn(100))
				startOp := time.Now()
				mustExecute(c, query)
				workerTracker.Record(time.Since(startOp))
			}
			globalTracker.Merge(workerTracker)
		}(i)
	}

	wg.Wait()
	duration := time.Since(start)

	fmt.Printf("\n--- Benchmark Results ---\n")
	fmt.Printf("Total Rows:    %d\n", *rows)
	fmt.Printf("Connections:   %d\n", *conns)
	fmt.Printf("Total Time:    %v\n", duration)
	fmt.Printf("Throughput:    %.2f rows/sec\n", float64(*rows)/duration.Seconds())
	fmt.Printf("%s\n", globalTracker.Calculate().String())

	// Test Index performance
	conn, _ = net.Dial("tcp", fmt.Sprintf("%s:%d", *host, *port))
	mustExecute(conn, "USE bench_db;")

	fmt.Printf("\nTesting Index Speedup...\n")
	idxTracker := benchmarks.NewLatencyTracker()
	start = time.Now()
	for k := 0; k < 10; k++ {
		searchID := rand.Intn(*rows)
		startOp := time.Now()
		mustExecute(conn, fmt.Sprintf("SELECT * FROM users WHERE id = %d;", searchID))
		idxTracker.Record(time.Since(startOp))
	}
	indexDuration := time.Since(start) / 10
	fmt.Printf("Index Lookup:  %v (%d ns avg)\n", indexDuration, idxTracker.Calculate().Avg)
	idxSummary := idxTracker.Calculate()
	fmt.Printf("  Index Lookup Percentiles - p50: %d ns, p95: %d ns, p99: %d ns, p99.9: %d ns\n",
		idxSummary.P50, idxSummary.P95, idxSummary.P99, idxSummary.P999)

	// Without Index (full scan)
	// We don't have a way to force no index yet, but we can search by non-indexed column
	scanTracker := benchmarks.NewLatencyTracker()
	start = time.Now()
	for k := 0; k < 10; k++ {
		startOp := time.Now()
		mustExecute(conn, "SELECT * FROM users WHERE age = -1;") // Force full scan
		scanTracker.Record(time.Since(startOp))
	}
	fullScanDuration := time.Since(start) / 10
	fmt.Printf("Full Scan:     %v (%d ns avg)\n", fullScanDuration, scanTracker.Calculate().Avg)
	scanSummary := scanTracker.Calculate()
	fmt.Printf("  Full Scan Percentiles    - p50: %d ns, p95: %d ns, p99: %d ns, p99.9: %d ns\n",
		scanSummary.P50, scanSummary.P95, scanSummary.P99, scanSummary.P999)

	if indexDuration > 0 {
		fmt.Printf("Speedup:       %.1fx\n", float64(fullScanDuration)/float64(indexDuration))
	}
}

func mustExecute(conn net.Conn, query string) {
	req := Request{ID: "bench", Query: query}
	bytes, _ := json.Marshal(req)
	conn.Write(append(bytes, '\n'))

	buf := make([]byte, 4096)
	n, err := conn.Read(buf)
	if err != nil {
		log.Fatalf("read failed: %v", err)
	}

	var resp Response
	if err := json.Unmarshal(buf[:n], &resp); err != nil {
		// NDJSON might have multiple lines, but for bench we assume one
	}
	if resp.Status == "error" {
		log.Printf("query failed: %s", query)
	}
}

func executeIgnoreError(conn net.Conn, query string) {
	req := Request{ID: "bench-setup", Query: query}
	bytes, _ := json.Marshal(req)
	conn.Write(append(bytes, '\n'))

	buf := make([]byte, 4096)
	_, _ = conn.Read(buf)
	// We don't care if it fails (e.g. database already exists)
}
