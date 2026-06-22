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
	"sync"
	"testing"
	"time"

	quic "github.com/quic-go/quic-go"
)

const (
	congestionProto   = "mp-quic-e2e-congestion"
	congestionTimeout = 20 * time.Second

	congestionWidth       = 640
	congestionHeight      = 480
	congestionBytesPerPix = 2
	congestionFrameSize   = congestionWidth * congestionHeight * congestionBytesPerPix

	congestionFrameCount = 12

	congestionFrameKindRGB   uint8 = 1
	congestionFrameKindDepth uint8 = 2

	congestionStreamTypeFrames uint8 = 1
	congestionStreamTypeLoad   uint8 = 2

	congestionLoadStreamCount = 4
	congestionLoadBytesEach   = 512 * 1024
	congestionChunkSize       = 32 * 1024

	congestionMaxAllowedFrameGap = 3 * time.Second
)

func TestScenarioCongestion(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), congestionTimeout)
	defer cancel()

	serverUDP := listenUDPForCongestionTest(t)
	defer serverUDP.Close()

	serverTLS := congestionServerTLSConfig(t)
	clientTLS := congestionClientTLSConfig()

	listener, err := quic.ListenEarly(serverUDP, serverTLS, &quic.Config{
		MaxIdleTimeout:   5 * time.Second,
		InitialMaxPathID: 1,
	})
	if err != nil {
		t.Fatalf("listen early failed: %v", err)
	}
	defer listener.Close()

	serverDone := make(chan congestionServerSummary, 1)

	go func() {
		summary, err := runCongestionServer(ctx, listener)
		summary.Err = err
		serverDone <- summary
	}()

	clientUDP := listenUDPForCongestionTest(t)
	defer clientUDP.Close()

	clientConn, err := quic.DialEarly(ctx, clientUDP, listener.Addr(), clientTLS, &quic.Config{
		MaxIdleTimeout:   5 * time.Second,
		InitialMaxPathID: 1,
	})
	if err != nil {
		t.Fatalf("dial early failed: %v", err)
	}
	defer clientConn.CloseWithError(0, "congestion test done")

	start := time.Now()

	if err := runCongestionClient(ctx, clientConn); err != nil {
		t.Fatalf("congestion client failed: %v", err)
	}

	elapsed := time.Since(start)

	select {
	case summary := <-serverDone:
		if summary.Err != nil {
			t.Fatalf("congestion server failed: %v", summary.Err)
		}

		if summary.RGBFrames != congestionFrameCount {
			t.Fatalf("unexpected RGB frame count: got=%d want=%d", summary.RGBFrames, congestionFrameCount)
		}

		if summary.DepthFrames != congestionFrameCount {
			t.Fatalf("unexpected Depth frame count: got=%d want=%d", summary.DepthFrames, congestionFrameCount)
		}

		if summary.LastRGBSerial != congestionFrameCount {
			t.Fatalf("unexpected last RGB serial: got=%d want=%d", summary.LastRGBSerial, congestionFrameCount)
		}

		if summary.LastDepthSerial != congestionFrameCount {
			t.Fatalf("unexpected last Depth serial: got=%d want=%d", summary.LastDepthSerial, congestionFrameCount)
		}

		expectedFrameBytes := int64(congestionFrameCount * 2 * congestionFrameSize)
		if summary.FrameBytesReceived != expectedFrameBytes {
			t.Fatalf("unexpected frame bytes: got=%d want=%d", summary.FrameBytesReceived, expectedFrameBytes)
		}

		expectedLoadBytes := int64(congestionLoadStreamCount * congestionLoadBytesEach)
		if summary.LoadBytesReceived != expectedLoadBytes {
			t.Fatalf("unexpected load bytes: got=%d want=%d", summary.LoadBytesReceived, expectedLoadBytes)
		}

		if summary.MaxFrameGap > congestionMaxAllowedFrameGap {
			t.Fatalf("frame gap too large under congestion: got=%s limit=%s", summary.MaxFrameGap, congestionMaxAllowedFrameGap)
		}

		t.Logf(
			"congestion scenario passed: rgb=%d depth=%d frameBytes=%d loadBytes=%d maxFrameGap=%s elapsed=%s",
			summary.RGBFrames,
			summary.DepthFrames,
			summary.FrameBytesReceived,
			summary.LoadBytesReceived,
			summary.MaxFrameGap,
			elapsed,
		)

	case <-ctx.Done():
		t.Fatalf("timeout waiting for congestion server summary: %v", ctx.Err())
	}
}

func runCongestionServer(
	ctx context.Context,
	listener *quic.EarlyListener,
) (congestionServerSummary, error) {
	conn, err := listener.Accept(ctx)
	if err != nil {
		return congestionServerSummary{}, fmt.Errorf("accept connection: %w", err)
	}
	defer conn.CloseWithError(0, "server done")

	var summary congestionServerSummary
	var mu sync.Mutex
	var wg sync.WaitGroup

	errCh := make(chan error, 1+congestionLoadStreamCount)

	for i := 0; i < 1+congestionLoadStreamCount; i++ {
		stream, err := conn.AcceptStream(ctx)
		if err != nil {
			return summary, fmt.Errorf("accept stream %d: %w", i, err)
		}

		wg.Add(1)

		go func(stream quic.Stream) {
			defer wg.Done()

			streamType, err := readCongestionStreamType(stream)
			if err != nil {
				errCh <- err
				return
			}

			switch streamType {
			case congestionStreamTypeFrames:
				frameSummary, err := receiveCongestionFrames(stream)
				if err != nil {
					errCh <- err
					return
				}

				if err := writeCongestionWireSummary(stream, frameSummary); err != nil {
					errCh <- fmt.Errorf("write congestion wire summary: %w", err)
					return
				}

				if err := stream.Close(); err != nil {
					errCh <- fmt.Errorf("close frame stream: %w", err)
					return
				}

				mu.Lock()
				summary.RGBFrames = frameSummary.RGBFrames
				summary.DepthFrames = frameSummary.DepthFrames
				summary.LastRGBSerial = frameSummary.LastRGBSerial
				summary.LastDepthSerial = frameSummary.LastDepthSerial
				summary.FrameBytesReceived = frameSummary.FrameBytesReceived
				summary.MaxFrameGap = frameSummary.MaxFrameGap
				mu.Unlock()

			case congestionStreamTypeLoad:
				n, err := io.Copy(io.Discard, stream)
				if err != nil {
					errCh <- fmt.Errorf("read load stream: %w", err)
					return
				}

				mu.Lock()
				summary.LoadBytesReceived += n
				mu.Unlock()

			default:
				errCh <- fmt.Errorf("unknown stream type: %d", streamType)
				return
			}
		}(stream)
	}

	wg.Wait()
	close(errCh)

	for err := range errCh {
		if err != nil {
			return summary, err
		}
	}

	return summary, nil
}

func runCongestionClient(
	ctx context.Context,
	conn quic.EarlyConnection,
) error {
	frameStream, err := conn.OpenStreamSync(ctx)
	if err != nil {
		return fmt.Errorf("open frame stream: %w", err)
	}

	if err := writeFull(frameStream, []byte{congestionStreamTypeFrames}); err != nil {
		return fmt.Errorf("write frame stream type: %w", err)
	}

	loadErrCh := make(chan error, congestionLoadStreamCount)
	var loadWG sync.WaitGroup

	for i := 0; i < congestionLoadStreamCount; i++ {
		loadWG.Add(1)

		go func(index int) {
			defer loadWG.Done()

			if err := runCongestionLoadStream(ctx, conn, index); err != nil {
				loadErrCh <- err
			}
		}(i)
	}

	for serial := uint64(1); serial <= congestionFrameCount; serial++ {
		rgbPayload := makeCongestionPayload(congestionFrameKindRGB, serial, congestionFrameSize)

		if err := writeCongestionFrame(frameStream, congestionFrameHeader{
			Kind:   congestionFrameKindRGB,
			Serial: serial,
			Width:  congestionWidth,
			Height: congestionHeight,
			Size:   congestionFrameSize,
		}, rgbPayload); err != nil {
			return fmt.Errorf("write RGB frame serial=%d: %w", serial, err)
		}

		depthPayload := makeCongestionPayload(congestionFrameKindDepth, serial, congestionFrameSize)

		if err := writeCongestionFrame(frameStream, congestionFrameHeader{
			Kind:   congestionFrameKindDepth,
			Serial: serial,
			Width:  congestionWidth,
			Height: congestionHeight,
			Size:   congestionFrameSize,
		}, depthPayload); err != nil {
			return fmt.Errorf("write Depth frame serial=%d: %w", serial, err)
		}
	}

	wireSummary, err := readCongestionWireSummary(frameStream)
	if err != nil {
		return fmt.Errorf("read congestion wire summary: %w", err)
	}

	if wireSummary.RGBFrames != congestionFrameCount {
		return fmt.Errorf("server wire summary RGB mismatch: got=%d want=%d", wireSummary.RGBFrames, congestionFrameCount)
	}

	if wireSummary.DepthFrames != congestionFrameCount {
		return fmt.Errorf("server wire summary Depth mismatch: got=%d want=%d", wireSummary.DepthFrames, congestionFrameCount)
	}

	if err := frameStream.Close(); err != nil {
		return fmt.Errorf("close frame stream: %w", err)
	}

	loadWG.Wait()
	close(loadErrCh)

	for err := range loadErrCh {
		if err != nil {
			return err
		}
	}

	return nil
}

func runCongestionLoadStream(
	ctx context.Context,
	conn quic.EarlyConnection,
	index int,
) error {
	stream, err := conn.OpenStreamSync(ctx)
	if err != nil {
		return fmt.Errorf("open load stream %d: %w", index, err)
	}

	if err := writeFull(stream, []byte{congestionStreamTypeLoad}); err != nil {
		return fmt.Errorf("write load stream type %d: %w", index, err)
	}

	chunk := make([]byte, congestionChunkSize)
	for i := range chunk {
		chunk[i] = byte(index + i%251)
	}

	remaining := congestionLoadBytesEach

	for remaining > 0 {
		n := congestionChunkSize
		if remaining < n {
			n = remaining
		}

		if err := writeFull(stream, chunk[:n]); err != nil {
			return fmt.Errorf("write load stream %d: %w", index, err)
		}

		remaining -= n
	}

	if err := stream.Close(); err != nil {
		return fmt.Errorf("close load stream %d: %w", index, err)
	}

	return nil
}

func receiveCongestionFrames(stream quic.Stream) (congestionServerSummary, error) {
	var summary congestionServerSummary

	expectedRGBSerial := uint64(1)
	expectedDepthSerial := uint64(1)

	totalFrames := congestionFrameCount * 2

	var lastFrameAt time.Time

	for i := 0; i < totalFrames; i++ {
		header, payload, err := readCongestionFrame(stream)
		if err != nil {
			return summary, fmt.Errorf("read frame %d: %w", i+1, err)
		}

		now := time.Now()
		if !lastFrameAt.IsZero() {
			gap := now.Sub(lastFrameAt)
			if gap > summary.MaxFrameGap {
				summary.MaxFrameGap = gap
			}
		}
		lastFrameAt = now

		if header.Width != congestionWidth {
			return summary, fmt.Errorf("unexpected frame width: got=%d want=%d", header.Width, congestionWidth)
		}

		if header.Height != congestionHeight {
			return summary, fmt.Errorf("unexpected frame height: got=%d want=%d", header.Height, congestionHeight)
		}

		if header.Size != congestionFrameSize {
			return summary, fmt.Errorf("unexpected frame size in header: got=%d want=%d", header.Size, congestionFrameSize)
		}

		if len(payload) != congestionFrameSize {
			return summary, fmt.Errorf("unexpected payload size: got=%d want=%d", len(payload), congestionFrameSize)
		}

		if !congestionPayloadMatches(header.Kind, header.Serial, payload) {
			return summary, fmt.Errorf("payload mismatch: kind=%d serial=%d", header.Kind, header.Serial)
		}

		switch header.Kind {
		case congestionFrameKindRGB:
			if header.Serial != expectedRGBSerial {
				return summary, fmt.Errorf("RGB serial gap: got=%d want=%d", header.Serial, expectedRGBSerial)
			}
			expectedRGBSerial++
			summary.RGBFrames++
			summary.LastRGBSerial = header.Serial

		case congestionFrameKindDepth:
			if header.Serial != expectedDepthSerial {
				return summary, fmt.Errorf("Depth serial gap: got=%d want=%d", header.Serial, expectedDepthSerial)
			}
			expectedDepthSerial++
			summary.DepthFrames++
			summary.LastDepthSerial = header.Serial

		default:
			return summary, fmt.Errorf("unknown frame kind: %d", header.Kind)
		}

		summary.FrameBytesReceived += int64(len(payload))
	}

	return summary, nil
}

type congestionFrameHeader struct {
	Kind   uint8
	Serial uint64
	Width  uint32
	Height uint32
	Size   uint32
}

const congestionHeaderSize = 1 + 8 + 4 + 4 + 4

func writeCongestionFrame(
	w io.Writer,
	header congestionFrameHeader,
	payload []byte,
) error {
	if len(payload) != int(header.Size) {
		return fmt.Errorf("payload size mismatch: got=%d want=%d", len(payload), header.Size)
	}

	raw := make([]byte, congestionHeaderSize)

	raw[0] = header.Kind
	binary.BigEndian.PutUint64(raw[1:9], header.Serial)
	binary.BigEndian.PutUint32(raw[9:13], header.Width)
	binary.BigEndian.PutUint32(raw[13:17], header.Height)
	binary.BigEndian.PutUint32(raw[17:21], header.Size)

	if err := writeFull(w, raw); err != nil {
		return fmt.Errorf("write frame header: %w", err)
	}

	if err := writeFull(w, payload); err != nil {
		return fmt.Errorf("write frame payload: %w", err)
	}

	return nil
}

func readCongestionFrame(r io.Reader) (congestionFrameHeader, []byte, error) {
	raw := make([]byte, congestionHeaderSize)

	if _, err := io.ReadFull(r, raw); err != nil {
		return congestionFrameHeader{}, nil, fmt.Errorf("read frame header: %w", err)
	}

	header := congestionFrameHeader{
		Kind:   raw[0],
		Serial: binary.BigEndian.Uint64(raw[1:9]),
		Width:  binary.BigEndian.Uint32(raw[9:13]),
		Height: binary.BigEndian.Uint32(raw[13:17]),
		Size:   binary.BigEndian.Uint32(raw[17:21]),
	}

	if header.Size == 0 {
		return congestionFrameHeader{}, nil, fmt.Errorf("invalid zero frame size")
	}

	if header.Size > 10*1024*1024 {
		return congestionFrameHeader{}, nil, fmt.Errorf("frame too large: %d", header.Size)
	}

	payload := make([]byte, header.Size)

	if _, err := io.ReadFull(r, payload); err != nil {
		return congestionFrameHeader{}, nil, fmt.Errorf("read frame payload: %w", err)
	}

	return header, payload, nil
}

type congestionServerSummary struct {
	RGBFrames          int
	DepthFrames        int
	LastRGBSerial      uint64
	LastDepthSerial    uint64
	FrameBytesReceived int64
	LoadBytesReceived  int64
	MaxFrameGap        time.Duration
	Err                error
}

type congestionWireSummary struct {
	RGBFrames       int
	DepthFrames     int
	LastRGBSerial   uint64
	LastDepthSerial uint64
	FrameBytes      int64
}

func writeCongestionWireSummary(w io.Writer, summary congestionServerSummary) error {
	raw := make([]byte, 4+4+8+8+8)

	binary.BigEndian.PutUint32(raw[0:4], uint32(summary.RGBFrames))
	binary.BigEndian.PutUint32(raw[4:8], uint32(summary.DepthFrames))
	binary.BigEndian.PutUint64(raw[8:16], summary.LastRGBSerial)
	binary.BigEndian.PutUint64(raw[16:24], summary.LastDepthSerial)
	binary.BigEndian.PutUint64(raw[24:32], uint64(summary.FrameBytesReceived))

	return writeFull(w, raw)
}

func readCongestionWireSummary(r io.Reader) (congestionWireSummary, error) {
	raw := make([]byte, 32)

	if _, err := io.ReadFull(r, raw); err != nil {
		return congestionWireSummary{}, err
	}

	return congestionWireSummary{
		RGBFrames:       int(binary.BigEndian.Uint32(raw[0:4])),
		DepthFrames:     int(binary.BigEndian.Uint32(raw[4:8])),
		LastRGBSerial:   binary.BigEndian.Uint64(raw[8:16]),
		LastDepthSerial: binary.BigEndian.Uint64(raw[16:24]),
		FrameBytes:      int64(binary.BigEndian.Uint64(raw[24:32])),
	}, nil
}

func readCongestionStreamType(r io.Reader) (uint8, error) {
	var raw [1]byte

	if _, err := io.ReadFull(r, raw[:]); err != nil {
		return 0, fmt.Errorf("read stream type: %w", err)
	}

	return raw[0], nil
}

func makeCongestionPayload(kind uint8, serial uint64, size int) []byte {
	payload := make([]byte, size)

	seed := byte(kind) ^ byte(serial) ^ 0x5a

	for i := range payload {
		payload[i] = seed + byte(i%251)
	}

	return payload
}

func congestionPayloadMatches(kind uint8, serial uint64, payload []byte) bool {
	expected := makeCongestionPayload(kind, serial, len(payload))
	return bytes.Equal(expected, payload)
}

func writeFull(w io.Writer, data []byte) error {
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

func listenUDPForCongestionTest(t *testing.T) *net.UDPConn {
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

func congestionServerTLSConfig(t *testing.T) *tls.Config {
	t.Helper()

	return &tls.Config{
		Certificates: []tls.Certificate{
			generateCongestionTLSCert(t),
		},
		NextProtos: []string{
			congestionProto,
		},
		MinVersion: tls.VersionTLS13,
	}
}

func congestionClientTLSConfig() *tls.Config {
	return &tls.Config{
		InsecureSkipVerify: true,
		NextProtos: []string{
			congestionProto,
		},
		MinVersion: tls.VersionTLS13,
	}
}

func generateCongestionTLSCert(t *testing.T) tls.Certificate {
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