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
	wifiDisconnectProto   = "mp-quic-e2e-wifi-disconnect"
	wifiDisconnectTimeout = 20 * time.Second

	wifiDisconnectWidth       = 640
	wifiDisconnectHeight      = 480
	wifiDisconnectBytesPerPix = 2
	wifiDisconnectFrameSize   = wifiDisconnectWidth * wifiDisconnectHeight * wifiDisconnectBytesPerPix

	wifiDisconnectFrameCount    = 16
	wifiDisconnectCutoverSerial = 6

	wifiDisconnectKindRGB   uint8 = 1
	wifiDisconnectKindDepth uint8 = 2

	wifiDisconnectSourceWiFi   uint8 = 1
	wifiDisconnectSourceBackup uint8 = 2

	wifiDisconnectSimulatedOutage = 100 * time.Millisecond
	wifiDisconnectMaxAllowedGap   = 2 * time.Second
)

func TestScenarioWiFiDisconnect(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), wifiDisconnectTimeout)
	defer cancel()

	serverUDP := listenUDPForWiFiDisconnectTest(t)
	defer serverUDP.Close()

	serverTLS := wifiDisconnectServerTLSConfig(t)
	clientTLS := wifiDisconnectClientTLSConfig()

	listener, err := quic.ListenEarly(serverUDP, serverTLS, &quic.Config{
		MaxIdleTimeout:   5 * time.Second,
		InitialMaxPathID: 1,
	})
	if err != nil {
		t.Fatalf("listen early failed: %v", err)
	}
	defer listener.Close()

	serverDone := make(chan wifiDisconnectSummary, 1)

	go func() {
		summary, err := runWiFiDisconnectServer(ctx, listener)
		summary.Err = err
		serverDone <- summary
	}()

	clientUDP := listenUDPForWiFiDisconnectTest(t)
	defer clientUDP.Close()

	clientConn, err := quic.DialEarly(ctx, clientUDP, listener.Addr(), clientTLS, &quic.Config{
		MaxIdleTimeout:   5 * time.Second,
		InitialMaxPathID: 1,
	})
	if err != nil {
		t.Fatalf("dial early failed: %v", err)
	}
	defer clientConn.CloseWithError(0, "wifi disconnect test done")

	start := time.Now()

	if err := runWiFiDisconnectClient(ctx, clientConn); err != nil {
		t.Fatalf("wifi disconnect client failed: %v", err)
	}

	elapsed := time.Since(start)

	select {
	case summary := <-serverDone:
		if summary.Err != nil {
			t.Fatalf("wifi disconnect server failed: %v", summary.Err)
		}

		validateWiFiDisconnectSummary(t, summary)

		t.Logf(
			"wifi disconnect scenario passed: rgb=%d depth=%d wifiFrames=%d backupFrames=%d gap=%s bytes=%d elapsed=%s",
			summary.RGBFrames,
			summary.DepthFrames,
			summary.WiFiFrames,
			summary.BackupFrames,
			summary.DisconnectGap,
			summary.PayloadBytes,
			elapsed,
		)

	case <-ctx.Done():
		t.Fatalf("timeout waiting for wifi disconnect server summary: %v", ctx.Err())
	}
}

func runWiFiDisconnectServer(
	ctx context.Context,
	listener *quic.EarlyListener,
) (wifiDisconnectSummary, error) {
	conn, err := listener.Accept(ctx)
	if err != nil {
		return wifiDisconnectSummary{}, fmt.Errorf("accept connection: %w", err)
	}
	defer conn.CloseWithError(0, "server done")

	resultCh := make(chan wifiDisconnectStreamResult, 2)

	var wg sync.WaitGroup

	for i := 0; i < 2; i++ {
		stream, err := conn.AcceptStream(ctx)
		if err != nil {
			return wifiDisconnectSummary{}, fmt.Errorf("accept stream %d: %w", i, err)
		}

		wg.Add(1)

		go func(stream quic.Stream) {
			defer wg.Done()

			records, err := receiveWiFiDisconnectStream(stream)

			resultCh <- wifiDisconnectStreamResult{
				Records: records,
				Err:     err,
			}
		}(stream)
	}

	wg.Wait()
	close(resultCh)

	var allRecords []wifiDisconnectFrameRecord

	for result := range resultCh {
		if result.Err != nil {
			return wifiDisconnectSummary{}, result.Err
		}

		allRecords = append(allRecords, result.Records...)
	}

	summary, err := summarizeWiFiDisconnectRecords(allRecords)
	if err != nil {
		return summary, err
	}

	return summary, nil
}

func runWiFiDisconnectClient(
	ctx context.Context,
	conn quic.EarlyConnection,
) error {
	wifiStream, err := conn.OpenStreamSync(ctx)
	if err != nil {
		return fmt.Errorf("open wifi stream: %w", err)
	}

	if err := writeFullWiFiDisconnect(wifiStream, []byte{wifiDisconnectSourceWiFi}); err != nil {
		return fmt.Errorf("write wifi stream source: %w", err)
	}

	for serial := uint64(1); serial <= wifiDisconnectCutoverSerial; serial++ {
		if err := sendWiFiDisconnectFramePair(wifiStream, serial); err != nil {
			return fmt.Errorf("send wifi frame pair serial=%d: %w", serial, err)
		}
	}

	if err := wifiStream.Close(); err != nil {
		return fmt.Errorf("close wifi stream after simulated disconnect: %w", err)
	}

	time.Sleep(wifiDisconnectSimulatedOutage)

	backupStream, err := conn.OpenStreamSync(ctx)
	if err != nil {
		return fmt.Errorf("open backup stream: %w", err)
	}

	if err := writeFullWiFiDisconnect(backupStream, []byte{wifiDisconnectSourceBackup}); err != nil {
		return fmt.Errorf("write backup stream source: %w", err)
	}

	for serial := uint64(wifiDisconnectCutoverSerial + 1); serial <= wifiDisconnectFrameCount; serial++ {
		if err := sendWiFiDisconnectFramePair(backupStream, serial); err != nil {
			return fmt.Errorf("send backup frame pair serial=%d: %w", serial, err)
		}
	}

	if err := backupStream.Close(); err != nil {
		return fmt.Errorf("close backup stream: %w", err)
	}

	return nil
}

func sendWiFiDisconnectFramePair(
	stream quic.Stream,
	serial uint64,
) error {
	rgbPayload := makeWiFiDisconnectPayload(wifiDisconnectKindRGB, serial, wifiDisconnectFrameSize)

	if err := writeWiFiDisconnectFrame(stream, wifiDisconnectFrameHeader{
		Kind:   wifiDisconnectKindRGB,
		Serial: serial,
		Width:  wifiDisconnectWidth,
		Height: wifiDisconnectHeight,
		Size:   wifiDisconnectFrameSize,
	}, rgbPayload); err != nil {
		return fmt.Errorf("write RGB frame: %w", err)
	}

	depthPayload := makeWiFiDisconnectPayload(wifiDisconnectKindDepth, serial, wifiDisconnectFrameSize)

	if err := writeWiFiDisconnectFrame(stream, wifiDisconnectFrameHeader{
		Kind:   wifiDisconnectKindDepth,
		Serial: serial,
		Width:  wifiDisconnectWidth,
		Height: wifiDisconnectHeight,
		Size:   wifiDisconnectFrameSize,
	}, depthPayload); err != nil {
		return fmt.Errorf("write Depth frame: %w", err)
	}

	return nil
}

func receiveWiFiDisconnectStream(
	stream quic.Stream,
) ([]wifiDisconnectFrameRecord, error) {
	source, err := readWiFiDisconnectStreamSource(stream)
	if err != nil {
		return nil, err
	}

	if source != wifiDisconnectSourceWiFi && source != wifiDisconnectSourceBackup {
		return nil, fmt.Errorf("unknown stream source: %d", source)
	}

	var records []wifiDisconnectFrameRecord

	for {
		header, payload, err := readWiFiDisconnectFrame(stream)
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}

			return records, err
		}

		if header.Kind != wifiDisconnectKindRGB && header.Kind != wifiDisconnectKindDepth {
			return records, fmt.Errorf("unknown frame kind: %d", header.Kind)
		}

		if header.Width != wifiDisconnectWidth {
			return records, fmt.Errorf("unexpected frame width: got=%d want=%d", header.Width, wifiDisconnectWidth)
		}

		if header.Height != wifiDisconnectHeight {
			return records, fmt.Errorf("unexpected frame height: got=%d want=%d", header.Height, wifiDisconnectHeight)
		}

		if header.Size != wifiDisconnectFrameSize {
			return records, fmt.Errorf("unexpected frame size: got=%d want=%d", header.Size, wifiDisconnectFrameSize)
		}

		if len(payload) != wifiDisconnectFrameSize {
			return records, fmt.Errorf("unexpected payload size: got=%d want=%d", len(payload), wifiDisconnectFrameSize)
		}

		if !wifiDisconnectPayloadMatches(header.Kind, header.Serial, payload) {
			return records, fmt.Errorf("payload mismatch: kind=%d serial=%d", header.Kind, header.Serial)
		}

		records = append(records, wifiDisconnectFrameRecord{
			Source:     source,
			Header:     header,
			PayloadSize: len(payload),
			ReceivedAt:  time.Now(),
		})
	}

	return records, nil
}

func summarizeWiFiDisconnectRecords(
	records []wifiDisconnectFrameRecord,
) (wifiDisconnectSummary, error) {
	var summary wifiDisconnectSummary

	rgbSeen := make(map[uint64]bool)
	depthSeen := make(map[uint64]bool)

	var lastWiFiAt time.Time
	var firstBackupAt time.Time

	for _, record := range records {
		if record.Source == wifiDisconnectSourceWiFi {
			summary.WiFiFrames++

			if record.ReceivedAt.After(lastWiFiAt) {
				lastWiFiAt = record.ReceivedAt
			}
		}

		if record.Source == wifiDisconnectSourceBackup {
			summary.BackupFrames++

			if firstBackupAt.IsZero() || record.ReceivedAt.Before(firstBackupAt) {
				firstBackupAt = record.ReceivedAt
			}
		}

		expectedSource := wifiDisconnectSourceWiFi
		if record.Header.Serial > wifiDisconnectCutoverSerial {
			expectedSource = wifiDisconnectSourceBackup
		}

		if record.Source != expectedSource {
			summary.UnexpectedSource++
		}

		switch record.Header.Kind {
		case wifiDisconnectKindRGB:
			if rgbSeen[record.Header.Serial] {
				summary.Duplicates++
			}

			rgbSeen[record.Header.Serial] = true
			summary.RGBFrames++

			if record.Header.Serial > summary.LastRGBSerial {
				summary.LastRGBSerial = record.Header.Serial
			}

		case wifiDisconnectKindDepth:
			if depthSeen[record.Header.Serial] {
				summary.Duplicates++
			}

			depthSeen[record.Header.Serial] = true
			summary.DepthFrames++

			if record.Header.Serial > summary.LastDepthSerial {
				summary.LastDepthSerial = record.Header.Serial
			}

		default:
			return summary, fmt.Errorf("unknown frame kind in summary: %d", record.Header.Kind)
		}

		summary.PayloadBytes += int64(record.PayloadSize)
	}

	for serial := uint64(1); serial <= wifiDisconnectFrameCount; serial++ {
		if !rgbSeen[serial] {
			summary.Missing++
		}

		if !depthSeen[serial] {
			summary.Missing++
		}
	}

	if !lastWiFiAt.IsZero() && !firstBackupAt.IsZero() {
		gap := firstBackupAt.Sub(lastWiFiAt)
		if gap < 0 {
			gap = 0
		}

		summary.DisconnectGap = gap
	}

	return summary, nil
}

func validateWiFiDisconnectSummary(
	t *testing.T,
	summary wifiDisconnectSummary,
) {
	t.Helper()

	if summary.RGBFrames != wifiDisconnectFrameCount {
		t.Fatalf("unexpected RGB frame count: got=%d want=%d", summary.RGBFrames, wifiDisconnectFrameCount)
	}

	if summary.DepthFrames != wifiDisconnectFrameCount {
		t.Fatalf("unexpected Depth frame count: got=%d want=%d", summary.DepthFrames, wifiDisconnectFrameCount)
	}

	if summary.LastRGBSerial != wifiDisconnectFrameCount {
		t.Fatalf("unexpected last RGB serial: got=%d want=%d", summary.LastRGBSerial, wifiDisconnectFrameCount)
	}

	if summary.LastDepthSerial != wifiDisconnectFrameCount {
		t.Fatalf("unexpected last Depth serial: got=%d want=%d", summary.LastDepthSerial, wifiDisconnectFrameCount)
	}

	expectedWiFiFrames := wifiDisconnectCutoverSerial * 2
	if summary.WiFiFrames != expectedWiFiFrames {
		t.Fatalf("unexpected Wi-Fi frame count: got=%d want=%d", summary.WiFiFrames, expectedWiFiFrames)
	}

	expectedBackupFrames := (wifiDisconnectFrameCount - wifiDisconnectCutoverSerial) * 2
	if summary.BackupFrames != expectedBackupFrames {
		t.Fatalf("unexpected backup frame count: got=%d want=%d", summary.BackupFrames, expectedBackupFrames)
	}

	if summary.Missing != 0 {
		t.Fatalf("expected no missing frames, got=%d", summary.Missing)
	}

	if summary.Duplicates != 0 {
		t.Fatalf("expected no duplicate frames, got=%d", summary.Duplicates)
	}

	if summary.UnexpectedSource != 0 {
		t.Fatalf("expected all frames to arrive from expected source, got unexpected=%d", summary.UnexpectedSource)
	}

	expectedPayloadBytes := int64(wifiDisconnectFrameCount * 2 * wifiDisconnectFrameSize)
	if summary.PayloadBytes != expectedPayloadBytes {
		t.Fatalf("unexpected payload bytes: got=%d want=%d", summary.PayloadBytes, expectedPayloadBytes)
	}

	if summary.DisconnectGap > wifiDisconnectMaxAllowedGap {
		t.Fatalf("disconnect gap too large: got=%s limit=%s", summary.DisconnectGap, wifiDisconnectMaxAllowedGap)
	}
}

type wifiDisconnectFrameHeader struct {
	Kind   uint8
	Serial uint64
	Width  uint32
	Height uint32
	Size   uint32
}

const wifiDisconnectHeaderSize = 1 + 8 + 4 + 4 + 4

func writeWiFiDisconnectFrame(
	w io.Writer,
	header wifiDisconnectFrameHeader,
	payload []byte,
) error {
	if len(payload) != int(header.Size) {
		return fmt.Errorf("payload size mismatch: got=%d want=%d", len(payload), header.Size)
	}

	raw := make([]byte, wifiDisconnectHeaderSize)

	raw[0] = header.Kind
	binary.BigEndian.PutUint64(raw[1:9], header.Serial)
	binary.BigEndian.PutUint32(raw[9:13], header.Width)
	binary.BigEndian.PutUint32(raw[13:17], header.Height)
	binary.BigEndian.PutUint32(raw[17:21], header.Size)

	if err := writeFullWiFiDisconnect(w, raw); err != nil {
		return fmt.Errorf("write frame header: %w", err)
	}

	if err := writeFullWiFiDisconnect(w, payload); err != nil {
		return fmt.Errorf("write frame payload: %w", err)
	}

	return nil
}

func readWiFiDisconnectFrame(r io.Reader) (wifiDisconnectFrameHeader, []byte, error) {
	raw := make([]byte, wifiDisconnectHeaderSize)

	if _, err := io.ReadFull(r, raw); err != nil {
		if errors.Is(err, io.EOF) {
			return wifiDisconnectFrameHeader{}, nil, io.EOF
		}

		return wifiDisconnectFrameHeader{}, nil, fmt.Errorf("read frame header: %w", err)
	}

	header := wifiDisconnectFrameHeader{
		Kind:   raw[0],
		Serial: binary.BigEndian.Uint64(raw[1:9]),
		Width:  binary.BigEndian.Uint32(raw[9:13]),
		Height: binary.BigEndian.Uint32(raw[13:17]),
		Size:   binary.BigEndian.Uint32(raw[17:21]),
	}

	if header.Serial == 0 {
		return wifiDisconnectFrameHeader{}, nil, fmt.Errorf("invalid zero serial")
	}

	if header.Size == 0 {
		return wifiDisconnectFrameHeader{}, nil, fmt.Errorf("invalid zero frame size")
	}

	if header.Size > 10*1024*1024 {
		return wifiDisconnectFrameHeader{}, nil, fmt.Errorf("frame too large: %d", header.Size)
	}

	payload := make([]byte, header.Size)

	if _, err := io.ReadFull(r, payload); err != nil {
		return wifiDisconnectFrameHeader{}, nil, fmt.Errorf("read frame payload: %w", err)
	}

	return header, payload, nil
}

func readWiFiDisconnectStreamSource(r io.Reader) (uint8, error) {
	var raw [1]byte

	if _, err := io.ReadFull(r, raw[:]); err != nil {
		return 0, fmt.Errorf("read stream source: %w", err)
	}

	return raw[0], nil
}

type wifiDisconnectFrameRecord struct {
	Source     uint8
	Header     wifiDisconnectFrameHeader
	PayloadSize int
	ReceivedAt  time.Time
}

type wifiDisconnectStreamResult struct {
	Records []wifiDisconnectFrameRecord
	Err     error
}

type wifiDisconnectSummary struct {
	RGBFrames        int
	DepthFrames      int
	LastRGBSerial    uint64
	LastDepthSerial  uint64
	WiFiFrames       int
	BackupFrames     int
	Missing          int
	Duplicates       int
	UnexpectedSource int
	PayloadBytes     int64
	DisconnectGap    time.Duration
	Err              error
}

func makeWiFiDisconnectPayload(kind uint8, serial uint64, size int) []byte {
	payload := make([]byte, size)

	seed := byte(kind) ^ byte(serial) ^ 0xc3

	for i := range payload {
		payload[i] = seed + byte(i%251)
	}

	return payload
}

func wifiDisconnectPayloadMatches(kind uint8, serial uint64, payload []byte) bool {
	expected := makeWiFiDisconnectPayload(kind, serial, len(payload))
	return bytes.Equal(expected, payload)
}

func writeFullWiFiDisconnect(w io.Writer, data []byte) error {
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

func listenUDPForWiFiDisconnectTest(t *testing.T) *net.UDPConn {
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

func wifiDisconnectServerTLSConfig(t *testing.T) *tls.Config {
	t.Helper()

	return &tls.Config{
		Certificates: []tls.Certificate{
			generateWiFiDisconnectTLSCert(t),
		},
		NextProtos: []string{
			wifiDisconnectProto,
		},
		MinVersion: tls.VersionTLS13,
	}
}

func wifiDisconnectClientTLSConfig() *tls.Config {
	return &tls.Config{
		InsecureSkipVerify: true,
		NextProtos: []string{
			wifiDisconnectProto,
		},
		MinVersion: tls.VersionTLS13,
	}
}

func generateWiFiDisconnectTLSCert(t *testing.T) tls.Certificate {
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