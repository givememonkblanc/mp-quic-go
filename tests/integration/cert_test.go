package integration

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
	"testing"
	"time"

	quic "github.com/quic-go/quic-go"

	mpPath "mp-quic-go/internal/mpquic/path"
)

const (
	addPathSmokeProto   = "mp-quic-add-path-smoke"
	addPathSmokeTimeout = 5 * time.Second
)

func TestAddPathSmoke(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), addPathSmokeTimeout)
	defer cancel()

	serverUDP := listenUDPForTest(t)
	defer serverUDP.Close()

	secondUDP := listenUDPForTest(t)
	defer secondUDP.Close()

	serverTLS := testServerTLSConfig(t)
	clientTLS := testClientTLSConfig()

	quicConfig := &quic.Config{
		MaxIdleTimeout: 3 * time.Second,
	}

	listener, err := quic.Listen(serverUDP, serverTLS, quicConfig)
	if err != nil {
		t.Fatalf("listen quic failed: %v", err)
	}
	defer listener.Close()

	serverConnCh := make(chan quic.Connection, 1)
	serverErrCh := make(chan error, 1)

	payload := []byte("add-path-smoke-payload")

	go func() {
		conn, err := listener.Accept(ctx)
		if err != nil {
			serverErrCh <- fmt.Errorf("accept connection: %w", err)
			return
		}

		serverConnCh <- conn

		stream, err := conn.AcceptStream(ctx)
		if err != nil {
			serverErrCh <- fmt.Errorf("accept stream: %w", err)
			return
		}

		buf := make([]byte, len(payload))
		if _, err := io.ReadFull(stream, buf); err != nil {
			serverErrCh <- fmt.Errorf("read stream payload: %w", err)
			return
		}

		if !bytes.Equal(buf, payload) {
			serverErrCh <- fmt.Errorf("unexpected stream payload: got=%q want=%q", buf, payload)
			return
		}

		if _, err := stream.Write(buf); err != nil {
			serverErrCh <- fmt.Errorf("write stream echo: %w", err)
			return
		}

		if err := stream.Close(); err != nil {
			serverErrCh <- fmt.Errorf("close server stream: %w", err)
			return
		}

		serverErrCh <- nil
	}()

	clientConn, err := quic.DialAddr(ctx, serverUDP.LocalAddr().String(), clientTLS, quicConfig)
	if err != nil {
		t.Fatalf("dial quic failed: %v", err)
	}
	defer clientConn.CloseWithError(0, "test done")

	var serverConn quic.Connection

	select {
	case serverConn = <-serverConnCh:
	case err := <-serverErrCh:
		t.Fatalf("server failed before connection accepted: %v", err)
	case <-ctx.Done():
		t.Fatalf("timeout waiting for server connection: %v", ctx.Err())
	}

	packetObsCh := observeOptionalUDPPacket(secondUDP, 500*time.Millisecond)

	if err := addPathForSmoke(serverConn, secondUDP.LocalAddr(), 1); err != nil {
		t.Fatalf("add path failed: %v", err)
	}

	stream, err := clientConn.OpenStreamSync(ctx)
	if err != nil {
		t.Fatalf("open client stream failed: %v", err)
	}

	if _, err := stream.Write(payload); err != nil {
		t.Fatalf("write client payload failed: %v", err)
	}

	echo := make([]byte, len(payload))
	if _, err := io.ReadFull(stream, echo); err != nil {
		t.Fatalf("read echo failed: %v", err)
	}

	if !bytes.Equal(echo, payload) {
		t.Fatalf("unexpected echo payload: got=%q want=%q", echo, payload)
	}

	if err := stream.Close(); err != nil {
		t.Fatalf("close client stream failed: %v", err)
	}

	select {
	case err := <-serverErrCh:
		if err != nil {
			t.Fatalf("server failed: %v", err)
		}
	case <-ctx.Done():
		t.Fatalf("timeout waiting for server completion: %v", ctx.Err())
	}

	select {
	case obs := <-packetObsCh:
		if obs.n > 0 {
			t.Logf("observed %d bytes on optional second UDP socket", obs.n)
		} else if obs.err != nil {
			t.Logf("no packet observed on second UDP socket during smoke test: %v", obs.err)
		}
	default:
		t.Log("second UDP observation did not complete before test end")
	}
}

func addPathForSmoke(conn quic.Connection, addr net.Addr, pathID uint32) error {
	udpAddr, ok := addr.(*net.UDPAddr)
	if !ok {
		return fmt.Errorf("expected *net.UDPAddr, got %T", addr)
	}

	switch c := any(conn).(type) {
	case interface {
		AddPath(*net.UDPAddr, uint32) error
	}:
		return c.AddPath(udpAddr, pathID)

	case interface {
		AddPath(*net.UDPAddr, mpPath.ID) error
	}:
		return c.AddPath(udpAddr, mpPath.ID(pathID))

	case interface {
		AddPath(net.Addr, uint32) error
	}:
		return c.AddPath(addr, pathID)

	case interface {
		AddPath(net.Addr, mpPath.ID) error
	}:
		return c.AddPath(addr, mpPath.ID(pathID))

	case interface {
		AddPath(string, uint32) error
	}:
		return c.AddPath(addr.String(), pathID)

	case interface {
		AddPath(string, mpPath.ID) error
	}:
		return c.AddPath(addr.String(), mpPath.ID(pathID))

	default:
		return fmt.Errorf("quic connection does not expose supported AddPath method")
	}
}

type udpObservation struct {
	n   int
	err error
}

func observeOptionalUDPPacket(conn *net.UDPConn, timeout time.Duration) <-chan udpObservation {
	ch := make(chan udpObservation, 1)

	go func() {
		buf := make([]byte, 2048)

		_ = conn.SetReadDeadline(time.Now().Add(timeout))
		n, _, err := conn.ReadFromUDP(buf)

		ch <- udpObservation{
			n:   n,
			err: err,
		}
	}()

	return ch
}

func listenUDPForTest(t *testing.T) *net.UDPConn {
	t.Helper()

	addr := &net.UDPAddr{
		IP:   net.ParseIP("127.0.0.1"),
		Port: 0,
	}

	conn, err := net.ListenUDP("udp4", addr)
	if err != nil {
		t.Fatalf("listen udp failed: %v", err)
	}

	return conn
}

func testServerTLSConfig(t *testing.T) *tls.Config {
	t.Helper()

	cert := generateSelfSignedCertForIntegrationTest(t)

	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		NextProtos:   []string{addPathSmokeProto},
		MinVersion:   tls.VersionTLS13,
	}
}

func testClientTLSConfig() *tls.Config {
	return &tls.Config{
		InsecureSkipVerify: true,
		NextProtos:         []string{addPathSmokeProto},
		MinVersion:         tls.VersionTLS13,
	}
}

func generateSelfSignedCertForIntegrationTest(t *testing.T) tls.Certificate {
	t.Helper()

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate rsa key failed: %v", err)
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

		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:              []string{"localhost"},
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1")},
		BasicConstraintsValid: true,
	}

	derBytes, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create certificate failed: %v", err)
	}

	certPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "CERTIFICATE",
		Bytes: derBytes,
	})

	keyPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(key),
	})

	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		t.Fatalf("parse tls key pair failed: %v", err)
	}

	return cert
}