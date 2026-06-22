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
	handoverLatencyProto = "mp-quic-benchmark-handover-latency"

	handoverLatencyWidth       = 640
	handoverLatencyHeight      = 480
	handoverLatencyBytesPerPix = 2
	handoverLatencyFrameSize   = handoverLatencyWidth * handoverLatencyHeight * handoverLatencyBytesPerPix

	handoverLatencyKindRGB uint8 = 1

	handoverLatencySourceWiFi   uint8 = 1
	handoverLatencySourceBackup uint8 = 2
)

func BenchmarkHandoverLatency(b *testing.B) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	serverUDP := listenUDPForHandoverBenchmark(b)
	defer serverUDP.Close()

	serverTLS := handoverBenchmarkServerTLSConfig(b)
	clientTLS := handoverBenchmarkClientTLSConfig()

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
		serverErrCh <- runHandoverBenchmarkServer(ctx, listener)
	}()

	clientUDP := listenUDPForHandoverBenchmark(b)
	defer clientUDP.Close()

	clientConn, err := quic.DialEarly(ctx, clientUDP, listener.Addr(), clientTLS, &quic.Config{
		MaxIdleTimeout:   30 * time.Second,
		InitialMaxPathID: 1,
	})
	if err != nil {
		b.Fatalf("dial early failed: %v", err)
	}
	defer clientConn.CloseWithError(0, "benchmark done")

	payload := makeHandoverBenchmarkPayload(handoverLatencyKindRGB, 1, handoverLatencyFrameSize)

	b.ReportAllocs()
	b.SetBytes(int64(len(payload)))
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		serial := uint64(i + 1)

		wifiStream, err := clientConn.OpenStreamSync(ctx)
		if err != nil {
			b.Fatalf("open wifi stream failed: %v", err)
		}

		if err := writeFullHandoverBenchmark(wifiStream, []byte{handoverLatencySourceWiFi}); err != nil {
			b.Fatalf("write wifi source failed: %v", err)
		}

		if err := writeHandoverBenchmarkFrame(wifiStream, handoverBenchmarkFrameHeader{
			Kind:   handoverLatencyKindRGB,
			Serial: serial,
			Width:  handoverLatencyWidth,
			Height: handoverLatencyHeight,
			Size:   handoverLatencyFrameSize,
		}, payload); err != nil {
			b.Fatalf("write wifi frame failed: %v", err)
		}

		if err := wifiStream.Close(); err != nil {
			b.Fatalf("close wifi stream failed: %v", err)
		}

		start := time.Now()

		backupStream, err := clientConn.OpenStreamSync(ctx)
		if err != nil {
			b.Fatalf("open backup stream failed: %v", err)
		}

		if err := writeFullHandoverBenchmark(backupStream, []byte{handoverLatencySourceBackup}); err != nil {
			b.Fatalf("write backup source failed: %v", err)
		}

		if err := writeHandoverBenchmarkFrame(backupStream, handoverBenchmarkFrameHeader{
			Kind:   handoverLatencyKindRGB,
			Serial: serial,
			Width:  handoverLatencyWidth,
			Height: handoverLatencyHeight,
			Size:   handoverLatencyFrameSize,
		}, payload); err != nil {
			b.Fatalf("write backup frame failed: %v", err)
		}

		ack, err := readHandoverBenchmarkAck(backupStream)
		if err != nil {
			b.Fatalf("read backup ack failed: %v", err)
		}

		if ack.Source != handoverLatencySourceBackup {
			b.Fatalf("unexpected ack source: got=%d want=%d", ack.Source, handoverLatencySourceBackup)
		}

		if ack.Serial != serial {
			b.Fatalf("unexpected ack serial: got=%d want=%d", ack.Serial, serial)
		}

		if err := backupStream.Close(); err != nil {
			b.Fatalf("close backup stream failed: %v", err)
		}

		b.ReportMetric(float64(time.Since(start).Microseconds()), "handover_us/op")
	}

	b.StopTimer()

	cancel()

	select {
	case err := <-serverErrCh:
		if err != nil && ctx.Err() == nil {
			b.Fatalf("server failed: %v", err)
		}

	case <-time.After(time.Second):
	}
}

func runHandoverBenchmarkServer(
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

		if err := handleHandoverBenchmarkStream(stream); err != nil {
			return err
		}
	}
}

func handleHandoverBenchmarkStream(stream quic.Stream) error {
	source, err := readHandoverBenchmarkSource(stream)
	if err != nil {
		return err
	}

	header, payload, err := readHandoverBenchmarkFrame(stream)
	if err != nil {
		return err
	}

	if header.Kind != handoverLatencyKindRGB {
		return fmt.Errorf("unexpected frame kind: got=%d want=%d", header.Kind, handoverLatencyKindRGB)
	}

	if header.Width != handoverLatencyWidth {
		return fmt.Errorf("unexpected width: got=%d want=%d", header.Width, handoverLatencyWidth)
	}

	if header.Height != handoverLatencyHeight {
		return fmt.Errorf("unexpected height: got=%d want=%d", header.Height, handoverLatencyHeight)
	}

	if header.Size != handoverLatencyFrameSize {
		return fmt.Errorf("unexpected frame size: got=%d want=%d", header.Size, handoverLatencyFrameSize)
	}

	if len(payload) != handoverLatencyFrameSize {
		return fmt.Errorf("unexpected payload size: got=%d want=%d", len(payload), handoverLatencyFrameSize)
	}

	expected := makeHandoverBenchmarkPayload(header.Kind, header.Serial, len(payload))
	if !bytes.Equal(payload, expected) && header.Serial == 1 {
		return fmt.Errorf("payload mismatch")
	}

	if source == handoverLatencySourceBackup {
		if err := writeHandoverBenchmarkAck(stream, handoverBenchmarkAck{
			Source: source,
			Serial: header.Serial,
		}); err != nil {
			return fmt.Errorf("write backup ack: %w", err)
		}
	}

	if err := stream.Close(); err != nil {
		return fmt.Errorf("close server stream: %w", err)
	}

	return nil
}

type handoverBenchmarkFrameHeader struct {
	Kind   uint8
	Serial uint64
	Width  uint32
	Height uint32
	Size   uint32
}

const handoverBenchmarkHeaderSize = 1 + 8 + 4 + 4 + 4

func writeHandoverBenchmarkFrame(
	w io.Writer,
	header handoverBenchmarkFrameHeader,
	payload []byte,
) error {
	if len(payload) != int(header.Size) {
		return fmt.Errorf("payload size mismatch: got=%d want=%d", len(payload), header.Size)
	}

	raw := make([]byte, handoverBenchmarkHeaderSize)

	raw[0] = header.Kind
	binary.BigEndian.PutUint64(raw[1:9], header.Serial)
	binary.BigEndian.PutUint32(raw[9:13], header.Width)
	binary.BigEndian.PutUint32(raw[13:17], header.Height)
	binary.BigEndian.PutUint32(raw[17:21], header.Size)

	if err := writeFullHandoverBenchmark(w, raw); err != nil {
		return err
	}

	if err := writeFullHandoverBenchmark(w, payload); err != nil {
		return err
	}

	return nil
}

func readHandoverBenchmarkFrame(r io.Reader) (handoverBenchmarkFrameHeader, []byte, error) {
	raw := make([]byte, handoverBenchmarkHeaderSize)

	if _, err := io.ReadFull(r, raw); err != nil {
		return handoverBenchmarkFrameHeader{}, nil, fmt.Errorf("read frame header: %w", err)
	}

	header := handoverBenchmarkFrameHeader{
		Kind:   raw[0],
		Serial: binary.BigEndian.Uint64(raw[1:9]),
		Width:  binary.BigEndian.Uint32(raw[9:13]),
		Height: binary.BigEndian.Uint32(raw[13:17]),
		Size:   binary.BigEndian.Uint32(raw[17:21]),
	}

	if header.Size == 0 {
		return handoverBenchmarkFrameHeader{}, nil, fmt.Errorf("invalid zero frame size")
	}

	if header.Size > 10*1024*1024 {
		return handoverBenchmarkFrameHeader{}, nil, fmt.Errorf("frame too large: %d", header.Size)
	}

	payload := make([]byte, header.Size)

	if _, err := io.ReadFull(r, payload); err != nil {
		return handoverBenchmarkFrameHeader{}, nil, fmt.Errorf("read frame payload: %w", err)
	}

	return header, payload, nil
}

type handoverBenchmarkAck struct {
	Source uint8
	Serial uint64
}

func writeHandoverBenchmarkAck(w io.Writer, ack handoverBenchmarkAck) error {
	raw := make([]byte, 1+8)

	raw[0] = ack.Source
	binary.BigEndian.PutUint64(raw[1:9], ack.Serial)

	return writeFullHandoverBenchmark(w, raw)
}

func readHandoverBenchmarkAck(r io.Reader) (handoverBenchmarkAck, error) {
	raw := make([]byte, 1+8)

	if _, err := io.ReadFull(r, raw); err != nil {
		return handoverBenchmarkAck{}, err
	}

	return handoverBenchmarkAck{
		Source: raw[0],
		Serial: binary.BigEndian.Uint64(raw[1:9]),
	}, nil
}

func readHandoverBenchmarkSource(r io.Reader) (uint8, error) {
	var raw [1]byte

	if _, err := io.ReadFull(r, raw[:]); err != nil {
		return 0, fmt.Errorf("read source: %w", err)
	}

	return raw[0], nil
}

func makeHandoverBenchmarkPayload(kind uint8, serial uint64, size int) []byte {
	payload := make([]byte, size)

	seed := byte(kind) ^ byte(serial) ^ 0x77

	for i := range payload {
		payload[i] = seed + byte(i%251)
	}

	return payload
}

func writeFullHandoverBenchmark(w io.Writer, data []byte) error {
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

func listenUDPForHandoverBenchmark(b *testing.B) *net.UDPConn {
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

func handoverBenchmarkServerTLSConfig(b *testing.B) *tls.Config {
	b.Helper()

	return &tls.Config{
		Certificates: []tls.Certificate{
			generateHandoverBenchmarkTLSCert(b),
		},
		NextProtos: []string{
			handoverLatencyProto,
		},
		MinVersion: tls.VersionTLS13,
	}
}

func handoverBenchmarkClientTLSConfig() *tls.Config {
	return &tls.Config{
		InsecureSkipVerify: true,
		NextProtos: []string{
			handoverLatencyProto,
		},
		MinVersion: tls.VersionTLS13,
	}
}

func generateHandoverBenchmarkTLSCert(b *testing.B) tls.Certificate {
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