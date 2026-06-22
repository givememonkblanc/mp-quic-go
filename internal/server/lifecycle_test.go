package server

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"io"
	"log/slog"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestServerNewWithValidConfig(t *testing.T) {
	certFile, keyFile := writeTestCertificateFiles(t)

	cfg := testServerConfig(certFile, keyFile)

	srv, err := New(cfg, testLogger())
	if err != nil {
		t.Fatalf("new server failed: %v", err)
	}

	if srv == nil {
		t.Fatal("expected non-nil server")
	}
}

func TestServerNewRejectsInvalidTransportParameters(t *testing.T) {
	certFile, keyFile := writeTestCertificateFiles(t)

	cfg := testServerConfig(certFile, keyFile)
	cfg.InitialMaxPathID = 4
	cfg.MaxPathID = 1

	_, err := New(cfg, testLogger())
	if err == nil {
		t.Fatal("expected invalid transport parameters to fail")
	}
}

func TestServerNewRejectsInvalidPathInterfaces(t *testing.T) {
	certFile, keyFile := writeTestCertificateFiles(t)

	cfg := testServerConfig(certFile, keyFile)
	cfg.PathInterfaces = "wlan0=0,wlan1=0"

	_, err := New(cfg, testLogger())
	if err == nil {
		t.Fatal("expected duplicate path interface mapping to fail")
	}
}

func TestServerStartFailsWithInvalidListenAddr(t *testing.T) {
	certFile, keyFile := writeTestCertificateFiles(t)

	cfg := testServerConfig(certFile, keyFile)
	cfg.ListenAddr = "not-a-valid-address"

	srv, err := New(cfg, testLogger())
	if err != nil {
		t.Fatalf("new server failed: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	err = srv.Start(ctx)
	if err == nil {
		t.Fatal("expected start with invalid listen address to fail")
	}
}

func TestServerStartFailsWithMissingCertificate(t *testing.T) {
	_, keyFile := writeTestCertificateFiles(t)

	cfg := testServerConfig("/tmp/mp-quic-missing-cert.pem", keyFile)

	srv, err := New(cfg, testLogger())
	if err != nil {
		t.Fatalf("new server failed: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	err = srv.Start(ctx)
	if err == nil {
		t.Fatal("expected start with missing certificate to fail")
	}
}

func TestServerStartFailsWithMissingKey(t *testing.T) {
	certFile, _ := writeTestCertificateFiles(t)

	cfg := testServerConfig(certFile, "/tmp/mp-quic-missing-key.pem")

	srv, err := New(cfg, testLogger())
	if err != nil {
		t.Fatalf("new server failed: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	err = srv.Start(ctx)
	if err == nil {
		t.Fatal("expected start with missing key to fail")
	}
}

func TestServerLifecycleStartAndCancel(t *testing.T) {
	certFile, keyFile := writeTestCertificateFiles(t)

	cfg := testServerConfig(certFile, keyFile)
	cfg.ListenAddr = freeUDPListenAddr(t)

	srv, err := New(cfg, testLogger())
	if err != nil {
		t.Fatalf("new server failed: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())

	errCh := make(chan error, 1)

	go func() {
		errCh <- srv.Start(ctx)
	}()

	// 서버가 UDP bind와 QUIC listener 생성까지 갈 시간을 조금 준다.
	time.Sleep(100 * time.Millisecond)

	cancel()

	select {
	case err := <-errCh:
		if err != nil &&
			!errors.Is(err, context.Canceled) &&
			!errors.Is(err, net.ErrClosed) {
			t.Fatalf("unexpected server shutdown error: %v", err)
		}

	case <-time.After(2 * time.Second):
		t.Fatal("server did not stop after context cancellation")
	}
}

func TestServerLifecycleStartTwiceFailsOrReturnsCleanly(t *testing.T) {
	certFile, keyFile := writeTestCertificateFiles(t)

	cfg := testServerConfig(certFile, keyFile)
	cfg.ListenAddr = freeUDPListenAddr(t)

	srv, err := New(cfg, testLogger())
	if err != nil {
		t.Fatalf("new server failed: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)

	go func() {
		errCh <- srv.Start(ctx)
	}()

	time.Sleep(100 * time.Millisecond)

	secondCtx, secondCancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer secondCancel()

	err = srv.Start(secondCtx)
	if err == nil {
		t.Fatal("expected second Start call to fail")
	}

	cancel()

	select {
	case <-errCh:
	case <-time.After(2 * time.Second):
		t.Fatal("server did not stop after cancellation")
	}
}

func TestServerLifecycleContextCanceledBeforeStart(t *testing.T) {
	certFile, keyFile := writeTestCertificateFiles(t)

	cfg := testServerConfig(certFile, keyFile)
	cfg.ListenAddr = freeUDPListenAddr(t)

	srv, err := New(cfg, testLogger())
	if err != nil {
		t.Fatalf("new server failed: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err = srv.Start(ctx)
	if err == nil {
		t.Fatal("expected Start with canceled context to fail")
	}
}

func testServerConfig(certFile, keyFile string) Config {
	return Config{
		ListenAddr:       "127.0.0.1:0",
		MaxIdleTimeout:   5 * time.Second,
		MaxStreamNum:     16,
		InitialMaxPathID: 1,
		MaxPathID:        4,
		ServerName:       "localhost",
		CertFile:         certFile,
		KeyFile:          keyFile,
		PathInterfaces:   "wlan0=0,wlan1=1",
		PathAddresses:    nil,
		EdgeEnvFile:      "",
	}
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func freeUDPListenAddr(t *testing.T) string {
	t.Helper()

	addr, err := net.ResolveUDPAddr("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("resolve udp addr failed: %v", err)
	}

	conn, err := net.ListenUDP("udp", addr)
	if err != nil {
		t.Fatalf("listen udp failed: %v", err)
	}
	defer conn.Close()

	return conn.LocalAddr().String()
}

func writeTestCertificateFiles(t *testing.T) (string, string) {
	t.Helper()

	dir := t.TempDir()

	certPEM, keyPEM := generateTestCertificatePEM(t)

	certFile := filepath.Join(dir, "server.crt")
	keyFile := filepath.Join(dir, "server.key")

	if err := os.WriteFile(certFile, certPEM, 0o600); err != nil {
		t.Fatalf("write cert file failed: %v", err)
	}

	if err := os.WriteFile(keyFile, keyPEM, 0o600); err != nil {
		t.Fatalf("write key file failed: %v", err)
	}

	return certFile, keyFile
}

func generateTestCertificatePEM(t *testing.T) ([]byte, []byte) {
	t.Helper()

	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate private key failed: %v", err)
	}

	serialNumber, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		t.Fatalf("generate serial number failed: %v", err)
	}

	template := x509.Certificate{
		SerialNumber: serialNumber,
		Subject: pkix.Name{
			CommonName: "localhost",
		},
		NotBefore: time.Now().Add(-time.Minute),
		NotAfter:  time.Now().Add(time.Hour),

		KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:              []string{"localhost"},
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1")},
		BasicConstraintsValid: true,
	}

	derBytes, err := x509.CreateCertificate(rand.Reader, &template, &template, &privateKey.PublicKey, privateKey)
	if err != nil {
		t.Fatalf("create certificate failed: %v", err)
	}

	certPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "CERTIFICATE",
		Bytes: derBytes,
	})

	keyBytes := x509.MarshalPKCS1PrivateKey(privateKey)

	keyPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: keyBytes,
	})

	return certPEM, keyPEM
}