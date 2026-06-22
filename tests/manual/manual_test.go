//go:build manual
// +build manual

package manual

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	quic "github.com/quic-go/quic-go"
)

const (
	manualTestProto   = "mp-quic-manual-test"
	manualTestTimeout = 5 * time.Second
)

// TestCase1_ServerStartup verifies that a QUIC server can start with
// generated TLS credentials and MP-QUIC config.
func TestCase1_ServerStartup(t *testing.T) {
	serverUDP := listenManualUDP(t)
	defer serverUDP.Close()

	tlsConfig := manualServerTLSConfig(t)

	listener, err := quic.ListenEarly(serverUDP, tlsConfig, &quic.Config{
		InitialMaxPathID: 1,
		MaxIdleTimeout:   10 * time.Second,
	})
	if err != nil {
		t.Fatalf("failed to create QUIC server: %v", err)
	}
	defer listener.Close()

	t.Logf("server started successfully on %s", listener.Addr())
}

// TestCase2_ClientConnectivity verifies that a client can connect to the
// manually started local QUIC server.
func TestCase2_ClientConnectivity(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), manualTestTimeout)
	defer cancel()

	serverUDP := listenManualUDP(t)
	defer serverUDP.Close()

	listener, err := quic.ListenEarly(serverUDP, manualServerTLSConfig(t), &quic.Config{
		InitialMaxPathID: 1,
		MaxIdleTimeout:   10 * time.Second,
	})
	if err != nil {
		t.Fatalf("failed to create QUIC server: %v", err)
	}
	defer listener.Close()

	serverDone := make(chan error, 1)

	go func() {
		conn, err := listener.Accept(ctx)
		if err != nil {
			serverDone <- fmt.Errorf("server accept failed: %w", err)
			return
		}

		t.Logf("server accepted client: %s", conn.RemoteAddr())

		if err := conn.CloseWithError(0, "manual connectivity test done"); err != nil {
			serverDone <- fmt.Errorf("server close failed: %w", err)
			return
		}

		serverDone <- nil
	}()

	clientUDP := listenManualUDP(t)
	defer clientUDP.Close()

	conn, err := quic.DialEarly(ctx, clientUDP, listener.Addr(), manualClientTLSConfig(), &quic.Config{
		InitialMaxPathID: 1,
		MaxIdleTimeout:   10 * time.Second,
	})
	if err != nil {
		t.Fatalf("client connection failed: %v", err)
	}
	defer conn.CloseWithError(0, "client done")

	t.Logf("client connected to server: local=%s remote=%s", clientUDP.LocalAddr(), conn.RemoteAddr())

	select {
	case err := <-serverDone:
		if err != nil {
			t.Fatalf("server failed: %v", err)
		}

	case <-ctx.Done():
		t.Fatalf("timeout waiting for server accept: %v", ctx.Err())
	}
}

// TestCase3_MultiStream verifies that multiple streams can be opened on a
// single QUIC connection and echoed independently.
func TestCase3_MultiStream(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), manualTestTimeout)
	defer cancel()

	const streamCount = 3

	serverAddr, serverDone, cleanupServer := startManualEchoServer(t, ctx, streamCount)
	defer cleanupServer()

	clientUDP := listenManualUDP(t)
	defer clientUDP.Close()

	conn, err := quic.DialEarly(ctx, clientUDP, serverAddr, manualClientTLSConfig(), &quic.Config{
		InitialMaxPathID: 1,
		MaxIdleTimeout:   10 * time.Second,
	})
	if err != nil {
		t.Fatalf("client connection failed: %v", err)
	}
	defer conn.CloseWithError(0, "client done")

	for i := 0; i < streamCount; i++ {
		payload := []byte(fmt.Sprintf("stream-%d-data", i))

		if err := sendManualEchoPayload(ctx, conn, payload); err != nil {
			t.Fatalf("stream %d echo failed: %v", i, err)
		}
	}

	select {
	case err := <-serverDone:
		if err != nil {
			t.Fatalf("server failed: %v", err)
		}

	case <-ctx.Done():
		t.Fatalf("timeout waiting for multi-stream server: %v", ctx.Err())
	}

	t.Log("multiple streams work correctly")
}

// TestCase4_ParallelStreams verifies that concurrent streams do not corrupt
// payload delivery.
func TestCase4_ParallelStreams(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), manualTestTimeout)
	defer cancel()

	const streamCount = 8

	serverAddr, serverDone, cleanupServer := startManualEchoServer(t, ctx, streamCount)
	defer cleanupServer()

	clientUDP := listenManualUDP(t)
	defer clientUDP.Close()

	conn, err := quic.DialEarly(ctx, clientUDP, serverAddr, manualClientTLSConfig(), &quic.Config{
		InitialMaxPathID: 1,
		MaxIdleTimeout:   10 * time.Second,
	})
	if err != nil {
		t.Fatalf("client connection failed: %v", err)
	}
	defer conn.CloseWithError(0, "client done")

	var wg sync.WaitGroup
	errCh := make(chan error, streamCount)

	for i := 0; i < streamCount; i++ {
		wg.Add(1)

		go func(index int) {
			defer wg.Done()

			payload := []byte(fmt.Sprintf("parallel-stream-%02d-payload", index))

			if err := sendManualEchoPayload(ctx, conn, payload); err != nil {
				errCh <- fmt.Errorf("parallel stream %d failed: %w", index, err)
			}
		}(i)
	}

	wg.Wait()
	close(errCh)

	for err := range errCh {
		if err != nil {
			t.Fatal(err)
		}
	}

	select {
	case err := <-serverDone:
		if err != nil {
			t.Fatalf("server failed: %v", err)
		}

	case <-ctx.Done():
		t.Fatalf("timeout waiting for parallel stream server: %v", ctx.Err())
	}

	t.Log("parallel streams work correctly")
}

// TestCase5_CertFiles verifies that generated test certificates can be written
// to disk and loaded back as a TLS key pair.
func TestCase5_CertFiles(t *testing.T) {
	certPEM, keyPEM := generateManualTestCert(t)

	dir := t.TempDir()

	certPath := filepath.Join(dir, "cert.pem")
	keyPath := filepath.Join(dir, "key.pem")

	if err := os.WriteFile(certPath, certPEM, 0644); err != nil {
		t.Fatalf("write cert file failed: %v", err)
	}

	if err := os.WriteFile(keyPath, keyPEM, 0600); err != nil {
		t.Fatalf("write key file failed: %v", err)
	}

	cert, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		t.Fatalf("load key pair from files failed: %v", err)
	}

	if len(cert.Certificate) == 0 {
		t.Fatal("expected loaded certificate chain to be non-empty")
	}

	t.Logf("certificate files generated and loaded: cert=%s key=%s", certPath, keyPath)
}

func startManualEchoServer(
	t *testing.T,
	ctx context.Context,
	streamCount int,
) (net.Addr, <-chan error, func()) {
	t.Helper()

	serverUDP := listenManualUDP(t)

	listener, err := quic.ListenEarly(serverUDP, manualServerTLSConfig(t), &quic.Config{
		InitialMaxPathID: 1,
		MaxIdleTimeout:   10 * time.Second,
	})
	if err != nil {
		_ = serverUDP.Close()
		t.Fatalf("failed to create QUIC server: %v", err)
	}

	done := make(chan error, 1)

	go func() {
		conn, err := listener.Accept(ctx)
		if err != nil {
			done <- fmt.Errorf("server accept failed: %w", err)
			return
		}
		defer conn.CloseWithError(0, "server done")

		var wg sync.WaitGroup
		errCh := make(chan error, streamCount)

		for i := 0; i < streamCount; i++ {
			stream, err := conn.AcceptStream(ctx)
			if err != nil {
				done <- fmt.Errorf("accept stream %d failed: %w", i, err)
				return
			}

			wg.Add(1)

			go func(index int, stream quic.Stream) {
				defer wg.Done()

				if err := echoManualStream(stream); err != nil {
					errCh <- fmt.Errorf("echo stream %d failed: %w", index, err)
				}
			}(i, stream)
		}

		wg.Wait()
		close(errCh)

		for err := range errCh {
			if err != nil {
				done <- err
				return
			}
		}

		done <- nil
	}()

	cleanup := func() {
		_ = listener.Close()
		_ = serverUDP.Close()
	}

	return listener.Addr(), done, cleanup
}

func echoManualStream(stream quic.Stream) error {
	payload, err := io.ReadAll(stream)
	if err != nil {
		return fmt.Errorf("read stream payload failed: %w", err)
	}

	if err := writeFullManual(stream, payload); err != nil {
		return fmt.Errorf("write echo failed: %w", err)
	}

	if err := stream.Close(); err != nil {
		return fmt.Errorf("close stream failed: %w", err)
	}

	return nil
}

func sendManualEchoPayload(
	ctx context.Context,
	conn quic.EarlyConnection,
	payload []byte,
) error {
	stream, err := conn.OpenStreamSync(ctx)
	if err != nil {
		return fmt.Errorf("open stream failed: %w", err)
	}

	if err := writeFullManual(stream, payload); err != nil {
		return fmt.Errorf("write payload failed: %w", err)
	}

	if err := stream.Close(); err != nil {
		return fmt.Errorf("close client write side failed: %w", err)
	}

	echo, err := io.ReadAll(stream)
	if err != nil {
		return fmt.Errorf("read echo failed: %w", err)
	}

	if !bytes.Equal(echo, payload) {
		return fmt.Errorf("echo mismatch: got=%q want=%q", echo, payload)
	}

	return nil
}

func writeFullManual(w io.Writer, data []byte) error {
	for len(data) > 0 {
		n, err := w.Write(data)
		if err != nil {
			return err
		}

		if n == 0 {
			return io.ErrShortWrite
		}

		data = data[n:]
	}

	return nil
}

func listenManualUDP(t *testing.T) *net.UDPConn {
	t.Helper()

	conn, err := net.ListenUDP("udp4", &net.UDPAddr{
		IP:   net.IPv4(127, 0, 0, 1),
		Port: 0,
	})
	if err != nil {
		t.Fatalf("listen UDP failed: %v", err)
	}

	return conn
}

func manualServerTLSConfig(t *testing.T) *tls.Config {
	t.Helper()

	certPEM, keyPEM := generateManualTestCert(t)

	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		t.Fatalf("parse key pair failed: %v", err)
	}

	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		NextProtos: []string{
			manualTestProto,
		},
		MinVersion: tls.VersionTLS13,
	}
}

func manualClientTLSConfig() *tls.Config {
	return &tls.Config{
		InsecureSkipVerify: true,
		NextProtos: []string{
			manualTestProto,
		},
		ServerName: "localhost",
		MinVersion: tls.VersionTLS13,
	}
}

func generateManualTestCert(t testing.TB) (certPEM, keyPEM []byte) {
	t.Helper()

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate rsa key failed: %v", err)
	}

	serialNumber, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		t.Fatalf("generate serial number failed: %v", err)
	}

	template := &x509.Certificate{
		SerialNumber: serialNumber,
		Subject: pkix.Name{
			CommonName: "localhost",
		},
		NotBefore: time.Now().Add(-1 * time.Minute),
		NotAfter:  time.Now().Add(24 * time.Hour),

		KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,

		DNSNames: []string{
			"localhost",
		},
		IPAddresses: []net.IP{
			net.ParseIP("127.0.0.1"),
			net.ParseIP("::1"),
		},
	}

	certDER, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create certificate failed: %v", err)
	}

	certPEM = pem.EncodeToMemory(&pem.Block{
		Type:  "CERTIFICATE",
		Bytes: certDER,
	})
	if certPEM == nil {
		t.Fatal("encode certificate PEM failed")
	}

	keyPEM = pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(key),
	})
	if keyPEM == nil {
		t.Fatal("encode private key PEM failed")
	}

	return certPEM, keyPEM
}