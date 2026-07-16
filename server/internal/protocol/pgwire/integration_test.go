package pgwire

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"testing"

	"github.com/jackc/pgx/v5"

	"vaultdb/internal/core/executor"
	"vaultdb/internal/core/metrics"
	"vaultdb/internal/core/parser"
	"vaultdb/internal/core/storage"
	"vaultdb/internal/core/txmanager"
)

func startTestServer(t *testing.T) (*PGWireServer, int) {
	t.Helper()
	dir := t.TempDir()
	txm := txmanager.NewManager()
	store, err := storage.NewPageStorageEngine(dir, nil, txm)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	// Create testdb database in storage engine
	sess := executor.NewSession(store, metrics.New(), txm, executor.NewBroadcaster())
	defer sess.Close()
	createDbStmt, err := parser.Parse("CREATE DATABASE testdb;")
	if err != nil {
		t.Fatalf("failed to parse CREATE DATABASE: %v", err)
	}
	if _, err := sess.Execute(createDbStmt); err != nil {
		t.Fatalf("failed to create testdb database: %v", err)
	}

	srv := NewServer(
		"127.0.0.1:0",
		store,
		metrics.New(),
		txm,
		executor.NewBroadcaster(),
		nil,
		nil,
		nil,
	)

	ctx := context.Background()
	if err := srv.Start(ctx); err != nil {
		t.Fatalf("failed to start server: %v", err)
	}
	t.Cleanup(func() { srv.Stop() })

	port := srv.listener.Addr().(*net.TCPAddr).Port
	return srv, port
}

func TestPGWireIntegration_ExtendedQuery(t *testing.T) {
	_, port := startTestServer(t)

	ctx := context.Background()
	connStr := fmt.Sprintf("postgres://postgres@127.0.0.1:%d/testdb?sslmode=disable", port)
	conn, err := pgx.Connect(ctx, connStr)
	if err != nil {
		t.Fatalf("failed to connect via pgx: %v", err)
	}
	defer conn.Close(ctx)

	// 1. Create table (DDL)
	_, err = conn.Exec(ctx, "CREATE TABLE users (id INT, name TEXT, score FLOAT, active BOOL);")
	if err != nil {
		t.Fatalf("CREATE TABLE failed: %v", err)
	}

	// 2. Insert rows using parameters (DML - Parse/Bind/Execute)
	_, err = conn.Exec(ctx, "INSERT INTO users (id, name, score, active) VALUES ($1, $2, $3, $4);", 1, "Alice", 99.5, true)
	if err != nil {
		t.Fatalf("INSERT 1 failed: %v", err)
	}
	_, err = conn.Exec(ctx, "INSERT INTO users (id, name, score, active) VALUES ($1, $2, $3, $4);", 2, "Bob", 78.2, false)
	if err != nil {
		t.Fatalf("INSERT 2 failed: %v", err)
	}

	// 3. Update row using parameters (DML)
	cmdTag, err := conn.Exec(ctx, "UPDATE users SET score = $1 WHERE id = $2;", 85.0, 2)
	if err != nil {
		t.Fatalf("UPDATE failed: %v", err)
	}
	if cmdTag.RowsAffected() != 1 {
		t.Errorf("expected 1 row affected by UPDATE, got %d", cmdTag.RowsAffected())
	}

	// 4. Select row with parameters (DQL)
	var id int
	var name string
	var score float64
	var active bool

	err = conn.QueryRow(ctx, "SELECT id, name, score, active FROM users WHERE id = $1;", 2).Scan(&id, &name, &score, &active)
	if err != nil {
		t.Fatalf("SELECT Row failed: %v", err)
	}

	if id != 2 || name != "Bob" || score != 85.0 || active != false {
		t.Errorf("SELECT returned wrong values: id=%d, name=%q, score=%f, active=%t", id, name, score, active)
	}

	// 5. Delete row with parameters (DML)
	cmdTag, err = conn.Exec(ctx, "DELETE FROM users WHERE id = $1;", 1)
	if err != nil {
		t.Fatalf("DELETE failed: %v", err)
	}
	if cmdTag.RowsAffected() != 1 {
		t.Errorf("expected 1 row affected by DELETE, got %d", cmdTag.RowsAffected())
	}

	// Verify row was deleted
	var count int
	err = conn.QueryRow(ctx, "SELECT COUNT(*) FROM users;").Scan(&count)
	if err != nil {
		t.Fatalf("SELECT COUNT failed: %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1 row remaining, got %d", count)
	}
}

func TestPGWireIntegration_SimpleQuery(t *testing.T) {
	_, port := startTestServer(t)

	conn, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		t.Fatalf("failed to connect via raw TCP: %v", err)
	}
	defer conn.Close()

	// 1. Send StartupMessage
	startupPayload := &pgBuffer{}
	startupPayload.writeInt32(196608) // protocol version 3.0
	startupPayload.writeString("user")
	startupPayload.writeString("postgres")
	startupPayload.writeString("database")
	startupPayload.writeString("testdb")
	startupPayload.writeByte(0)

	length := int32(len(startupPayload.buf) + 4)
	var lengthBuf [4]byte
	binary.BigEndian.PutUint32(lengthBuf[:], uint32(length))

	_, _ = conn.Write(lengthBuf[:])
	_, _ = conn.Write(startupPayload.buf)

	// Consume handshake response until ReadyForQuery ('Z')
	for {
		var typeByte [1]byte
		if _, err := io.ReadFull(conn, typeByte[:]); err != nil {
			t.Fatalf("failed to read handshake response type: %v", err)
		}
		var lenBuf [4]byte
		if _, err := io.ReadFull(conn, lenBuf[:]); err != nil {
			t.Fatalf("failed to read handshake response len: %v", err)
		}
		msgLen := binary.BigEndian.Uint32(lenBuf[:])
		payload := make([]byte, msgLen-4)
		if _, err := io.ReadFull(conn, payload); err != nil {
			t.Fatalf("failed to read handshake response payload: %v", err)
		}

		if typeByte[0] == 'Z' {
			break
		}
	}

	// 2. Send Simple Query ('Q')
	query := "CREATE TABLE items (id INT, price FLOAT);"
	qPayload := append([]byte(query), 0)
	_ = WriteMessage(conn, 'Q', qPayload)

	// Read responses. Expecting CommandComplete ('C') and ReadyForQuery ('Z')
	var hasCommandComplete bool
	for {
		var typeByte [1]byte
		if _, err := io.ReadFull(conn, typeByte[:]); err != nil {
			t.Fatalf("failed to read query response type: %v", err)
		}
		var lenBuf [4]byte
		if _, err := io.ReadFull(conn, lenBuf[:]); err != nil {
			t.Fatalf("failed to read query response len: %v", err)
		}
		msgLen := binary.BigEndian.Uint32(lenBuf[:])
		payload := make([]byte, msgLen-4)
		if _, err := io.ReadFull(conn, payload); err != nil {
			t.Fatalf("failed to read query response payload: %v", err)
		}

		if typeByte[0] == 'C' {
			tag := string(payload[:len(payload)-1]) // strip null
			if tag != "CREATE TABLE" {
				t.Errorf("expected CommandComplete tag 'CREATE TABLE', got %q", tag)
			}
			hasCommandComplete = true
		}
		if typeByte[0] == 'Z' {
			break
		}
	}

	if !hasCommandComplete {
		t.Error("never received CommandComplete message")
	}
}
