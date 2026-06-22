//go:build live
// +build live

package integration

import (
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"testing"
	"time"

	quic "github.com/quic-go/quic-go"
)

const (
	defaultLiveServerAddr = "127.0.0.1:4433"
	defaultLiveProto      = "mp-quic"
	defaultLiveServerName = "localhost"
	defaultLiveTimeout    = 10 * time.Second
)

func TestLiveServerConnect(t *testing.T) {
	cfg := liveTestConfigFromEnv(t)

	ctx, cancel := context.WithTimeout(context.Background(), cfg.timeout)
	defer cancel()

	localUDP := listenLiveUDP(t)
	defer localUDP.Close()

	remoteAddr, err := net.ResolveUDPAddr("udp", cfg.serverAddr)
	if err != nil {
		t.Fatalf("resolve live server addr %q failed: %v", cfg.serverAddr, err)
	}

	tlsConf := &tls.Config{
		InsecureSkipVerify: true,
		NextProtos: []string{
			cfg.nextProto,
		},
		ServerName: cfg.serverName,
		MinVersion: tls.VersionTLS13,
	}

	quicConf := &quic.Config{
		MaxIdleTimeout: 30 * time.Second,
	}

	session, err := quic.DialEarly(ctx, localUDP, remoteAddr, tlsConf, quicConf)
	if err != nil {
		t.Fatalf("dial live server %s failed: %v", cfg.serverAddr, err)
	}
	defer session.CloseWithError(0, "live test done")

	t.Logf("connected to live MP-QUIC server: %s", cfg.serverAddr)

	stream, err := session.OpenStreamSync(ctx)
	if err != nil {
		t.Fatalf("open stream failed: %v", err)
	}

	payload := []byte(cfg.message)

	n, err := stream.Write(payload)
	if err != nil {
		t.Fatalf("write live payload failed: %v", err)
	}
	if n != len(payload) {
		t.Fatalf("short write: got=%d want=%d", n, len(payload))
	}

	t.Logf("sent %d bytes: %q", len(payload), payload)

	if err := stream.Close(); err != nil {
		t.Fatalf("close write side failed: %v", err)
	}

	echo, readErr := readStreamWithTimeout(stream, cfg.readTimeout)

	if cfg.expectEcho {
		if readErr != nil {
			t.Fatalf("expected echo but read failed: %v", readErr)
		}

		if !bytes.Equal(echo, payload) {
			t.Fatalf("unexpected echo payload: got=%q want=%q", echo, payload)
		}

		t.Logf("received expected echo: %q", echo)
		return
	}

	if readErr != nil {
		t.Logf("read skipped/failed as expected when echo is not required: %v", readErr)
		return
	}

	t.Logf("received optional response %d bytes: %q", len(echo), echo)
}

type liveTestConfig struct {
	serverAddr string
	nextProto  string
	serverName string
	message    string

	timeout     time.Duration
	readTimeout time.Duration
	expectEcho  bool
}

func liveTestConfigFromEnv(t *testing.T) liveTestConfig {
	t.Helper()

	cfg := liveTestConfig{
		serverAddr:  getenvDefault("MP_QUIC_LIVE_ADDR", defaultLiveServerAddr),
		nextProto:   getenvDefault("MP_QUIC_LIVE_PROTO", defaultLiveProto),
		serverName:  getenvDefault("MP_QUIC_LIVE_SERVER_NAME", defaultLiveServerName),
		message:     getenvDefault("MP_QUIC_LIVE_MESSAGE", "hello from live test!"),
		timeout:     defaultLiveTimeout,
		readTimeout: 2 * time.Second,
		expectEcho:  false,
	}

	if raw := os.Getenv("MP_QUIC_LIVE_TIMEOUT"); raw != "" {
		duration, err := time.ParseDuration(raw)
		if err != nil {
			t.Fatalf("invalid MP_QUIC_LIVE_TIMEOUT %q: %v", raw, err)
		}
		cfg.timeout = duration
	}

	if raw := os.Getenv("MP_QUIC_LIVE_READ_TIMEOUT"); raw != "" {
		duration, err := time.ParseDuration(raw)
		if err != nil {
			t.Fatalf("invalid MP_QUIC_LIVE_READ_TIMEOUT %q: %v", raw, err)
		}
		cfg.readTimeout = duration
	}

	if raw := os.Getenv("MP_QUIC_LIVE_EXPECT_ECHO"); raw != "" {
		expectEcho, err := strconv.ParseBool(raw)
		if err != nil {
			t.Fatalf("invalid MP_QUIC_LIVE_EXPECT_ECHO %q: %v", raw, err)
		}
		cfg.expectEcho = expectEcho
	}

	return cfg
}

func listenLiveUDP(t *testing.T) *net.UDPConn {
	t.Helper()

	conn, err := net.ListenUDP("udp4", &net.UDPAddr{
		IP:   net.IPv4(127, 0, 0, 1),
		Port: 0,
	})
	if err != nil {
		t.Fatalf("listen local udp failed: %v", err)
	}

	return conn
}

func readStreamWithTimeout(stream quic.Stream, timeout time.Duration) ([]byte, error) {
	type result struct {
		data []byte
		err  error
	}

	ch := make(chan result, 1)

	go func() {
		buf := make([]byte, 4096)
		n, err := stream.Read(buf)
		if err != nil {
			ch <- result{err: err}
			return
		}

		ch <- result{
			data: append([]byte(nil), buf[:n]...),
			err:  nil,
		}
	}()

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case res := <-ch:
		if res.err != nil {
			if res.err == io.EOF {
				return nil, fmt.Errorf("stream closed before response: %w", res.err)
			}
			return nil, res.err
		}
		return res.data, nil

	case <-timer.C:
		return nil, fmt.Errorf("read timeout after %s", timeout)
	}
}

func getenvDefault(key, fallback string) string {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}

	return value
}