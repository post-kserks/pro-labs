package pgwire

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"vaultdb/internal/auth"
)

func newTestAuthManager(t *testing.T) *auth.Manager {
	t.Helper()
	m, err := auth.New(true, nil, nil, 60, 100, 60)
	if err != nil {
		t.Fatalf("auth.New: %v", err)
	}
	return m
}

func pgErrCode(err error) string {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code
	}
	return ""
}

func TestPGWireAuth_ValidToken(t *testing.T) {
	am := newTestAuthManager(t)
	token := am.GenerateToken("test", "admin")
	_, port := startTestServerWith(t, func(s *PGWireServer) { s.AuthManager = am })

	ctx := context.Background()
	connStr := fmt.Sprintf("postgres://postgres:%s@127.0.0.1:%d/testdb?sslmode=disable", token, port)
	conn, err := pgx.Connect(ctx, connStr)
	if err != nil {
		t.Fatalf("connect with valid token failed: %v", err)
	}
	defer conn.Close(ctx)

	if _, err := conn.Exec(ctx, "CREATE TABLE t_auth (id INT);"); err != nil {
		t.Fatalf("CREATE TABLE failed: %v", err)
	}
}

func TestPGWireAuth_InvalidToken(t *testing.T) {
	am := newTestAuthManager(t)
	_ = am.GenerateToken("test", "admin")
	_, port := startTestServerWith(t, func(s *PGWireServer) { s.AuthManager = am })

	ctx := context.Background()
	for name, userinfo := range map[string]string{
		"wrong password": "postgres:not-a-token",
		"no password":    "postgres",
	} {
		connStr := fmt.Sprintf("postgres://%s@127.0.0.1:%d/testdb?sslmode=disable", userinfo, port)
		conn, err := pgx.Connect(ctx, connStr)
		if err == nil {
			_ = conn.Close(ctx)
			t.Fatalf("%s: expected authentication failure, got success", name)
		}
		if code := pgErrCode(err); code != "28P01" {
			t.Errorf("%s: expected SQLSTATE 28P01, got %q (err=%v)", name, code, err)
		}
	}
}

func TestPGWireAuth_RevokedToken(t *testing.T) {
	am := newTestAuthManager(t)
	token := am.GenerateToken("test", "admin")
	am.RevokeToken(token)
	_, port := startTestServerWith(t, func(s *PGWireServer) { s.AuthManager = am })

	ctx := context.Background()
	connStr := fmt.Sprintf("postgres://postgres:%s@127.0.0.1:%d/testdb?sslmode=disable", token, port)
	conn, err := pgx.Connect(ctx, connStr)
	if err == nil {
		_ = conn.Close(ctx)
		t.Fatal("expected revoked token to be rejected")
	}
	if code := pgErrCode(err); code != "28P01" {
		t.Errorf("expected SQLSTATE 28P01, got %q (err=%v)", code, err)
	}
}

func TestPGWireAuth_RBACAppliesToToken(t *testing.T) {
	am := newTestAuthManager(t)
	token := am.GenerateToken("ro", "reader")
	_, port := startTestServerWith(t, func(s *PGWireServer) { s.AuthManager = am })

	ctx := context.Background()
	connStr := fmt.Sprintf("postgres://postgres:%s@127.0.0.1:%d/testdb?sslmode=disable", token, port)
	conn, err := pgx.Connect(ctx, connStr)
	if err != nil {
		t.Fatalf("connect failed: %v", err)
	}
	defer conn.Close(ctx)

	_, err = conn.Exec(ctx, "CREATE TABLE t_denied (id INT);", pgx.QueryExecModeSimpleProtocol)
	if err == nil || !strings.Contains(err.Error(), "permission denied") {
		t.Fatalf("expected permission denied for reader role, got %v", err)
	}
}

func TestPGWire_ConnectionLimit(t *testing.T) {
	_, port := startTestServerWith(t, func(s *PGWireServer) { s.MaxConnections = 1 })

	ctx := context.Background()
	connStr := fmt.Sprintf("postgres://postgres@127.0.0.1:%d/testdb?sslmode=disable", port)
	first, err := pgx.Connect(ctx, connStr)
	if err != nil {
		t.Fatalf("first connection failed: %v", err)
	}

	second, err := pgx.Connect(ctx, connStr)
	if err == nil {
		_ = second.Close(ctx)
		t.Fatal("expected second connection to be rejected")
	}
	if code := pgErrCode(err); code != "53300" {
		t.Errorf("expected SQLSTATE 53300, got %q (err=%v)", code, err)
	}

	// After closing the first connection a slot becomes free again.
	_ = first.Close(ctx)
	deadline := time.Now().Add(5 * time.Second)
	for {
		c, err := pgx.Connect(ctx, connStr)
		if err == nil {
			_ = c.Close(ctx)
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("connection slot was not released: %v", err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestPGWire_HandshakeTimeout(t *testing.T) {
	_, port := startTestServerWith(t, func(s *PGWireServer) { s.HandshakeTimeout = 200 * time.Millisecond })

	conn, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	// Send nothing; the server must close the connection after the timeout.
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, err = io.ReadAll(conn)
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		t.Fatal("server did not close idle unauthenticated connection")
	}
}

func selfSignedTLSConfig(t *testing.T) *tls.Config {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "127.0.0.1"},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return &tls.Config{
		Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}},
		MinVersion:   tls.VersionTLS12,
	}
}

func TestPGWire_TLSWithAuth(t *testing.T) {
	am := newTestAuthManager(t)
	token := am.GenerateToken("test", "admin")
	_, port := startTestServerWith(t, func(s *PGWireServer) {
		s.AuthManager = am
		s.TLSConfig = selfSignedTLSConfig(t)
	})

	ctx := context.Background()
	connStr := fmt.Sprintf("postgres://postgres:%s@127.0.0.1:%d/testdb?sslmode=require", token, port)
	conn, err := pgx.Connect(ctx, connStr)
	if err != nil {
		t.Fatalf("TLS connect failed: %v", err)
	}
	defer conn.Close(ctx)

	if _, ok := conn.PgConn().Conn().(*tls.Conn); !ok {
		t.Fatalf("expected TLS connection, got %T", conn.PgConn().Conn())
	}
	if _, err := conn.Exec(ctx, "CREATE TABLE t_tls (id INT);"); err != nil {
		t.Fatalf("CREATE TABLE over TLS failed: %v", err)
	}
}
