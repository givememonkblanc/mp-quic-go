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
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"sync"
	"testing"
	"time"

	quic "github.com/quic-go/quic-go"
)

const (
	frameContinuityProto   = "mp-quic-e2e-frame-continuity"
	frameContinuityTimeout = 20 * time.Second

	frameContinuityWidth       = 640
	frameContinuityHeight      = 480
	frameContinuityBytesPerPix = 2
	frameContinuityFrameSize   = frameContinuityWidth * frameContinuityHeight * frameContinuityBytesPerPix

	frameContinuityFrameCount = 30

	frameContinuityKindRGB   uint8 = 1
	frameContinuityKindDepth uint8 = 2

	frameContinuityMaxAllowedGap = 5 * time.Second
)

func TestScenarioFrameContinuity(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), frameContinuityTimeout)
	defer cancel()

	serverUDP := listenUDPForFrameContinuityTest(t)
	defer serverUDP.Close()

	serverTLS := frameContinuityServerTLSConfig(t)
	clientTLS := frameContinuityClientTLSConfig()

	listener, err := quic.ListenEarly(serverUDP, serverTLS, &quic.Config{
		MaxIdleTimeout:   5 * time.Second,
		InitialMaxPathID: 1,
	})
	if err != nil {
		t.Fatalf("listen early failed: %v", err)
	}
	defer listener.Close()

	serverDone := make(chan frameContinuityServerSummary, 1)

	go func() {
		summary, err := runFrameContinuityServer(ctx, listener)
		summary.Err = err
		serverDone <- summary
	}()

	clientUDP := listenUDPForFrameContinuityTest(t)
	defer clientUDP.Close()

	clientConn, err := quic.DialEarly(ctx, clientUDP, listener.Addr(), clientTLS, &quic.Config{
		MaxIdleTimeout:   5 * time.Second,
		InitialMaxPathID: 1,
	})
	if err != nil {
		t.Fatalf("dial early failed: %v", err)
	}
	defer clientConn.CloseWithError(0, "frame continuity test done")

	start := time.Now()

	clientSummary, err := runFrameContinuityClient(ctx, clientConn)
	if err != nil {
		t.Fatalf("frame continuity client failed: %v", err)
	}

	elapsed := time.Since(start)

	validateFrameContinuityStreamSummary(t, "client RGB summary", clientSummary.RGB, frameContinuityKindRGB)
	validateFrameContinuityStreamSummary(t, "client Depth summary", clientSummary.Depth, frameContinuityKindDepth)

	select {
	case summary := <-serverDone:
		if summary.Err != nil {
			t.Fatalf("frame continuity server failed: %v", summary.Err)
		}

		validateFrameContinuityStreamSummary(t, "server RGB summary", summary.RGB, frameContinuityKindRGB)
		validateFrameContinuityStreamSummary(t, "server Depth summary", summary.Depth, frameContinuityKindDepth)

		t.Logf(
			"frame continuity scenario passed: rgb=%d depth=%d rgbBytes=%d depthBytes=%d elapsed=%s",
			summary.RGB.Frames,
			summary.Depth.Frames,
			summary.RGB.PayloadBytes,
			summary.Depth.PayloadBytes,
			elapsed,
		)

	case <-ctx.Done():
		t.Fatalf("timeout waiting for frame continuity server summary: %v", ctx.Err())
	}
}

func runFrameContinuityServer(
	ctx context.Context,
	listener *quic.EarlyListener,
) (frameContinuityServerSummary, error) {
	conn, err := listener.Accept(ctx)
	if err != nil {
		return frameContinuityServerSummary{}, fmt.Errorf("accept connection: %w", err)
	}
	defer conn.CloseWithError(0, "server done")

	resultCh := make(chan frameContinuityStreamResult, 2)

	var wg sync.WaitGroup

	for i := 0; i < 2; i++ {
		stream, err := conn.AcceptStream(ctx)
		if err != nil {
			return frameContinuityServerSummary{}, fmt.Errorf("accept stream %d: %w", i, err)
		}

		wg.Add(1)

		go func(stream quic.Stream) {
			defer wg.Done()

			summary, err := receiveFrameContinuityStream(stream)

			resultCh <- frameContinuityStreamResult{
				Summary: summary,
				Err:     err,
			}
		}(stream)
	}

	wg.Wait()
	close(resultCh)

	var serverSummary frameContinuityServerSummary

	for result := range resultCh {
		if result.Err != nil {
			return serverSummary, result.Err
		}

		switch result.Summary.Kind {
		case frameContinuityKindRGB:
			serverSummary.RGB = result.Summary

		case frameContinuityKindDepth:
			serverSummary.Depth = result.Summary

		default:
			return serverSummary, fmt.Errorf("unknown stream summary kind: %d", result.Summary.Kind)
		}
	}

	return serverSummary, nil
}

func runFrameContinuityClient(
	ctx context.Context,
	conn quic.EarlyConnection,
) (frameContinuityServerSummary, error) {
	rgbStream, err := conn.OpenStreamSync(ctx)
	if err != nil {
		return frameContinuityServerSummary{}, fmt.Errorf("open RGB stream: %w", err)
	}

	depthStream, err := conn.OpenStreamSync(ctx)
	if err != nil {
		return frameContinuityServerSummary{}, fmt.Errorf("open Depth stream: %w", err)
	}

	errCh := make(chan error, 2)

	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()

		if err := sendFrameContinuityStream(rgbStream, frameContinuityKindRGB); err != nil {
			errCh <- fmt.Errorf("send RGB stream: %w", err)
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()

		if err := sendFrameContinuityStream(depthStream, frameContinuityKindDepth); err != nil {
			errCh <- fmt.Errorf("send Depth stream: %w", err)
		}
	}()

	wg.Wait()
	close(errCh)

	for err := range errCh {
		if err != nil {
			return frameContinuityServerSummary{}, err
		}
	}

	rgbSummary, err := readFrameContinuityWireSummary(rgbStream)
	if err != nil {
		return frameContinuityServerSummary{}, fmt.Errorf("read RGB wire summary: %w", err)
	}

	depthSummary, err := readFrameContinuityWireSummary(depthStream)
	if err != nil {
		return frameContinuityServerSummary{}, fmt.Errorf("read Depth wire summary: %w", err)
	}

	return frameContinuityServerSummary{
		RGB:   rgbSummary,
		Depth: depthSummary,
	}, nil
}

func sendFrameContinuityStream(
	stream quic.Stream,
	kind uint8,
) error {
	if err := writeFullFrameContinuity(stream, []byte{kind}); err != nil {
		return fmt.Errorf("write stream kind: %w", err)
	}

	for serial := uint64(1); serial <= frameContinuityFrameCount; serial++ {
		payload := makeFrameContinuityPayload(kind, serial, frameContinuityFrameSize)

		header := frameContinuityFrameHeader{
			Kind:              kind,
			Serial:            serial,
			TimestampUnixNano: uint64(time.Now().UnixNano()),
			Width:             frameContinuityWidth,
			Height:            frameContinuityHeight,
			Size:              frameContinuityFrameSize,
		}

		if err := writeFrameContinuityFrame(stream, header, payload); err != nil {
			return fmt.Errorf("write frame serial=%d kind=%d: %w", serial, kind, err)
		}

		if serial%10 == 0 {
			time.Sleep(5 * time.Millisecond)
		}
	}

	if err := stream.Close(); err != nil {
		return fmt.Errorf("close stream write side: %w", err)
	}

	return nil
}

func receiveFrameContinuityStream(
	stream quic.Stream,
) (frameContinuityStreamSummary, error) {
	kind, err := readFrameContinuityStreamKind(stream)
	if err != nil {
		return frameContinuityStreamSummary{}, err
	}

	if kind != frameContinuityKindRGB && kind != frameContinuityKindDepth {
		return frameContinuityStreamSummary{}, fmt.Errorf("unknown frame stream kind: %d", kind)
	}

	summary := frameContinuityStreamSummary{
		Kind: kind,
	}

	expectedSerial := uint64(1)
	seen := make(map[uint64]bool)

	var lastFrameAt time.Time

	for {
		header, payload, err := readFrameContinuityFrame(stream)
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}

			return summary, err
		}

		now := time.Now()
		if !lastFrameAt.IsZero() {
			gap := now.Sub(lastFrameAt)
			if gap > summary.MaxFrameGap {
				summary.MaxFrameGap = gap
			}
		}
		lastFrameAt = now

		if header.Kind != kind {
			return summary, fmt.Errorf("stream kind mismatch: stream=%d frame=%d serial=%d", kind, header.Kind, header.Serial)
		}

		if header.Width != frameContinuityWidth {
			return summary, fmt.Errorf("unexpected width: got=%d want=%d", header.Width, frameContinuityWidth)
		}

		if header.Height != frameContinuityHeight {
			return summary, fmt.Errorf("unexpected height: got=%d want=%d", header.Height, frameContinuityHeight)
		}

		if header.Size != frameContinuityFrameSize {
			return summary, fmt.Errorf("unexpected size: got=%d want=%d", header.Size, frameContinuityFrameSize)
		}

		if len(payload) != frameContinuityFrameSize {
			return summary, fmt.Errorf("unexpected payload size: got=%d want=%d", len(payload), frameContinuityFrameSize)
		}

		if !frameContinuityPayloadMatches(header.Kind, header.Serial, payload) {
			return summary, fmt.Errorf("payload mismatch: kind=%d serial=%d", header.Kind, header.Serial)
		}

		if seen[header.Serial] {
			summary.Duplicates++
		} else {
			seen[header.Serial] = true
		}

		if header.Serial == expectedSerial {
			expectedSerial++
		} else if header.Serial > expectedSerial {
			summary.Missing += int(header.Serial - expectedSerial)
			expectedSerial = header.Serial + 1
		} else {
			summary.OutOfOrder++
		}

		summary.Frames++
		summary.LastSerial = header.Serial
		summary.PayloadBytes += int64(len(payload))
	}

	if err := writeFrameContinuityWireSummary(stream, summary); err != nil {
		return summary, fmt.Errorf("write wire summary: %w", err)
	}

	if err := stream.Close(); err != nil {
		return summary, fmt.Errorf("close server stream: %w", err)
	}

	return summary, nil
}

type frameContinuityFrameHeader struct {
	Kind              uint8
	Serial            uint64
	TimestampUnixNano uint64
	Width             uint32
	Height            uint32
	Size              uint32
}

const frameContinuityHeaderSize = 1 + 8 + 8 + 4 + 4 + 4

func writeFrameContinuityFrame(
	w io.Writer,
	header frameContinuityFrameHeader,
	payload []byte,
) error {
	if len(payload) != int(header.Size) {
		return fmt.Errorf("payload size mismatch: got=%d want=%d", len(payload), header.Size)
	}

	raw := make([]byte, frameContinuityHeaderSize)

	raw[0] = header.Kind
	binary.BigEndian.PutUint64(raw[1:9], header.Serial)
	binary.BigEndian.PutUint64(raw[9:17], header.TimestampUnixNano)
	binary.BigEndian.PutUint32(raw[17:21], header.Width)
	binary.BigEndian.PutUint32(raw[21:25], header.Height)
	binary.BigEndian.PutUint32(raw[25:29], header.Size)

	if err := writeFullFrameContinuity(w, raw); err != nil {
		return fmt.Errorf("write frame header: %w", err)
	}

	if err := writeFullFrameContinuity(w, payload); err != nil {
		return fmt.Errorf("write frame payload: %w", err)
	}

	return nil
}

func readFrameContinuityFrame(r io.Reader) (frameContinuityFrameHeader, []byte, error) {
	raw := make([]byte, frameContinuityHeaderSize)

	if _, err := io.ReadFull(r, raw); err != nil {
		if errors.Is(err, io.EOF) {
			return frameContinuityFrameHeader{}, nil, io.EOF
		}

		return frameContinuityFrameHeader{}, nil, fmt.Errorf("read frame header: %w", err)
	}

	header := frameContinuityFrameHeader{
		Kind:              raw[0],
		Serial:            binary.BigEndian.Uint64(raw[1:9]),
		TimestampUnixNano: binary.BigEndian.Uint64(raw[9:17]),
		Width:             binary.BigEndian.Uint32(raw[17:21]),
		Height:            binary.BigEndian.Uint32(raw[21:25]),
		Size:              binary.BigEndian.Uint32(raw[25:29]),
	}

	if header.Serial == 0 {
		return frameContinuityFrameHeader{}, nil, fmt.Errorf("invalid zero serial")
	}

	if header.Size == 0 {
		return frameContinuityFrameHeader{}, nil, fmt.Errorf("invalid zero frame size")
	}

	if header.Size > 10*1024*1024 {
		return frameContinuityFrameHeader{}, nil, fmt.Errorf("frame too large: %d", header.Size)
	}

	payload := make([]byte, header.Size)

	if _, err := io.ReadFull(r, payload); err != nil {
		return frameContinuityFrameHeader{}, nil, fmt.Errorf("read frame payload: %w", err)
	}

	return header, payload, nil
}

type frameContinuityServerSummary struct {
	RGB   frameContinuityStreamSummary
	Depth frameContinuityStreamSummary
	Err   error
}

type frameContinuityStreamSummary struct {
	Kind         uint8
	Frames       int
	LastSerial   uint64
	Missing      int
	Duplicates   int
	OutOfOrder   int
	PayloadBytes int64
	MaxFrameGap  time.Duration
}

type frameContinuityStreamResult struct {
	Summary frameContinuityStreamSummary
	Err     error
}

func writeFrameContinuityWireSummary(
	w io.Writer,
	summary frameContinuityStreamSummary,
) error {
	raw := make([]byte, 1+4+8+4+4+4+8+8)

	raw[0] = summary.Kind
	binary.BigEndian.PutUint32(raw[1:5], uint32(summary.Frames))
	binary.BigEndian.PutUint64(raw[5:13], summary.LastSerial)
	binary.BigEndian.PutUint32(raw[13:17], uint32(summary.Missing))
	binary.BigEndian.PutUint32(raw[17:21], uint32(summary.Duplicates))
	binary.BigEndian.PutUint32(raw[21:25], uint32(summary.OutOfOrder))
	binary.BigEndian.PutUint64(raw[25:33], uint64(summary.PayloadBytes))
	binary.BigEndian.PutUint64(raw[33:41], uint64(summary.MaxFrameGap))

	return writeFullFrameContinuity(w, raw)
}

func readFrameContinuityWireSummary(r io.Reader) (frameContinuityStreamSummary, error) {
	raw := make([]byte, 41)

	if _, err := io.ReadFull(r, raw); err != nil {
		return frameContinuityStreamSummary{}, err
	}

	return frameContinuityStreamSummary{
		Kind:         raw[0],
		Frames:       int(binary.BigEndian.Uint32(raw[1:5])),
		LastSerial:   binary.BigEndian.Uint64(raw[5:13]),
		Missing:      int(binary.BigEndian.Uint32(raw[13:17])),
		Duplicates:   int(binary.BigEndian.Uint32(raw[17:21])),
		OutOfOrder:   int(binary.BigEndian.Uint32(raw[21:25])),
		PayloadBytes: int64(binary.BigEndian.Uint64(raw[25:33])),
		MaxFrameGap:  time.Duration(binary.BigEndian.Uint64(raw[33:41])),
	}, nil
}

func readFrameContinuityStreamKind(r io.Reader) (uint8, error) {
	var raw [1]byte

	if _, err := io.ReadFull(r, raw[:]); err != nil {
		return 0, fmt.Errorf("read stream kind: %w", err)
	}

	return raw[0], nil
}

func validateFrameContinuityStreamSummary(
	t *testing.T,
	name string,
	summary frameContinuityStreamSummary,
	wantKind uint8,
) {
	t.Helper()

	if summary.Kind != wantKind {
		t.Fatalf("%s: unexpected kind: got=%d want=%d", name, summary.Kind, wantKind)
	}

	if summary.Frames != frameContinuityFrameCount {
		t.Fatalf("%s: unexpected frame count: got=%d want=%d", name, summary.Frames, frameContinuityFrameCount)
	}

	if summary.LastSerial != frameContinuityFrameCount {
		t.Fatalf("%s: unexpected last serial: got=%d want=%d", name, summary.LastSerial, frameContinuityFrameCount)
	}

	if summary.Missing != 0 {
		t.Fatalf("%s: expected no missing frames, got=%d", name, summary.Missing)
	}

	if summary.Duplicates != 0 {
		t.Fatalf("%s: expected no duplicate frames, got=%d", name, summary.Duplicates)
	}

	if summary.OutOfOrder != 0 {
		t.Fatalf("%s: expected no out-of-order frames, got=%d", name, summary.OutOfOrder)
	}

	expectedBytes := int64(frameContinuityFrameCount * frameContinuityFrameSize)
	if summary.PayloadBytes != expectedBytes {
		t.Fatalf("%s: unexpected payload bytes: got=%d want=%d", name, summary.PayloadBytes, expectedBytes)
	}

	if summary.MaxFrameGap > frameContinuityMaxAllowedGap {
		t.Fatalf("%s: frame gap too large: got=%s limit=%s", name, summary.MaxFrameGap, frameContinuityMaxAllowedGap)
	}
}

func makeFrameContinuityPayload(kind uint8, serial uint64, size int) []byte {
	payload := make([]byte, size)

	seed := byte(kind) ^ byte(serial) ^ 0xa5

	for i := range payload {
		payload[i] = seed + byte(i%251)
	}

	return payload
}

func frameContinuityPayloadMatches(kind uint8, serial uint64, payload []byte) bool {
	expected := makeFrameContinuityPayload(kind, serial, len(payload))
	return bytes.Equal(expected, payload)
}

func writeFullFrameContinuity(w io.Writer, data []byte) error {
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

func listenUDPForFrameContinuityTest(t *testing.T) *net.UDPConn {
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

func frameContinuityServerTLSConfig(t *testing.T) *tls.Config {
	t.Helper()

	return &tls.Config{
		Certificates: []tls.Certificate{
			generateFrameContinuityTLSCert(t),
		},
		NextProtos: []string{
			frameContinuityProto,
		},
		MinVersion: tls.VersionTLS13,
	}
}

func frameContinuityClientTLSConfig() *tls.Config {
	return &tls.Config{
		InsecureSkipVerify: true,
		NextProtos: []string{
			frameContinuityProto,
		},
		MinVersion: tls.VersionTLS13,
	}
}

func generateFrameContinuityTLSCert(t *testing.T) tls.Certificate {
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