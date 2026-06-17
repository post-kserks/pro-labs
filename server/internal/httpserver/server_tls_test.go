package httpserver

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func generateTLSCertFiles(t *testing.T) (certFile, keyFile string) {
	t.Helper()
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{Organization: []string{"Test"}},
		NotBefore:    time.Now(),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{"localhost"},
	}
	certDER, err := x509.CreateCertificate(rand.Reader, template, template, &priv.PublicKey, priv)
	if err != nil {
		t.Fatalf("create cert: %v", err)
	}
	dir := t.TempDir()
	certFile = filepath.Join(dir, "cert.pem")
	keyFile = filepath.Join(dir, "key.pem")
	certOut, _ := os.Create(certFile)
	defer certOut.Close()
	pem.Encode(certOut, &pem.Block{Type: "CERTIFICATE", Bytes: certDER})
	keyOut, _ := os.Create(keyFile)
	defer keyOut.Close()
	pem.Encode(keyOut, &pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(priv)})
	return certFile, keyFile
}

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("find free port: %v", err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

func TestHTTPTLSMinVersion(t *testing.T) {
	certFile, keyFile := generateTLSCertFiles(t)

	apiPort := freePort(t)
	monitorPort := freePort(t)

	srv := newTestServer(t, mustAuth(t, false, nil))
	srv.cfg.TLSCertFile = certFile
	srv.cfg.TLSKeyFile = keyFile
	srv.cfg.Port = apiPort
	srv.cfg.MonitorPort = monitorPort

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.Start(ctx)
	}()

	// Wait for server to start
	time.Sleep(300 * time.Millisecond)

	addr := fmt.Sprintf("127.0.0.1:%d", apiPort)

	baseTLS := &tls.Config{InsecureSkipVerify: true}

	// TLS 1.2 should succeed
	tlsCfg12 := baseTLS.Clone()
	tlsCfg12.MinVersion = tls.VersionTLS12
	tlsCfg12.MaxVersion = tls.VersionTLS12

	conn12, err := tls.DialWithDialer(&net.Dialer{Timeout: 2 * time.Second}, "tcp", addr, tlsCfg12)
	if err != nil {
		t.Fatalf("TLS 1.2 connection should succeed: %v", err)
	}
	conn12.Close()

	// TLS 1.1 should fail
	tlsCfg11 := baseTLS.Clone()
	tlsCfg11.MinVersion = tls.VersionTLS10
	tlsCfg11.MaxVersion = tls.VersionTLS11

	_, err = tls.DialWithDialer(&net.Dialer{Timeout: 2 * time.Second}, "tcp", addr, tlsCfg11)
	if err == nil {
		t.Fatal("TLS 1.1 connection should fail")
	}

	cancel()
	select {
	case <-errCh:
	case <-time.After(5 * time.Second):
		t.Fatal("server did not shut down in time")
	}
}

func TestHTTPTLSConfigApplied(t *testing.T) {
	certFile, keyFile := generateTLSCertFiles(t)

	apiPort := freePort(t)
	monitorPort := freePort(t)

	srv := newTestServer(t, mustAuth(t, false, nil))
	srv.cfg.TLSCertFile = certFile
	srv.cfg.TLSKeyFile = keyFile
	srv.cfg.Port = apiPort
	srv.cfg.MonitorPort = monitorPort

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.Start(ctx)
	}()

	time.Sleep(300 * time.Millisecond)

	addr := fmt.Sprintf("127.0.0.1:%d", apiPort)

	baseTLS := &tls.Config{InsecureSkipVerify: true}

	// TLS 1.3 should succeed
	tlsCfg13 := baseTLS.Clone()
	tlsCfg13.MinVersion = tls.VersionTLS13

	conn13, err := tls.DialWithDialer(&net.Dialer{Timeout: 2 * time.Second}, "tcp", addr, tlsCfg13)
	if err != nil {
		t.Fatalf("TLS 1.3 connection should succeed: %v", err)
	}
	conn13.Close()

	cancel()
	select {
	case <-errCh:
	case <-time.After(5 * time.Second):
		t.Fatal("server did not shut down in time")
	}
}

func TestHTTPTLSMonitorServerMinVersion(t *testing.T) {
	certFile, keyFile := generateTLSCertFiles(t)

	apiPort := freePort(t)
	monitorPort := freePort(t)

	srv := newTestServer(t, mustAuth(t, false, nil))
	srv.cfg.TLSCertFile = certFile
	srv.cfg.TLSKeyFile = keyFile
	srv.cfg.Port = apiPort
	srv.cfg.MonitorPort = monitorPort

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.Start(ctx)
	}()

	time.Sleep(300 * time.Millisecond)

	addr := fmt.Sprintf("127.0.0.1:%d", monitorPort)

	baseTLS := &tls.Config{InsecureSkipVerify: true}

	// TLS 1.2 should succeed on monitor port
	tlsCfg12 := baseTLS.Clone()
	tlsCfg12.MinVersion = tls.VersionTLS12
	tlsCfg12.MaxVersion = tls.VersionTLS12

	conn12, err := tls.DialWithDialer(&net.Dialer{Timeout: 2 * time.Second}, "tcp", addr, tlsCfg12)
	if err != nil {
		t.Fatalf("TLS 1.2 connection to monitor should succeed: %v", err)
	}
	conn12.Close()

	// TLS 1.1 should fail on monitor port
	tlsCfg11 := baseTLS.Clone()
	tlsCfg11.MinVersion = tls.VersionTLS10
	tlsCfg11.MaxVersion = tls.VersionTLS11

	_, err = tls.DialWithDialer(&net.Dialer{Timeout: 2 * time.Second}, "tcp", addr, tlsCfg11)
	if err == nil {
		t.Fatal("TLS 1.1 connection to monitor should fail")
	}

	cancel()
	select {
	case <-errCh:
	case <-time.After(5 * time.Second):
		t.Fatal("server did not shut down in time")
	}
}

func TestHTTPTLSWithoutConfig(t *testing.T) {
	apiPort := freePort(t)
	monitorPort := freePort(t)

	srv := newTestServer(t, mustAuth(t, false, nil))
	srv.cfg.TLSCertFile = ""
	srv.cfg.TLSKeyFile = ""
	srv.cfg.Port = apiPort
	srv.cfg.MonitorPort = monitorPort

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.Start(ctx)
	}()

	time.Sleep(300 * time.Millisecond)

	addr := fmt.Sprintf("127.0.0.1:%d", apiPort)

	// Plain TCP should work when TLS is not configured
	conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
	if err != nil {
		t.Fatalf("plain TCP connection should succeed: %v", err)
	}
	conn.Close()

	cancel()
	select {
	case <-errCh:
	case <-time.After(5 * time.Second):
		t.Fatal("server did not shut down in time")
	}
}
