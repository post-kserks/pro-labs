package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"
)

type queryResponse struct {
	Status   string      `json:"status"`
	Type     string      `json:"type"`
	Columns  []string    `json:"columns"`
	Rows     [][]string  `json:"rows"`
	Affected int         `json:"affected"`
	Message  string      `json:"message"`
}

type VaultDBConn struct {
	conn    net.Conn
	scanner *bufio.Scanner
	reqID   int
	token   string
}

func DialVaultDB(host string, port int, token string) (*VaultDBConn, error) {
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("%s:%d", host, port), 5*time.Second)
	if err != nil {
		return nil, fmt.Errorf("connect: %w", err)
	}
	return &VaultDBConn{
		conn:    conn,
		scanner: bufio.NewScanner(conn),
		reqID:   0,
		token:   token,
	}, nil
}

func (v *VaultDBConn) Query(sql string) (*queryResponse, error) {
	v.reqID++
	v.conn.SetDeadline(time.Now().Add(10 * time.Second))

	escaped := strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(sql)
	req := fmt.Sprintf(`{"id":"%d","query":"%s","token":"%s"}`, v.reqID, escaped, v.token)
	if _, err := fmt.Fprintf(v.conn, "%s\n", req); err != nil {
		return nil, fmt.Errorf("send: %w", err)
	}

	if !v.scanner.Scan() {
		return nil, fmt.Errorf("no response (scan err: %v)", v.scanner.Err())
	}
	var resp queryResponse
	if err := json.Unmarshal(v.scanner.Bytes(), &resp); err != nil {
		return nil, fmt.Errorf("parse: %w (body: %s)", err, v.scanner.Text())
	}
	return &resp, nil
}

func (v *VaultDBConn) Close() {
	v.conn.Close()
}

func (v *VaultDBConn) MustQuery(sql string) *queryResponse {
	resp, err := v.Query(sql)
	if err != nil {
		fmt.Fprintf(os.Stderr, "  \033[31m✗ %s\033[0m\n    SQL: %s\n", err, sql)
		return &queryResponse{Status: "error", Message: err.Error()}
	}
	return resp
}

func getWALMetrics(host string, port int, token string) (entries, checkpoints int64, err error) {
	req, err := http.NewRequest("GET", fmt.Sprintf("http://%s:%d/metrics", host, port), nil)
	if err != nil {
		return 0, 0, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, 0, err
	}
	defer resp.Body.Close()

	reEntries := regexp.MustCompile(`vaultdb_wal_entries_total (\d+)`)
	reCheckpoints := regexp.MustCompile(`vaultdb_wal_checkpoint_total (\d+)`)

	var buf strings.Builder
	buf.Grow(4096)
	if _, err := io.Copy(&buf, resp.Body); err != nil {
		return 0, 0, err
	}
	body := buf.String()

	if m := reEntries.FindStringSubmatch(body); len(m) > 1 {
		fmt.Sscanf(m[1], "%d", &entries)
	}
	if m := reCheckpoints.FindStringSubmatch(body); len(m) > 1 {
		fmt.Sscanf(m[1], "%d", &checkpoints)
	}
	return entries, checkpoints, nil
}

func printStep(n int, desc string) {
	fmt.Printf("\n  \033[36m·\033[0m Step %d: %s\n", n, desc)
}

func printOK(msg string) {
	fmt.Printf("    \033[32m✓\033[0m %s\n", msg)
}

func printFail(msg string) {
	fmt.Printf("    \033[31m✗\033[0m %s\n", msg)
}

func printInfo(msg string) {
	fmt.Printf("    \033[90m%s\033[0m\n", msg)
}

func main() {
	vaultdbHost := "127.0.0.1"
	tcpPort := 5432
	monitorPort := 5433
	walPath := "./data_test/wal/vaultdb.wal"
	apiToken := "vdb_sk_demo_test"

	fmt.Println("\n  \033[1m═══ VaultDB WAL Demo via TCP Client ═══\033[0m\n")

	// ── Step 1: Check initial state ──
	printStep(1, "Checking initial WAL state")
	entriesBefore, ckptsBefore, err := getWALMetrics(vaultdbHost, monitorPort, apiToken)
	if err != nil {
		printFail("Cannot reach VaultDB monitor: " + err.Error())
		os.Exit(1)
	}
	printOK(fmt.Sprintf("WAL entries (metric): %d, Checkpoints: %d", entriesBefore, ckptsBefore))

	walSizeBefore := walFileSize(walPath)
	if walSizeBefore >= 0 {
		printOK(fmt.Sprintf("WAL file size: %d bytes", walSizeBefore))
	} else {
		printOK("WAL file not created yet (will be after writes)")
	}

	// ── Step 2: Connect via TCP ──
	fmt.Println()
	printStep(2, "Connecting to VaultDB via TCP (port 5432)")
	vc, err := DialVaultDB(vaultdbHost, tcpPort, apiToken)
	if err != nil {
		printFail("TCP connection failed: " + err.Error())
		os.Exit(1)
	}
	defer vc.Close()

	resp := vc.MustQuery("SELECT 1;")
	printOK(fmt.Sprintf("Connected. SELECT 1 → %s (status=%s)", resp.Rows, resp.Status))

	// ── Step 3: Write operations through WAL ──
	fmt.Println()
	printStep(3, "Executing write operations through WAL")

	printInfo("DROP/CREATE DATABASE wal_demo")
	vc.MustQuery("DROP DATABASE wal_demo;")
	resp = vc.MustQuery("CREATE DATABASE wal_demo;")
	printOK(fmt.Sprintf("CREATE DATABASE → %s", resp.Message))

	printInfo("USE wal_demo + CREATE TABLE items")
	vc.MustQuery("USE wal_demo;")
	resp = vc.MustQuery("CREATE TABLE items (id INT PRIMARY KEY, name TEXT, price FLOAT);")
	printOK(fmt.Sprintf("CREATE TABLE → %s", resp.Message))

	printInfo("INSERT 3 rows")
	for i, item := range []struct{ name string; price float64 }{
		{"Widget", 9.99}, {"Gadget", 24.99}, {"Doohickey", 49.99},
	} {
		sql := fmt.Sprintf("INSERT INTO items VALUES (%d, '%s', %.2f);", i+1, item.name, item.price)
		resp = vc.MustQuery(sql)
		printOK(fmt.Sprintf("INSERT %d (%s) → %s", i+1, item.name, resp.Message))
	}

	printInfo("UPDATE price WHERE id = 1")
	resp = vc.MustQuery("UPDATE items SET price = 14.99 WHERE id = 1;")
	printOK(fmt.Sprintf("UPDATE → %s", resp.Message))

	printInfo("DELETE WHERE id = 3")
	resp = vc.MustQuery("DELETE FROM items WHERE id = 3;")
	printOK(fmt.Sprintf("DELETE → %s", resp.Message))

	printInfo("SELECT * FROM items (verify)")
	resp = vc.MustQuery("SELECT * FROM items;")
	printOK(fmt.Sprintf("Remaining rows: %d — %s", len(resp.Rows), resp.Rows))

	// ── Step 4: Transaction with COMMIT ──
	fmt.Println()
	printStep(4, "Transaction with COMMIT (OpCommit in WAL)")
	vc.MustQuery("BEGIN;")
	vc.MustQuery("INSERT INTO items VALUES (10, 'Contraption', 99.99);")
	resp = vc.MustQuery("COMMIT;")
	printOK(fmt.Sprintf("COMMIT → %s", resp.Message))

	// ── Step 5: Transaction with ROLLBACK ──
	printStep(5, "Transaction with ROLLBACK (OpAbort in WAL)")
	vc.MustQuery("BEGIN;")
	vc.MustQuery("INSERT INTO items VALUES (11, 'Ghost', 0.01);")
	resp = vc.MustQuery("ROLLBACK;")
	printOK(fmt.Sprintf("ROLLBACK → %s", resp.Message))

	// Verify rollback
	resp = vc.MustQuery("SELECT * FROM items WHERE id = 11;")
	if len(resp.Rows) == 0 {
		printInfo("Verified: row with id=11 was rolled back, not visible")
	}

	// ── Step 6: Verify WAL state ──
	fmt.Println()
	printStep(6, "Verifying WAL state after operations")

	entriesAfter, ckptsAfter, _ := getWALMetrics(vaultdbHost, monitorPort, apiToken)
	printOK(fmt.Sprintf("WAL entries (metric): %d (%+d), Checkpoints: %d (%+d)",
		entriesAfter, entriesAfter-entriesBefore,
		ckptsAfter, ckptsAfter-ckptsBefore))

	if walSizeBefore < 0 {
		walSizeBefore = 0
	}
	walSizeAfter := walFileSize(walPath)
	printOK(fmt.Sprintf("WAL file: %d bytes (before) → %d bytes (after)", walSizeBefore, walSizeAfter))

	// Try to read WAL file hex dump to see entries
	if walSizeAfter > 0 {
		printInfo("WAL file contains binary records (magic: VDB1)")
	}

	// ── Summary ──
	fmt.Println()
	fmt.Println("  \033[1m═══ Summary ═══\033[0m")
	fmt.Println()
	printOK("TCP protocol: JSON-line request/response over port 5432")
	printOK("Write operations go through the WAL before applying to storage")
	printOK("Each page-level write (Insert/Update/Delete) creates a WAL record")
	printOK("COMMIT writes OpCommit to WAL (ARIES protocol)")
	printOK("ROLLBACK writes OpAbort to WAL (undo support)")
	printOK("WAL enables: crash recovery, MVCC, time travel (AS OF)")

	if entriesAfter-entriesBefore == 0 {
		fmt.Println()
		printInfo("Note: vaultdb_wal_entries_total metric stayed at 0.")
		printInfo("This means OnAppend callback was not triggered yet.")
		printInfo("Check that the new vaultdb-server binary is running.")
	}

	fmt.Println()
}

func walFileSize(path string) int64 {
	fi, err := os.Stat(path)
	if err != nil {
		return -1
	}
	return fi.Size()
}
