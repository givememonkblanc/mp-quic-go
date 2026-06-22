package benchmark

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/binary"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net"
	"testing"
	"time"

	quic "github.com/quic-go/quic-go"
)

const (
	throughputBenchmarkProto   = "mp-quic-benchmark-throughput"
	throughputBenchmarkTimeout = 60 * time.Second

	throughputFrameLikePayloadSize = 640 * 480 * 2
)

func BenchmarkThroughput(b *testing.B) {
	cases := []struct {
		name        string
		payloadSize int
	}{
		{
			name:        "small_4KiB",
			payloadSize: 4 * 1024,
		},
		{
			name:        "frame_640x480x2",
			payloadSize: throughputFrameLikePayloadSize,
		},
		{
			name:        "large_4MiB",
			payloadSize: 4 * 1024 * 1024,
		},
	}

	for _, tc := range cases {
		tc := tc

		b.Run(tc.name, func(b *testing.B) {
			benchmarkThroughputPayload(b, tc.payloadSize)
		})
	}
}

func benchmarkThroughputPayload(
	b *testing.B,
	payloadSize int,
) {
	ctx, cancel := context.WithTimeout(context.Background(), throughputBenchmarkTimeout)
	defer cancel()

	serverUDP := listenUDPForThroughputBenchmark(b)
	defer serverUDP.Close()

	serverTLS := throughputBenchmarkServerTLSConfig(b)
	clientTLS := throughputBenchmarkClientTLSConfig()

	listener, err := quic.ListenEarly(serverUDP, serverTLS, &quic.Config{
		MaxIdleTimeout:   30 * time.Second,
		InitialMaxPathID: 1,
	})
	if err != nil {
		b.Fatalf("listen early failed: %v", err)
	}
	defer listener.Close()

	serverErrCh := make(chan error, 1)

	go func() {
		serverErrCh <- runThroughputBenchmarkServer(ctx, listener)
	}()

	clientUDP := listenUDPForThroughputBenchmark(b)
	defer clientUDP.Close()

	clientConn, err := quic.DialEarly(ctx, clientUDP, listener.Addr(), clientTLS, &quic.Config{
		MaxIdleTimeout:   30 * time.Second,
		InitialMaxPathID: 1,
	})
	if err != nil {
		b.Fatalf("dial early failed: %v", err)
	}
	defer clientConn.CloseWithError(0, "throughput benchmark done")

	payload := makeThroughputBenchmarkPayload(payloadSize)

	if err := sendThroughputPayload(ctx, clientConn, payload); err != nil {
		b.Fatalf("warmup send failed: %v", err)
	}

	b.ReportAllocs()
	b.SetBytes(int64(payloadSize))

	start := time.Now()

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		if err := sendThroughputPayload(ctx, clientConn, payload); err != nil {
			b.Fatalf("send payload failed at iteration %d: %v", i, err)
		}
	}

	b.StopTimer()

	elapsed := time.Since(start)
	totalBytes := int64(payloadSize) * int64(b.N)

	if elapsed > 0 {
		mibPerSec := float64(totalBytes) / 1024.0 / 1024.0 / elapsed.Seconds()
		b.ReportMetric(mibPerSec, "MiB/s")
	}

	cancel()

	select {
	case err := <-serverErrCh:
		if err != nil && ctx.Err() == nil {
			b.Fatalf("server failed: %v", err)
		}

	case <-time.After(time.Second):
	}
}

func runThroughputBenchmarkServer(
	ctx context.Context,
	listener *quic.EarlyListener,
) error {
	conn, err := listener.Accept(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return nil
		}

		return fmt.Errorf("accept connection: %w", err)
	}
	defer conn.CloseWithError(0, "server done")

	for {
		stream, err := conn.AcceptStream(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}

			return fmt.Errorf("accept stream: %w", err)
		}

		if err := handleThroughputBenchmarkStream(stream); err != nil {
			return err
		}
	}
}

func handleThroughputBenchmarkStream(
	stream quic.Stream,
) error {
	n, err := io.Copy(io.Discard, stream)
	if err != nil {
		return fmt.Errorf("read throughput payload: %w", err)
	}

	if err := writeThroughputAck(stream, uint64(n)); err != nil {
		return fmt.Errorf("write throughput ack: %w", err)
	}

	if err := stream.Close(); err != nil {
		return fmt.Errorf("close server stream: %w", err)
	}

	return nil
}

func sendThroughputPayload(
	ctx context.Context,
	conn quic.EarlyConnection,
	payload []byte,
) error {
	stream, err := conn.OpenStreamSync(ctx)
	if err != nil {
		return fmt.Errorf("open stream: %w", err)
	}

	if err := writeFullThroughputBenchmark(stream, payload); err != nil {
		return fmt.Errorf("write payload: %w", err)
	}

	if err := stream.Close(); err != nil {
		return fmt.Errorf("close client write side: %w", err)
	}

	ackBytes, err := readThroughputAck(stream)
	if err != nil {
		return fmt.Errorf("read throughput ack: %w", err)
	}

	if ackBytes != uint64(len(payload)) {
		return fmt.Errorf("unexpected ack bytes: got=%d want=%d", ackBytes, len(payload))
	}

	return nil
}

func writeThroughputAck(
	w io.Writer,
	n uint64,
) error {
	raw := make([]byte, 8)
	binary.BigEndian.PutUint64(raw, n)

	return writeFullThroughputBenchmark(w, raw)
}

func readThroughputAck(
	r io.Reader,
) (uint64, error) {
	raw := make([]byte, 8)

	if _, err := io.ReadFull(r, raw); err != nil {
		return 0, err
	}

	return binary.BigEndian.Uint64(raw), nil
}

func makeThroughputBenchmarkPayload(size int) []byte {
	payload := make([]byte, size)

	for i := range payload {
		payload[i] = byte((i * 31) % 251)
	}

	return payload
}

func writeFullThroughputBenchmark(
	w io.Writer,
	data []byte,
) error {
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

func listenUDPForThroughputBenchmark(b *testing.B) *net.UDPConn {
	b.Helper()

	conn, err := net.ListenUDP("udp4", &net.UDPAddr{
		IP:   net.IPv4(127, 0, 0, 1),
		Port: 0,
	})
	if err != nil {
		b.Fatalf("listen UDP failed: %v", err)
	}

	return conn
}

func throughputBenchmarkServerTLSConfig(b *testing.B) *tls.Config {
	b.Helper()

	return &tls.Config{
		Certificates: []tls.Certificate{
			generateThroughputBenchmarkTLSCert(b),
		},
		NextProtos: []string{
			throughputBenchmarkProto,
		},
		MinVersion: tls.VersionTLS13,
	}
}

func throughputBenchmarkClientTLSConfig() *tls.Config {
	return &tls.Config{
		InsecureSkipVerify: true,
		NextProtos: []string{
			throughputBenchmarkProto,
		},
		MinVersion: tls.VersionTLS13,
	}
}

func generateThroughputBenchmarkTLSCert(b *testing.B) tls.Certificate {
	b.Helper()

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		b.Fatalf("generate rsa key failed: %v", err)
	}

	serialNumber, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		b.Fatalf("generate serial number failed: %v", err)
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
		b.Fatalf("create certificate failed: %v", err)
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
		b.Fatalf("parse key pair failed: %v", err)
	}

	return cert
}

func BenchmarkThroughputPayloadGenerator(b *testing.B) {
	payload := makeThroughputBenchmarkPayload(throughputFrameLikePayloadSize)

	if len(payload) != throughputFrameLikePayloadSize {
		b.Fatalf("unexpected payload size: got=%d want=%d", len(payload), throughputFrameLikePayloadSize)
	}

	expected := makeThroughputBenchmarkPayload(throughputFrameLikePayloadSize)

	if !bytes.Equal(payload, expected) {
		b.Fatal("payload generator should be deterministic")
	}
}