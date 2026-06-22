package e2e

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
	baselineProto   = "mp-quic-e2e-baseline"
	baselineTimeout = 15 * time.Second

	baselineWidth       = 640
	baselineHeight      = 480
	baselineBytesPerPix = 2
	baselineFrameSize   = baselineWidth * baselineHeight * baselineBytesPerPix

	baselineFrameCount = 20

	baselineFrameKindRGB   uint8 = 1
	baselineFrameKindDepth uint8 = 2
)

// TestScenarioBaseline verifies the normal no-failure e2e path.
//
// Scenario:
// 1. Local QUIC server starts.
// 2. Client connects.
// 3. Client sends RGB and Depth frame streams.
// 4. Server receives all frames.
// 5. Serial numbers are continuous.
// 6. Payload sizes are stable.
// 7. Server returns a final ACK summary.
//
// This test does not simulate Wi-Fi disconnection, path migration, congestion,
// or recovery. It is the baseline scenario that later failure scenarios compare against.
func TestScenarioBaseline(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), baselineTimeout)
	defer cancel()

	serverUDP := listenUDPForBaselineTest(t)
	defer serverUDP.Close()

	serverTLS := baselineServerTLSConfig(t)
	clientTLS := baselineClientTLSConfig()

	listener, err := quic.ListenEarly(serverUDP, serverTLS, &quic.Config{
		MaxIdleTimeout:   5 * time.Second,
		InitialMaxPathID: 1,
	})
	if err != nil {
		t.Fatalf("listen early failed: %v", err)
	}
	defer listener.Close()

	serverDone := make(chan baselineServerSummary, 1)

	go func() {
		summary, err := runBaselineServer(ctx, listener)
		serverDone <- baselineServerSummary{
			RGBFrames:       summary.RGBFrames,
			DepthFrames:     summary.DepthFrames,
			LastRGBSerial:   summary.LastRGBSerial,
			LastDepthSerial: summary.LastDepthSerial,
			BytesReceived:   summary.BytesReceived,
			Err:             err,
		}
	}()

	clientUDP := listenUDPForBaselineTest(t)
	defer clientUDP.Close()

	clientConn, err := quic.DialEarly(ctx, clientUDP, listener.Addr(), clientTLS, &quic.Config{
		MaxIdleTimeout:   5 * time.Second,
		InitialMaxPathID: 1,
	})
	if err != nil {
		t.Fatalf("dial early failed: %v", err)
	}
	defer clientConn.CloseWithError(0, "baseline test done")

	start := time.Now()

	if err := runBaselineClient(ctx, clientConn); err != nil {
		t.Fatalf("baseline client failed: %v", err)
	}

	elapsed := time.Since(start)

	select {
	case summary := <-serverDone:
		if summary.Err != nil {
			t.Fatalf("baseline server failed: %v", summary.Err)
		}

		if summary.RGBFrames != baselineFrameCount {
			t.Fatalf("unexpected RGB frame count: got=%d want=%d", summary.RGBFrames, baselineFrameCount)
		}

		if summary.DepthFrames != baselineFrameCount {
			t.Fatalf("unexpected Depth frame count: got=%d want=%d", summary.DepthFrames, baselineFrameCount)
		}

		if summary.LastRGBSerial != baselineFrameCount {
			t.Fatalf("unexpected last RGB serial: got=%d want=%d", summary.LastRGBSerial, baselineFrameCount)
		}

		if summary.LastDepthSerial != baselineFrameCount {
			t.Fatalf("unexpected last Depth serial: got=%d want=%d", summary.LastDepthSerial, baselineFrameCount)
		}

		expectedBytes := baselineFrameCount * 2 * baselineFrameSize
		if summary.BytesReceived != expectedBytes {
			t.Fatalf("unexpected received bytes: got=%d want=%d", summary.BytesReceived, expectedBytes)
		}

		t.Logf(
			"baseline scenario passed: rgb=%d depth=%d bytes=%d elapsed=%s",
			summary.RGBFrames,
			summary.DepthFrames,
			summary.BytesReceived,
			elapsed,
		)

	case <-ctx.Done():
		t.Fatalf("timeout waiting for baseline server summary: %v", ctx.Err())
	}
}

func runBaselineServer(
	ctx context.Context,
	listener *quic.EarlyListener,
) (baselineServerSummary, error) {
	conn, err := listener.Accept(ctx)
	if err != nil {
		return baselineServerSummary{}, fmt.Errorf("accept connection: %w", err)
	}
	defer conn.CloseWithError(0, "server done")

	stream, err := conn.AcceptStream(ctx)
	if err != nil {
		return baselineServerSummary{}, fmt.Errorf("accept stream: %w", err)
	}

	summary := baselineServerSummary{}

	expectedRGBSerial := uint64(1)
	expectedDepthSerial := uint64(1)

	totalFrames := baselineFrameCount * 2

	for i := 0; i < totalFrames; i++ {
		header, payload, err := readBaselineFrame(stream)
		if err != nil {
			return summary, fmt.Errorf("read frame %d: %w", i+1, err)
		}

		if header.Width != baselineWidth {
			return summary, fmt.Errorf("unexpected frame width: got=%d want=%d", header.Width, baselineWidth)
		}

		if header.Height != baselineHeight {
			return summary, fmt.Errorf("unexpected frame height: got=%d want=%d", header.Height, baselineHeight)
		}

		if header.Size != baselineFrameSize {
			return summary, fmt.Errorf("unexpected frame size in header: got=%d want=%d", header.Size, baselineFrameSize)
		}

		if len(payload) != baselineFrameSize {
			return summary, fmt.Errorf("unexpected payload size: got=%d want=%d", len(payload), baselineFrameSize)
		}

		if !baselinePayloadMatches(header.Kind, header.Serial, payload) {
			return summary, fmt.Errorf("payload content mismatch: kind=%d serial=%d", header.Kind, header.Serial)
		}

		switch header.Kind {
		case baselineFrameKindRGB:
			if header.Serial != expectedRGBSerial {
				return summary, fmt.Errorf("RGB serial gap: got=%d want=%d", header.Serial, expectedRGBSerial)
			}
			expectedRGBSerial++
			summary.RGBFrames++
			summary.LastRGBSerial = header.Serial

		case baselineFrameKindDepth:
			if header.Serial != expectedDepthSerial {
				return summary, fmt.Errorf("Depth serial gap: got=%d want=%d", header.Serial, expectedDepthSerial)
			}
			expectedDepthSerial++
			summary.DepthFrames++
			summary.LastDepthSerial = header.Serial

		default:
			return summary, fmt.Errorf("unknown frame kind: %d", header.Kind)
		}

		summary.BytesReceived += len(payload)
	}

	if err := writeBaselineSummary(stream, summary); err != nil {
		return summary, fmt.Errorf("write baseline summary: %w", err)
	}

	if err := stream.Close(); err != nil {
		return summary, fmt.Errorf("close server stream: %w", err)
	}

	return summary, nil
}

func runBaselineClient(
	ctx context.Context,
	conn quic.EarlyConnection,
) error {
	stream, err := conn.OpenStreamSync(ctx)
	if err != nil {
		return fmt.Errorf("open stream: %w", err)
	}

	for serial := uint64(1); serial <= baselineFrameCount; serial++ {
		rgbPayload := makeBaselinePayload(baselineFrameKindRGB, serial, baselineFrameSize)
		if err := writeBaselineFrame(stream, baselineFrameHeader{
			Kind:   baselineFrameKindRGB,
			Serial: serial,
			Width:  baselineWidth,
			Height: baselineHeight,
			Size:   baselineFrameSize,
		}, rgbPayload); err != nil {
			return fmt.Errorf("write RGB frame serial=%d: %w", serial, err)
		}

		depthPayload := makeBaselinePayload(baselineFrameKindDepth, serial, baselineFrameSize)
		if err := writeBaselineFrame(stream, baselineFrameHeader{
			Kind:   baselineFrameKindDepth,
			Serial: serial,
			Width:  baselineWidth,
			Height: baselineHeight,
			Size:   baselineFrameSize,
		}, depthPayload); err != nil {
			return fmt.Errorf("write Depth frame serial=%d: %w", serial, err)
		}
	}

	summary, err := readBaselineSummary(stream)
	if err != nil {
		return fmt.Errorf("read baseline summary: %w", err)
	}

	if summary.RGBFrames != baselineFrameCount {
		return fmt.Errorf("server summary RGB frame count mismatch: got=%d want=%d", summary.RGBFrames, baselineFrameCount)
	}

	if summary.DepthFrames != baselineFrameCount {
		return fmt.Errorf("server summary Depth frame count mismatch: got=%d want=%d", summary.DepthFrames, baselineFrameCount)
	}

	if err := stream.Close(); err != nil {
		return fmt.Errorf("close client stream: %w", err)
	}

	return nil
}

type baselineFrameHeader struct {
	Kind   uint8
	Serial uint64
	Width  uint32
	Height uint32
	Size   uint32
}

const baselineHeaderSize = 1 + 8 + 4 + 4 + 4

func writeBaselineFrame(
	w io.Writer,
	header baselineFrameHeader,
	payload []byte,
) error {
	if len(payload) != int(header.Size) {
		return fmt.Errorf("payload size mismatch: got=%d want=%d", len(payload), header.Size)
	}

	raw := make([]byte, baselineHeaderSize)

	raw[0] = header.Kind
	binary.BigEndian.PutUint64(raw[1:9], header.Serial)
	binary.BigEndian.PutUint32(raw[9:13], header.Width)
	binary.BigEndian.PutUint32(raw[13:17], header.Height)
	binary.BigEndian.PutUint32(raw[17:21], header.Size)

	if _, err := w.Write(raw); err != nil {
		return fmt.Errorf("write frame header: %w", err)
	}

	if _, err := w.Write(payload); err != nil {
		return fmt.Errorf("write frame payload: %w", err)
	}

	return nil
}

func readBaselineFrame(r io.Reader) (baselineFrameHeader, []byte, error) {
	raw := make([]byte, baselineHeaderSize)

	if _, err := io.ReadFull(r, raw); err != nil {
		return baselineFrameHeader{}, nil, fmt.Errorf("read frame header: %w", err)
	}

	header := baselineFrameHeader{
		Kind:   raw[0],
		Serial: binary.BigEndian.Uint64(raw[1:9]),
		Width:  binary.BigEndian.Uint32(raw[9:13]),
		Height: binary.BigEndian.Uint32(raw[13:17]),
		Size:   binary.BigEndian.Uint32(raw[17:21]),
	}

	if header.Size == 0 {
		return baselineFrameHeader{}, nil, fmt.Errorf("invalid zero frame size")
	}

	if header.Size > 10*1024*1024 {
		return baselineFrameHeader{}, nil, fmt.Errorf("frame too large: %d", header.Size)
	}

	payload := make([]byte, header.Size)

	if _, err := io.ReadFull(r, payload); err != nil {
		return baselineFrameHeader{}, nil, fmt.Errorf("read frame payload: %w", err)
	}

	return header, payload, nil
}

type baselineServerSummary struct {
	RGBFrames       int
	DepthFrames     int
	LastRGBSerial   uint64
	LastDepthSerial uint64
	BytesReceived   int
	Err             error
}

func writeBaselineSummary(w io.Writer, summary baselineServerSummary) error {
	raw := make([]byte, 4+4+8+8+8)

	binary.BigEndian.PutUint32(raw[0:4], uint32(summary.RGBFrames))
	binary.BigEndian.PutUint32(raw[4:8], uint32(summary.DepthFrames))
	binary.BigEndian.PutUint64(raw[8:16], summary.LastRGBSerial)
	binary.BigEndian.PutUint64(raw[16:24], summary.LastDepthSerial)
	binary.BigEndian.PutUint64(raw[24:32], uint64(summary.BytesReceived))

	_, err := w.Write(raw)
	return err
}

func readBaselineSummary(r io.Reader) (baselineServerSummary, error) {
	raw := make([]byte, 32)

	if _, err := io.ReadFull(r, raw); err != nil {
		return baselineServerSummary{}, err
	}

	return baselineServerSummary{
		RGBFrames:       int(binary.BigEndian.Uint32(raw[0:4])),
		DepthFrames:     int(binary.BigEndian.Uint32(raw[4:8])),
		LastRGBSerial:   binary.BigEndian.Uint64(raw[8:16]),
		LastDepthSerial: binary.BigEndian.Uint64(raw[16:24]),
		BytesReceived:   int(binary.BigEndian.Uint64(raw[24:32])),
	}, nil
}

func makeBaselinePayload(kind uint8, serial uint64, size int) []byte {
	payload := make([]byte, size)

	seed := byte(kind) ^ byte(serial)

	for i := range payload {
		payload[i] = seed + byte(i%251)
	}

	return payload
}

func baselinePayloadMatches(kind uint8, serial uint64, payload []byte) bool {
	expected := makeBaselinePayload(kind, serial, len(payload))
	return bytes.Equal(expected, payload)
}

func listenUDPForBaselineTest(t *testing.T) *net.UDPConn {
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

func baselineServerTLSConfig(t *testing.T) *tls.Config {
	t.Helper()

	return &tls.Config{
		Certificates: []tls.Certificate{
			generateBaselineTLSCert(t),
		},
		NextProtos: []string{
			baselineProto,
		},
		MinVersion: tls.VersionTLS13,
	}
}

func baselineClientTLSConfig() *tls.Config {
	return &tls.Config{
		InsecureSkipVerify: true,
		NextProtos: []string{
			baselineProto,
		},
		MinVersion: tls.VersionTLS13,
	}
}

func generateBaselineTLSCert(t *testing.T) tls.Certificate {
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
		t.Fatalf("parse key pair failed: %v", err)
	}

	return cert
}