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
}
