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
	wifiRecoveryProto   = "mp-quic-e2e-wifi-recovery"
	wifiRecoveryTimeout = 25 * time.Second

	wifiRecoveryWidth       = 640
	wifiRecoveryHeight      = 480
	wifiRecoveryBytesPerPix = 2
	wifiRecoveryFrameSize   = wifiRecoveryWidth * wifiRecoveryHeight * wifiRecoveryBytesPerPix

	wifiRecoveryFrameCount       = 24
	wifiRecoveryDisconnectSerial = 6
	wifiRecoveryRecoverSerial    = 16

	wifiRecoveryKindRGB   uint8 = 1
	wifiRecoveryKindDepth uint8 = 2

	wifiRecoverySourceWiFiInitial uint8 = 1
	wifiRecoverySourceBackup      uint8 = 2
	wifiRecoverySourceWiFiReturn  uint8 = 3

	wifiRecoverySimulatedOutage   = 100 * time.Millisecond
	wifiRecoverySimulatedRecovery = 100 * time.Millisecond

	wifiRecoveryMaxAllowedGap = 2 * time.Second
)

func TestScenarioWiFiRecovery(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), wifiRecoveryTimeout)
	defer cancel()

	serverUDP := listenUDPForWiFiRecoveryTest(t)
	defer serverUDP.Close()

	serverTLS := wifiRecoveryServerTLSConfig(t)
	clientTLS := wifiRecoveryClientTLSConfig()

	listener, err := quic.ListenEarly(serverUDP, serverTLS, &quic.Config{
		MaxIdleTimeout:   5 * time.Second,
		InitialMaxPathID: 1,
	})
	if err != nil {
		t.Fatalf("listen early failed: %v", err)
	}
	defer listener.Close()

	serverDone := make(chan wifiRecoverySummary, 1)

	go func() {
		summary, err := runWiFiRecoveryServer(ctx, listener)
		summary.Err = err
		serverDone <- summary
	}()

	clientUDP := listenUDPForWiFiRecoveryTest(t)
	defer clientUDP.Close()

	clientConn, err := quic.DialEarly(ctx, clientUDP, listener.Addr(), clientTLS, &quic.Config{
		MaxIdleTimeout:   5 * time.Second,
		InitialMaxPathID: 1,
	})
	if err != nil {
		t.Fatalf("dial early failed: %v", err)
	}
	defer clientConn.CloseWithError(0, "wifi recovery test done")

	start := time.Now()

	if err := runWiFiRecoveryClient(ctx, clientConn); err != nil {
		t.Fatalf("wifi recovery client failed: %v", err)
	}

	elapsed := time.Since(start)

	select {
	case summary := <-serverDone:
		if summary.Err != nil {
			t.Fatalf("wifi recovery server failed: %v", summary.Err)
		}

		validateWiFiRecoverySummary(t, summary)

		t.Logf(
			"wifi recovery scenario passed: rgb=%d depth=%d initialWiFi=%d backup=%d recoveredWiFi=%d disconnectGap=%s recoveryGap=%s bytes=%d elapsed=%s",
			summary.RGBFrames,
			summary.DepthFrames,
			summary.InitialWiFiFrames,
			summary.BackupFrames,
			summary.RecoveredWiFiFrames,
			summary.DisconnectGap,
			summary.RecoveryGap,
			summary.PayloadBytes,
			elapsed,
		)

	case <-ctx.Done():
		t.Fatalf("timeout waiting for wifi recovery server summary: %v", ctx.Err())
	}
}

func runWiFiRecoveryServer(
	ctx context.Context,
	listener *quic.EarlyListener,
) (wifiRecoverySummary, error) {
	conn, err := listener.Accept(ctx)
	if err != nil {
		return wifiRecoverySummary{}, fmt.Errorf("accept connection: %w", err)
	}
	defer conn.CloseWithError(0, "server done")

	resultCh := make(chan wifiRecoveryStreamResult, 3)

	var wg sync.WaitGroup

	for i := 0; i < 3; i++ {
		stream, err := conn.AcceptStream(ctx)
		if err != nil {
			return wifiRecoverySummary{}, fmt.Errorf("accept stream %d: %w", i, err)
		}

		wg.Add(1)

		go func(stream quic.Stream) {
			defer wg.Done()

			records, err := receiveWiFiRecoveryStream(stream)

			resultCh <- wifiRecoveryStreamResult{
				Records: records,
				Err:     err,
			}
		}(stream)
	}

	wg.Wait()
	close(resultCh)

	var allRecords []wifiRecoveryFrameRecord

	for result := range resultCh {
		if result.Err != nil {
			return wifiRecoverySummary{}, result.Err
		}

		allRecords = append(allRecords, result.Records...)
	}

	summary, err := summarizeWiFiRecoveryRecords(allRecords)
	if err != nil {
		return summary, err
	}

	return summary, nil
}

func runWiFiRecoveryClient(
	ctx context.Context,
	conn quic.EarlyConnection,
) error {
	wifiInitialStream, err := conn.OpenStreamSync(ctx)
	if err != nil {
		return fmt.Errorf("open initial wifi stream: %w", err)
	}

	if err := writeFullWiFiRecovery(wifiInitialStream, []byte{wifiRecoverySourceWiFiInitial}); err != nil {
		return fmt.Errorf("write initial wifi stream source: %w", err)
	}

	for serial := uint64(1); serial <= wifiRecoveryDisconnectSerial; serial++ {
		if err := sendWiFiRecoveryFramePair(wifiInitialStream, serial); err != nil {
			return fmt.Errorf("send initial wifi frame pair serial=%d: %w", serial, err)
		}
	}

	if err := wifiInitialStream.Close(); err != nil {
		return fmt.Errorf("close initial wifi stream: %w", err)
	}

	time.Sleep(wifiRecoverySimulatedOutage)

	backupStream, err := conn.OpenStreamSync(ctx)
	if err != nil {
		return fmt.Errorf("open backup stream: %w", err)
	}

	if err := writeFullWiFiRecovery(backupStream, []byte{wifiRecoverySourceBackup}); err != nil {
		return fmt.Errorf("write backup stream source: %w", err)
	}

	for serial := uint64(wifiRecoveryDisconnectSerial + 1); serial <= wifiRecoveryRecoverSerial; serial++ {
		if err := sendWiFiRecoveryFramePair(backupStream, serial); err != nil {
			return fmt.Errorf("send backup frame pair serial=%d: %w", serial, err)
		}
	}

	if err := backupStream.Close(); err != nil {
		return fmt.Errorf("close backup stream: %w", err)
	}

	time.Sleep(wifiRecoverySimulatedRecovery)

	wifiReturnStream, err := conn.OpenStreamSync(ctx)
	if err != nil {
		return fmt.Errorf("open recovered wifi stream: %w", err)
	}

	if err := writeFullWiFiRecovery(wifiReturnStream, []byte{wifiRecoverySourceWiFiReturn}); err != nil {
		return fmt.Errorf("write recovered wifi stream source: %w", err)
	}

	for serial := uint64(wifiRecoveryRecoverSerial + 1); serial <= wifiRecoveryFrameCount; serial++ {
		if err := sendWiFiRecoveryFramePair(wifiReturnStream, serial); err != nil {
			return fmt.Errorf("send recovered wifi frame pair serial=%d: %w", serial, err)
		}
	}

	if err := wifiReturnStream.Close(); err != nil {
		return fmt.Errorf("close recovered wifi stream: %w", err)
	}

	return nil
}

func sendWiFiRecoveryFramePair(
	stream quic.Stream,
	serial uint64,
) error {
	rgbPayload := makeWiFiRecoveryPayload(wifiRecoveryKindRGB, serial, wifiRecoveryFrameSize)

	if err := writeWiFiRecoveryFrame(stream, wifiRecoveryFrameHeader{
		Kind:   wifiRecoveryKindRGB,
		Serial: serial,
		Width:  wifiRecoveryWidth,
		Height: wifiRecoveryHeight,
		Size:   wifiRecoveryFrameSize,
	}, rgbPayload); err != nil {
		return fmt.Errorf("write RGB frame: %w", err)
	}

	depthPayload := makeWiFiRecoveryPayload(wifiRecoveryKindDepth, serial, wifiRecoveryFrameSize)

	if err := writeWiFiRecoveryFrame(stream, wifiRecoveryFrameHeader{
		Kind:   wifiRecoveryKindDepth,
		Serial: serial,
		Width:  wifiRecoveryWidth,
		Height: wifiRecoveryHeight,
		Size:   wifiRecoveryFrameSize,
	}, depthPayload); err != nil {
		return fmt.Errorf("write Depth frame: %w", err)
	}

	return nil
}

func receiveWiFiRecoveryStream(
	stream quic.Stream,
) ([]wifiRecoveryFrameRecord, error) {
	source, err := readWiFiRecoveryStreamSource(stream)
	if err != nil {
		return nil, err
	}

	if source != wifiRecoverySourceWiFiInitial &&
		source != wifiRecoverySourceBackup &&
		source != wifiRecoverySourceWiFiReturn {
		return nil, fmt.Errorf("unknown stream source: %d", source)
	}

	var records []wifiRecoveryFrameRecord

	for {
		header, payload, err := readWiFiRecoveryFrame(stream)
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}

			return records, err
		}

		if header.Kind != wifiRecoveryKindRGB && header.Kind != wifiRecoveryKindDepth {
			return records, fmt.Errorf("unknown frame kind: %d", header.Kind)
		}

		if header.Width != wifiRecoveryWidth {
			return records, fmt.Errorf("unexpected frame width: got=%d want=%d", header.Width, wifiRecoveryWidth)
		}

		if header.Height != wifiRecoveryHeight {
			return records, fmt.Errorf("unexpected frame height: got=%d want=%d", header.Height, wifiRecoveryHeight)
		}

		if header.Size != wifiRecoveryFrameSize {
			return records, fmt.Errorf("unexpected frame size: got=%d want=%d", header.Size, wifiRecoveryFrameSize)
		}

		if len(payload) != wifiRecoveryFrameSize {
			return records, fmt.Errorf("unexpected payload size: got=%d want=%d", len(payload), wifiRecoveryFrameSize)
		}

		if !wifiRecoveryPayloadMatches(header.Kind, header.Serial, payload) {
			return records, fmt.Errorf("payload mismatch: kind=%d serial=%d", header.Kind, header.Serial)
		}

		records = append(records, wifiRecoveryFrameRecord{
			Source:     source,
			Header:     header,
			PayloadSize: len(payload),
			ReceivedAt:  time.Now(),
		})
	}

	return records, nil
}

func summarizeWiFiRecoveryRecords(
	records []wifiRecoveryFrameRecord,
) (wifiRecoverySummary, error) {
	var summary wifiRecoverySummary

	rgbSeen := make(map[uint64]bool)
	depthSeen := make(map[uint64]bool)

	var lastInitialWiFiAt time.Time
	var firstBackupAt time.Time
	var lastBackupAt time.Time
	var firstRecoveredWiFiAt time.Time

	for _, record := range records {
		switch record.Source {
		case wifiRecoverySourceWiFiInitial:
			summary.InitialWiFiFrames++

			if record.ReceivedAt.After(lastInitialWiFiAt) {
				lastInitialWiFiAt = record.ReceivedAt
			}

		case wifiRecoverySourceBackup:
			summary.BackupFrames++

			if firstBackupAt.IsZero() || record.ReceivedAt.Before(firstBackupAt) {
				firstBackupAt = record.ReceivedAt
			}

			if record.ReceivedAt.After(lastBackupAt) {
				lastBackupAt = record.ReceivedAt
			}

		case wifiRecoverySourceWiFiReturn:
			summary.RecoveredWiFiFrames++

			if firstRecoveredWiFiAt.IsZero() || record.ReceivedAt.Before(firstRecoveredWiFiAt) {
				firstRecoveredWiFiAt = record.ReceivedAt
			}

		default:
			return summary, fmt.Errorf("unknown record source: %d", record.Source)
		}

		expectedSource := expectedWiFiRecoverySourceForSerial(record.Header.Serial)
		if record.Source != expectedSource {
			summary.UnexpectedSource++
		}

		switch record.Header.Kind {
		case wifiRecoveryKindRGB:
			if rgbSeen[record.Header.Serial] {
				summary.Duplicates++
			}

			rgbSeen[record.Header.Serial] = true
			summary.RGBFrames++

			if record.Header.Serial > summary.LastRGBSerial {
				summary.LastRGBSerial = record.Header.Serial
			}

		case wifiRecoveryKindDepth:
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

	for serial := uint64(1); serial <= wifiRecoveryFrameCount; serial++ {
		if !rgbSeen[serial] {
			summary.Missing++
		}

		if !depthSeen[serial] {
			summary.Missing++
		}
	}

	if !lastInitialWiFiAt.IsZero() && !firstBackupAt.IsZero() {
		gap := firstBackupAt.Sub(lastInitialWiFiAt)
		if gap < 0 {
			gap = 0
		}

		summary.DisconnectGap = gap
	}

	if !lastBackupAt.IsZero() && !firstRecoveredWiFiAt.IsZero() {
		gap := firstRecoveredWiFiAt.Sub(lastBackupAt)
		if gap < 0 {
			gap = 0
		}

		summary.RecoveryGap = gap
	}

	return summary, nil
}

func expectedWiFiRecoverySourceForSerial(serial uint64) uint8 {
	switch {
	case serial <= wifiRecoveryDisconnectSerial:
		return wifiRecoverySourceWiFiInitial

	case serial <= wifiRecoveryRecoverSerial:
		return wifiRecoverySourceBackup

	default:
		return wifiRecoverySourceWiFiReturn
	}
}

func validateWiFiRecoverySummary(
	t *testing.T,
	summary wifiRecoverySummary,
) {
	t.Helper()

	if summary.RGBFrames != wifiRecoveryFrameCount {
		t.Fatalf("unexpected RGB frame count: got=%d want=%d", summary.RGBFrames, wifiRecoveryFrameCount)
	}

	if summary.DepthFrames != wifiRecoveryFrameCount {
		t.Fatalf("unexpected Depth frame count: got=%d want=%d", summary.DepthFrames, wifiRecoveryFrameCount)
	}

	if summary.LastRGBSerial != wifiRecoveryFrameCount {
		t.Fatalf("unexpected last RGB serial: got=%d want=%d", summary.LastRGBSerial, wifiRecoveryFrameCount)
	}

	if summary.LastDepthSerial != wifiRecoveryFrameCount {
		t.Fatalf("unexpected last Depth serial: got=%d want=%d", summary.LastDepthSerial, wifiRecoveryFrameCount)
	}

	expectedInitialWiFiFrames := wifiRecoveryDisconnectSerial * 2
	if summary.InitialWiFiFrames != expectedInitialWiFiFrames {
		t.Fatalf("unexpected initial Wi-Fi frame count: got=%d want=%d", summary.InitialWiFiFrames, expectedInitialWiFiFrames)
	}

	expectedBackupFrames := (wifiRecoveryRecoverSerial - wifiRecoveryDisconnectSerial) * 2
	if summary.BackupFrames != expectedBackupFrames {
		t.Fatalf("unexpected backup frame count: got=%d want=%d", summary.BackupFrames, expectedBackupFrames)
	}

	expectedRecoveredWiFiFrames := (wifiRecoveryFrameCount - wifiRecoveryRecoverSerial) * 2
	if summary.RecoveredWiFiFrames != expectedRecoveredWiFiFrames {
		t.Fatalf("unexpected recovered Wi-Fi frame count: got=%d want=%d", summary.RecoveredWiFiFrames, expectedRecoveredWiFiFrames)
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

	expectedPayloadBytes := int64(wifiRecoveryFrameCount * 2 * wifiRecoveryFrameSize)
	if summary.PayloadBytes != expectedPayloadBytes {
		t.Fatalf("unexpected payload bytes: got=%d want=%d", summary.PayloadBytes, expectedPayloadBytes)
	}

	if summary.DisconnectGap > wifiRecoveryMaxAllowedGap {
		t.Fatalf("disconnect gap too large: got=%s limit=%s", summary.DisconnectGap, wifiRecoveryMaxAllowedGap)
	}

	if summary.RecoveryGap > wifiRecoveryMaxAllowedGap {
		t.Fatalf("recovery gap too large: got=%s limit=%s", summary.RecoveryGap, wifiRecoveryMaxAllowedGap)
	}
}

type wifiRecoveryFrameHeader struct {
	Kind   uint8
	Serial uint64
	Width  uint32
	Height uint32
	Size   uint32
}

const wifiRecoveryHeaderSize = 1 + 8 + 4 + 4 + 4

func writeWiFiRecoveryFrame(
	w io.Writer,
	header wifiRecoveryFrameHeader,
	payload []byte,
) error {
	if len(payload) != int(header.Size) {
		return fmt.Errorf("payload size mismatch: got=%d want=%d", len(payload), header.Size)
	}

	raw := make([]byte, wifiRecoveryHeaderSize)

	raw[0] = header.Kind
	binary.BigEndian.PutUint64(raw[1:9], header.Serial)
	binary.BigEndian.PutUint32(raw[9:13], header.Width)
	binary.BigEndian.PutUint32(raw[13:17], header.Height)
	binary.BigEndian.PutUint32(raw[17:21], header.Size)

	if err := writeFullWiFiRecovery(w, raw); err != nil {
		return fmt.Errorf("write frame header: %w", err)
	}

	if err := writeFullWiFiRecovery(w, payload); err != nil {
		return fmt.Errorf("write frame payload: %w", err)
	}

	return nil
}

func readWiFiRecoveryFrame(r io.Reader) (wifiRecoveryFrameHeader, []byte, error) {
	raw := make([]byte, wifiRecoveryHeaderSize)

	if _, err := io.ReadFull(r, raw); err != nil {
		if errors.Is(err, io.EOF) {
			return wifiRecoveryFrameHeader{}, nil, io.EOF
		}

		return wifiRecoveryFrameHeader{}, nil, fmt.Errorf("read frame header: %w", err)
	}

	header := wifiRecoveryFrameHeader{
		Kind:   raw[0],
		Serial: binary.BigEndian.Uint64(raw[1:9]),
		Width:  binary.BigEndian.Uint32(raw[9:13]),
		Height: binary.BigEndian.Uint32(raw[13:17]),
		Size:   binary.BigEndian.Uint32(raw[17:21]),
	}

	if header.Serial == 0 {
		return wifiRecoveryFrameHeader{}, nil, fmt.Errorf("invalid zero serial")
	}

	if header.Size == 0 {
		return wifiRecoveryFrameHeader{}, nil, fmt.Errorf("invalid zero frame size")
	}

	if header.Size > 10*1024*1024 {
		return wifiRecoveryFrameHeader{}, nil, fmt.Errorf("frame too large: %d", header.Size)
	}

	payload := make([]byte, header.Size)

	if _, err := io.ReadFull(r, payload); err != nil {
		return wifiRecoveryFrameHeader{}, nil, fmt.Errorf("read frame payload: %w", err)
	}

	return header, payload, nil
}

func readWiFiRecoveryStreamSource(r io.Reader) (uint8, error) {
	var raw [1]byte

	if _, err := io.ReadFull(r, raw[:]); err != nil {
		return 0, fmt.Errorf("read stream source: %w", err)
	}

	return raw[0], nil
}

type wifiRecoveryFrameRecord struct {
	Source     uint8
	Header     wifiRecoveryFrameHeader
	PayloadSize int
	ReceivedAt  time.Time
}

type wifiRecoveryStreamResult struct {
	Records []wifiRecoveryFrameRecord
	Err     error
}

type wifiRecoverySummary struct {
	RGBFrames           int
	DepthFrames         int
	LastRGBSerial       uint64
	LastDepthSerial     uint64
	InitialWiFiFrames   int
	BackupFrames        int
	RecoveredWiFiFrames int
	Missing             int
	Duplicates          int
	UnexpectedSource    int
	PayloadBytes        int64
	DisconnectGap       time.Duration
	RecoveryGap         time.Duration
	Err                 error
}

func makeWiFiRecoveryPayload(kind uint8, serial uint64, size int) []byte {
	payload := make([]byte, size)

	seed := byte(kind) ^ byte(serial) ^ 0xd4

	for i := range payload {
		payload[i] = seed + byte(i%251)
	}

	return payload
}

func wifiRecoveryPayloadMatches(kind uint8, serial uint64, payload []byte) bool {
	expected := makeWiFiRecoveryPayload(kind, serial, len(payload))
	return bytes.Equal(expected, payload)
}

func writeFullWiFiRecovery(w io.Writer, data []byte) error {
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

func listenUDPForWiFiRecoveryTest(t *testing.T) *net.UDPConn {
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

func wifiRecoveryServerTLSConfig(t *testing.T) *tls.Config {
	t.Helper()

	return &tls.Config{
		Certificates: []tls.Certificate{
			generateWiFiRecoveryTLSCert(t),
		},
		NextProtos: []string{
			wifiRecoveryProto,
		},
		MinVersion: tls.VersionTLS13,
	}
}

func wifiRecoveryClientTLSConfig() *tls.Config {
	return &tls.Config{
		InsecureSkipVerify: true,
		NextProtos: []string{
			wifiRecoveryProto,
		},
		MinVersion: tls.VersionTLS13,
	}
}

func generateWiFiRecoveryTLSCert(t *testing.T) tls.Certificate {
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