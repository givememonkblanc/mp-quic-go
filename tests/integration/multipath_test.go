package integration

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	quic "github.com/quic-go/quic-go"
)

const (
	multipathTestProto   = "mp-quic-multipath-test"
	multipathTestTimeout = 10 * time.Second
)

// TestMultiPathAddAndSend validates that:
// 1. A QUIC connection can be established.
// 2. The server can add an additional path with AddPath.
// 3. A configured PathSelector is invoked after path addition.
// 4. Application stream traffic still works after AddPath.
//
// This is a smoke/integration test. It does not strictly prove that application
// payload was transmitted over the second path. That should be tested separately
// with a path-level instrumentation test.
func TestMultiPathAddAndSend(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), multipathTestTimeout)
	defer cancel()

	serverUDP := listenUDPForMultipathTest(t)
	defer serverUDP.Close()

	secondUDP := listenUDPForMultipathTest(t)
	defer secondUDP.Close()

	selector := &recordingFixedPathSelector{
		id: 1,
	}

	serverTLS := newIntegrationServerTLSConfig(t, multipathTestProto)
	clientTLS := newIntegrationClientTLSConfig(t, multipathTestProto)

	quicConfig := &quic.Config{
		InitialMaxPathID: 1,
		MaxIdleTimeout:  5 * time.Second,
		PathSelector:    selector,
	}

	listener, err := quic.ListenEarly(serverUDP, serverTLS, quicConfig)
	if err != nil {
		t.Fatalf("listen early failed: %v", err)
	}
	defer listener.Close()

	payload := []byte("hello from client via multipath integration test")

	serverDone := make(chan error, 1)
	serverConnCh := make(chan quic.EarlyConnection, 1)

	go func() {
		serverConn, err := listener.Accept(ctx)
		if err != nil {
			serverDone <- fmt.Errorf("accept connection: %w", err)
			return
		}

		serverConnCh <- serverConn

		stream, err := serverConn.AcceptStream(ctx)
		if err != nil {
			serverDone <- fmt.Errorf("accept stream: %w", err)
			return
		}

		received := make([]byte, len(payload))
		if _, err := io.ReadFull(stream, received); err != nil {
			serverDone <- fmt.Errorf("read stream payload: %w", err)
			return
		}

		if !bytes.Equal(received, payload) {
			serverDone <- fmt.Errorf("unexpected payload: got=%q want=%q", received, payload)
			return
		}

		if _, err := stream.Write(received); err != nil {
			serverDone <- fmt.Errorf("write echo: %w", err)
			return
		}

		if err := stream.Close(); err != nil {
			serverDone <- fmt.Errorf("close server stream: %w", err)
			return
		}

		serverDone <- nil
	}()

	clientUDP := listenUDPForMultipathTest(t)
	defer clientUDP.Close()

	clientConn, err := quic.DialEarly(ctx, clientUDP, listener.Addr(), clientTLS, &quic.Config{
		InitialMaxPathID: 1,
		MaxIdleTimeout:  5 * time.Second,
	})
	if err != nil {
		t.Fatalf("dial early failed: %v", err)
	}
	defer clientConn.CloseWithError(0, "test done")

	var serverConn quic.EarlyConnection

	select {
	case serverConn = <-serverConnCh:
	case err := <-serverDone:
		t.Fatalf("server failed before connection was accepted: %v", err)
	case <-ctx.Done():
		t.Fatalf("timeout waiting for server connection: %v", ctx.Err())
	}

	secondPathObserver := observeSecondUDPForMultipathTest(t, secondUDP, 1*time.Second)

	secondAddr, ok := secondUDP.LocalAddr().(*net.UDPAddr)
	if !ok {
		t.Fatalf("expected UDP addr, got %T", secondUDP.LocalAddr())
	}

	if err := serverConn.AddPath(secondAddr, 1); err != nil {
		t.Fatalf("server AddPath failed: %v", err)
	}

	t.Logf("server added path 1 -> %s", secondAddr)

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
		t.Fatalf("unexpected echo: got=%q want=%q", echo, payload)
	}

	if err := stream.Close(); err != nil {
		t.Fatalf("close client stream failed: %v", err)
	}

	select {
	case err := <-serverDone:
		if err != nil {
			t.Fatalf("server failed: %v", err)
		}
	case <-ctx.Done():
		t.Fatalf("timeout waiting for server completion: %v", ctx.Err())
	}

	if selector.Calls() == 0 {
		t.Fatal("expected PathSelector to be called at least once")
	}

	t.Logf("PathSelector was called %d times", selector.Calls())

	select {
	case obs := <-secondPathObserver:
		if obs.err != nil {
			t.Logf("no packet observed on second UDP socket during smoke test: %v", obs.err)
		} else {
			t.Logf("observed %d bytes on second UDP socket", obs.n)
		}
	default:
		t.Log("second UDP observation did not finish before test completion")
	}
}

type recordingFixedPathSelector struct {
	id    quic.PathID
	calls int64
}

func (s *recordingFixedPathSelector) SelectPath(paths []quic.PathState) quic.PathID {
	atomic.AddInt64(&s.calls, 1)
	return s.id
}

func (s *recordingFixedPathSelector) Name() string {
	return "recording-fixed-path"
}

func (s *recordingFixedPathSelector) Calls() int64 {
	return atomic.LoadInt64(&s.calls)
}

type multipathUDPObservation struct {
	n   int
	err error
}

func observeSecondUDPForMultipathTest(
	t *testing.T,
	conn *net.UDPConn,
	timeout time.Duration,
) <-chan multipathUDPObservation {
	t.Helper()

	ch := make(chan multipathUDPObservation, 1)

	go func() {
		buf := make([]byte, 2048)

		_ = conn.SetReadDeadline(time.Now().Add(timeout))

		n, _, err := conn.ReadFromUDP(buf)
		ch <- multipathUDPObservation{
			n:   n,
			err: err,
		}
	}()

	return ch
}

func listenUDPForMultipathTest(t *testing.T) *net.UDPConn {
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

// TestMultiPathAddPathDoesNotBreakExistingStream verifies that AddPath can be
// called while a connection is alive and the original path still carries stream traffic.
func TestMultiPathAddPathDoesNotBreakExistingStream(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), multipathTestTimeout)
	defer cancel()

	serverUDP := listenUDPForMultipathTest(t)
	defer serverUDP.Close()

	secondUDP := listenUDPForMultipathTest(t)
	defer secondUDP.Close()

	serverTLS := newIntegrationServerTLSConfig(t, multipathTestProto)
	clientTLS := newIntegrationClientTLSConfig(t, multipathTestProto)

	listener, err := quic.ListenEarly(serverUDP, serverTLS, &quic.Config{
		InitialMaxPathID: 1,
		MaxIdleTimeout:  5 * time.Second,
	})
	if err != nil {
		t.Fatalf("listen early failed: %v", err)
	}
	defer listener.Close()

	payloads := [][]byte{
		[]byte("before-add-path"),
		[]byte("after-add-path"),
	}

	serverConnCh := make(chan quic.EarlyConnection, 1)
	serverErrCh := make(chan error, 1)

	go func() {
		serverConn, err := listener.Accept(ctx)
		if err != nil {
			serverErrCh <- fmt.Errorf("accept connection: %w", err)
			return
		}

		serverConnCh <- serverConn

		for _, expected := range payloads {
			stream, err := serverConn.AcceptStream(ctx)
			if err != nil {
				serverErrCh <- fmt.Errorf("accept stream: %w", err)
				return
			}

			got := make([]byte, len(expected))
			if _, err := io.ReadFull(stream, got); err != nil {
				serverErrCh <- fmt.Errorf("read stream: %w", err)
				return
			}

			if !bytes.Equal(got, expected) {
				serverErrCh <- fmt.Errorf("unexpected payload: got=%q want=%q", got, expected)
				return
			}

			if _, err := stream.Write(got); err != nil {
				serverErrCh <- fmt.Errorf("write echo: %w", err)
				return
			}

			if err := stream.Close(); err != nil {
				serverErrCh <- fmt.Errorf("close stream: %w", err)
				return
			}
		}

		serverErrCh <- nil
	}()

	clientUDP := listenUDPForMultipathTest(t)
	defer clientUDP.Close()

	clientConn, err := quic.DialEarly(ctx, clientUDP, listener.Addr(), clientTLS, &quic.Config{
		InitialMaxPathID: 1,
		MaxIdleTimeout:  5 * time.Second,
	})
	if err != nil {
		t.Fatalf("dial early failed: %v", err)
	}
	defer clientConn.CloseWithError(0, "test done")

	sendAndExpectEcho(t, ctx, clientConn, payloads[0])

	var serverConn quic.EarlyConnection

	select {
	case serverConn = <-serverConnCh:
	case err := <-serverErrCh:
		t.Fatalf("server failed before AddPath: %v", err)
	case <-ctx.Done():
		t.Fatalf("timeout waiting for server connection: %v", ctx.Err())
	}

	secondAddr, ok := secondUDP.LocalAddr().(*net.UDPAddr)
	if !ok {
		t.Fatalf("expected UDP addr, got %T", secondUDP.LocalAddr())
	}

	if err := serverConn.AddPath(secondAddr, 1); err != nil {
		t.Fatalf("server AddPath failed: %v", err)
	}

	sendAndExpectEcho(t, ctx, clientConn, payloads[1])

	select {
	case err := <-serverErrCh:
		if err != nil {
			t.Fatalf("server failed: %v", err)
		}
	case <-ctx.Done():
		t.Fatalf("timeout waiting for server completion: %v", ctx.Err())
	}
}

func sendAndExpectEcho(
	t *testing.T,
	ctx context.Context,
	conn quic.EarlyConnection,
	payload []byte,
) {
	t.Helper()

	stream, err := conn.OpenStreamSync(ctx)
	if err != nil {
		t.Fatalf("open stream failed: %v", err)
	}

	if _, err := stream.Write(payload); err != nil {
		t.Fatalf("write payload failed: %v", err)
	}

	echo := make([]byte, len(payload))
	if _, err := io.ReadFull(stream, echo); err != nil {
		t.Fatalf("read echo failed: %v", err)
	}

	if !bytes.Equal(echo, payload) {
		t.Fatalf("unexpected echo: got=%q want=%q", echo, payload)
	}

	if err := stream.Close(); err != nil {
		t.Fatalf("close stream failed: %v", err)
	}
}

func TestMultiPathSecondUDPObserverDoesNotBlock(t *testing.T) {
	conn := listenUDPForMultipathTest(t)
	defer conn.Close()

	obsCh := observeSecondUDPForMultipathTest(t, conn, 50*time.Millisecond)

	select {
	case <-obsCh:
	case <-time.After(time.Second):
		t.Fatal("observer blocked")
	}
}

func TestRecordingFixedPathSelector(t *testing.T) {
	selector := &recordingFixedPathSelector{
		id: 1,
	}

	got := selector.SelectPath(nil)
	if got != 1 {
		t.Fatalf("expected selected path 1, got %d", got)
	}

	if selector.Calls() != 1 {
		t.Fatalf("expected selector call count 1, got %d", selector.Calls())
	}

	if selector.Name() == "" {
		t.Fatal("expected selector name to be non-empty")
	}
}

func TestRecordingFixedPathSelectorConcurrentCalls(t *testing.T) {
	selector := &recordingFixedPathSelector{
		id: 1,
	}

	var wg sync.WaitGroup

	for worker := 0; worker < 8; worker++ {
		wg.Add(1)

		go func() {
			defer wg.Done()

			for i := 0; i < 1000; i++ {
				if got := selector.SelectPath(nil); got != 1 {
					t.Errorf("expected selected path 1, got %d", got)
					return
				}
			}
		}()
	}

	wg.Wait()

	if selector.Calls() != 8000 {
		t.Fatalf("expected selector call count 8000, got %d", selector.Calls())
	}
}