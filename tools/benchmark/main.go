package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"math/rand"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"vaultdb/benchmarks"
)

func main() {
	host := flag.String("host", "127.0.0.1", "VaultDB host")
	port := flag.Int("port", 5433, "VaultDB port")
	rows := flag.Int("rows", 1000, "Number of rows/ops")
	conns := flag.Int("conns", 10, "Number of concurrent connections")
	workload := flag.String("workload", "tpcc_lite", "Workload type: tpcc_lite or olap_join")
	flag.Parse()

	fmt.Printf("Starting benchmark: %s workload, %d ops, %d connections\n", *workload, *rows, *conns)

	ctx := context.Background()
	connString := fmt.Sprintf("postgres://vaultdb:bench_token@%s:%d/bench_db", *host, *port)

	pool, err := pgxpool.New(ctx, connString)
	if err != nil {
		log.Fatalf("failed to connect: %v", err)
	}
	defer pool.Close()

	// Setup: attempt to create tables/indices, ignore errors if already exists
	_, _ = pool.Exec(ctx, "CREATE TABLE users (id INT, name TEXT, age INT, balance DECIMAL);")
	_, _ = pool.Exec(ctx, "CREATE INDEX idx_id ON users(id);")
	_, _ = pool.Exec(ctx, "CREATE TABLE orders (id INT, user_id INT, amount DECIMAL);")

	start := time.Now()
	var wg sync.WaitGroup
	opsPerConn := *rows / *conns
	globalTracker := benchmarks.NewLatencyTracker()

	for i := 0; i < *conns; i++ {
		wg.Add(1)
		go func(connIdx int) {
			defer wg.Done()
			workerTracker := benchmarks.NewLatencyTracker()

			for j := 0; j < opsPerConn; j++ {
				startOp := time.Now()
				
				if *workload == "tpcc_lite" {
					tx, err := pool.Begin(ctx)
					if err == nil {
						id := connIdx*opsPerConn + j
						opType := rand.Intn(4)
						if opType == 0 {
							// INSERT user
							_, _ = tx.Exec(ctx, "INSERT INTO users VALUES ($1, $2, $3, $4)", id, fmt.Sprintf("user_%d", id), rand.Intn(100), rand.Float64()*1000)
						} else if opType == 1 {
							// UPDATE
							updateID := rand.Intn(id + 1)
							_, _ = tx.Exec(ctx, "UPDATE users SET balance = balance + 10 WHERE id = $1", updateID)
						} else if opType == 2 {
							// SELECT
							selectID := rand.Intn(id + 1)
							rows, err := tx.Query(ctx, "SELECT * FROM users WHERE id = $1", selectID)
							if err == nil {
								rows.Close()
							}
						} else {
							// INSERT order
							_, _ = tx.Exec(ctx, "INSERT INTO orders VALUES ($1, $2, $3)", id, id, rand.Float64()*100)
						}
						_ = tx.Commit(ctx)
					}
				} else if *workload == "olap_join" {
					query := `
						SELECT u.age, SUM(o.amount), COUNT(o.id)
						FROM users u
						JOIN orders o ON u.id = o.user_id
						WHERE u.age > 20 AND u.balance * 1.5 < 5000 AND (o.amount + 10) / 2 > 50
						GROUP BY u.age
					`
					rows, err := pool.Query(ctx, query)
					if err == nil {
						rows.Close()
					}
				}
				workerTracker.Record(time.Since(startOp))
			}
			globalTracker.Merge(workerTracker)
		}(i)
	}

	wg.Wait()
	duration := time.Since(start)

	fmt.Printf("\n--- Benchmark Results ---\n")
	fmt.Printf("Workload:      %s\n", *workload)
	fmt.Printf("Total Ops:     %d\n", *rows)
	fmt.Printf("Connections:   %d\n", *conns)
	fmt.Printf("Total Time:    %v\n", duration)
	fmt.Printf("Throughput:    %.2f ops/sec\n", float64(*rows)/duration.Seconds())
	fmt.Printf("%s\n", globalTracker.Calculate().String())
}
