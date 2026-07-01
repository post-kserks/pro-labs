package tls

import (
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"net"
	"os"
	"path/filepath"
	"testing"
)

func TestGenerateSelfSignedCert(t *testing.T) {
	cert, err := GenerateSelfSignedCert("localhost")
	if err != nil {
		t.Fatalf("GenerateSelfSignedCert(\"localhost\") error: %v", err)
	}
	if cert == nil {
		t.Fatal("expected non-nil certificate")
	}
	if len(cert.Certificate) == 0 {
		t.Fatal("expected at least one certificate in chain")
	}
}

func TestGenerateSelfSignedCertIP(t *testing.T) {
	cert, err := GenerateSelfSignedCert("127.0.0.1")
	if err != nil {
		t.Fatalf("GenerateSelfSignedCert(\"127.0.0.1\") error: %v", err)
	}
	if cert == nil {
		t.Fatal("expected non-nil certificate")
	}

	x509Cert, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		t.Fatalf("x509.ParseCertificate error: %v", err)
	}
	if len(x509Cert.IPAddresses) != 1 {
		t.Fatalf("expected 1 IP SAN, got %d", len(x509Cert.IPAddresses))
	}
	expected := net.ParseIP("127.0.0.1")
	if !x509Cert.IPAddresses[0].Equal(expected) {
		t.Errorf("expected IP SAN %v, got %v", expected, x509Cert.IPAddresses[0])
	}
}

func TestSaveCertToFile(t *testing.T) {
	cert, err := GenerateSelfSignedCert("localhost")
	if err != nil {
		t.Fatalf("GenerateSelfSignedCert error: %v", err)
	}

	pk, ok := cert.PrivateKey.(*rsa.PrivateKey)
	if !ok {
		t.Fatal("expected RSA private key")
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Certificate[0]})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(pk)})

	dir := t.TempDir()
	certFile := filepath.Join(dir, "cert.pem")
	keyFile := filepath.Join(dir, "key.pem")

	if err := SaveCertToFile(certPEM, keyPEM, certFile, keyFile); err != nil {
		t.Fatalf("SaveCertToFile error: %v", err)
	}

	info, err := os.Stat(certFile)
	if err != nil {
		t.Fatalf("cert file not created: %v", err)
	}
	if info.Size() == 0 {
		t.Error("cert file is empty")
	}

	info, err = os.Stat(keyFile)
	if err != nil {
		t.Fatalf("key file not created: %v", err)
	}
	if info.Size() == 0 {
		t.Error("key file is empty")
	}
}

func TestLoadTLSConfig(t *testing.T) {
	cert, err := GenerateSelfSignedCert("localhost")
	if err != nil {
		t.Fatalf("GenerateSelfSignedCert error: %v", err)
	}

	pk, ok := cert.PrivateKey.(*rsa.PrivateKey)
	if !ok {
		t.Fatal("expected RSA private key")
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Certificate[0]})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(pk)})

	dir := t.TempDir()
	certFile := filepath.Join(dir, "cert.pem")
	keyFile := filepath.Join(dir, "key.pem")

	if err := SaveCertToFile(certPEM, keyPEM, certFile, keyFile); err != nil {
		t.Fatalf("SaveCertToFile error: %v", err)
	}

	tlsConfig, err := LoadTLSConfig(certFile, keyFile)
	if err != nil {
		t.Fatalf("LoadTLSConfig error: %v", err)
	}
	if tlsConfig == nil {
		t.Fatal("expected non-nil TLS config")
	}
	if len(tlsConfig.Certificates) != 1 {
		t.Fatalf("expected 1 certificate, got %d", len(tlsConfig.Certificates))
	}
	if tlsConfig.MinVersion != tls.VersionTLS12 {
		t.Errorf("expected MinVersion TLS 1.2, got %v", tlsConfig.MinVersion)
	}
	if len(tlsConfig.CipherSuites) == 0 {
		t.Error("expected non-empty cipher suites")
	}
	if len(tlsConfig.CurvePreferences) == 0 {
		t.Error("expected non-empty curve preferences")
	}
}

func TestLoadMTLSConfig(t *testing.T) {
	// Generate server cert
	serverCert, err := GenerateSelfSignedCert("localhost")
	if err != nil {
		t.Fatalf("GenerateSelfSignedCert error: %v", err)
	}

	pk, ok := serverCert.PrivateKey.(*rsa.PrivateKey)
	if !ok {
		t.Fatal("expected RSA private key")
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: serverCert.Certificate[0]})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(pk)})

	// Generate CA cert
	caCert, err := GenerateSelfSignedCert("ca")
	if err != nil {
		t.Fatalf("GenerateSelfSignedCert CA error: %v", err)
	}
	caCertPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caCert.Certificate[0]})

	dir := t.TempDir()
	certFile := filepath.Join(dir, "server.pem")
	keyFile := filepath.Join(dir, "server-key.pem")
	caFile := filepath.Join(dir, "ca.pem")

	if err := SaveCertToFile(certPEM, keyPEM, certFile, keyFile); err != nil {
		t.Fatalf("SaveCertToFile error: %v", err)
	}
	if err := os.WriteFile(caFile, caCertPEM, 0640); err != nil {
		t.Fatalf("WriteFile ca error: %v", err)
	}

	mtlsConfig, err := LoadMTLSConfig(certFile, keyFile, caFile)
	if err != nil {
		t.Fatalf("LoadMTLSConfig error: %v", err)
	}
	if mtlsConfig == nil {
		t.Fatal("expected non-nil mTLS config")
	}
	if mtlsConfig.ClientAuth != tls.RequireAndVerifyClientCert {
		t.Errorf("expected RequireAndVerifyClientCert, got %v", mtlsConfig.ClientAuth)
	}
	if mtlsConfig.ClientCAs == nil {
		t.Fatal("expected non-nil ClientCAs pool")
	}
	if len(mtlsConfig.Certificates) != 1 {
		t.Fatalf("expected 1 certificate, got %d", len(mtlsConfig.Certificates))
	}
	if mtlsConfig.MinVersion != tls.VersionTLS12 {
		t.Errorf("expected MinVersion TLS 1.2, got %v", mtlsConfig.MinVersion)
	}
}

func TestLoadTLSConfigInvalidPaths(t *testing.T) {
	_, err := LoadTLSConfig("/nonexistent/cert.pem", "/nonexistent/key.pem")
	if err == nil {
		t.Fatal("expected error for nonexistent paths")
	}
}

func TestLoadTLSConfigCorruptFiles(t *testing.T) {
	dir := t.TempDir()
	certFile := filepath.Join(dir, "bad.pem")
	keyFile := filepath.Join(dir, "bad-key.pem")

	if err := os.WriteFile(certFile, []byte("not a real cert"), 0640); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyFile, []byte("not a real key"), 0640); err != nil {
		t.Fatal(err)
	}

	_, err := LoadTLSConfig(certFile, keyFile)
	if err == nil {
		t.Fatal("expected error for corrupt PEM files")
	}
}

func TestLoadMTLSConfigInvalidCAFile(t *testing.T) {
	serverCert, err := GenerateSelfSignedCert("localhost")
	if err != nil {
		t.Fatalf("GenerateSelfSignedCert error: %v", err)
	}

	pk, ok := serverCert.PrivateKey.(*rsa.PrivateKey)
	if !ok {
		t.Fatal("expected RSA private key")
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: serverCert.Certificate[0]})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(pk)})

	dir := t.TempDir()
	certFile := filepath.Join(dir, "server.pem")
	keyFile := filepath.Join(dir, "server-key.pem")
	caFile := filepath.Join(dir, "bad-ca.pem")

	if err := SaveCertToFile(certPEM, keyPEM, certFile, keyFile); err != nil {
		t.Fatalf("SaveCertToFile error: %v", err)
	}
	if err := os.WriteFile(caFile, []byte("garbage"), 0640); err != nil {
		t.Fatal(err)
	}

	_, err = LoadMTLSConfig(certFile, keyFile, caFile)
	if err == nil {
		t.Fatal("expected error for invalid CA PEM")
	}
}

func TestLoadMTLSConfigMissingCAFile(t *testing.T) {
	serverCert, err := GenerateSelfSignedCert("localhost")
	if err != nil {
		t.Fatalf("GenerateSelfSignedCert error: %v", err)
	}

	pk, ok := serverCert.PrivateKey.(*rsa.PrivateKey)
	if !ok {
		t.Fatal("expected RSA private key")
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: serverCert.Certificate[0]})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(pk)})

	dir := t.TempDir()
	certFile := filepath.Join(dir, "server.pem")
	keyFile := filepath.Join(dir, "server-key.pem")

	if err := SaveCertToFile(certPEM, keyPEM, certFile, keyFile); err != nil {
		t.Fatalf("SaveCertToFile error: %v", err)
	}

	_, err = LoadMTLSConfig(certFile, keyFile, "/nonexistent/ca.pem")
	if err == nil {
		t.Fatal("expected error for missing CA file")
	}
}

func TestLoadMTLSConfigMissingKeyFile(t *testing.T) {
	serverCert, err := GenerateSelfSignedCert("localhost")
	if err != nil {
		t.Fatalf("GenerateSelfSignedCert error: %v", err)
	}

	pk, ok := serverCert.PrivateKey.(*rsa.PrivateKey)
	if !ok {
		t.Fatal("expected RSA private key")
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: serverCert.Certificate[0]})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(pk)})

	dir := t.TempDir()
	certFile := filepath.Join(dir, "server.pem")
	caFile := filepath.Join(dir, "ca.pem")

	// Write cert as CA for this test
	if err := os.WriteFile(certFile, certPEM, 0640); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(caFile, certPEM, 0640); err != nil {
		t.Fatal(err)
	}
	_ = keyPEM

	_, err = LoadMTLSConfig(certFile, "/nonexistent/key.pem", caFile)
	if err == nil {
		t.Fatal("expected error for missing key file")
	}
}

func TestGenerateSelfSignedCertEmptyHost(t *testing.T) {
	cert, err := GenerateSelfSignedCert("")
	if err != nil {
		t.Fatalf("GenerateSelfSignedCert(\"\") error: %v", err)
	}
	if cert == nil {
		t.Fatal("expected non-nil certificate")
	}

	x509Cert, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		t.Fatalf("x509.ParseCertificate error: %v", err)
	}
	if len(x509Cert.DNSNames) != 0 || len(x509Cert.IPAddresses) != 0 {
		t.Error("expected no SANs for empty host")
	}
}
