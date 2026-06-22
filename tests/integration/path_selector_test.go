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
	pathSelectorTestProto   = "mp-quic-path-selector-test"
	pathSelectorTestTimeout = 10 * time.Second
)

func TestPathSelectorInvokedAfterAddPath(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), pathSelectorTestTimeout)
	defer cancel()

	serverUDP := listenUDPForPathSelectorTest(t)
	defer serverUDP.Close()

	secondUDP := listenUDPForPathSelectorTest(t)
	defer secondUDP.Close()

	selector := newPathSelectorRecorder(1)

	serverTLS := newIntegrationServerTLSConfig(t, pathSelectorTestProto)
	clientTLS := newIntegrationClientTLSConfig(t, pathSelectorTestProto)

	listener, err := quic.ListenEarly(serverUDP, serverTLS, &quic.Config{
		InitialMaxPathID: 1,
		MaxIdleTimeout:  5 * time.Second,
		PathSelector:    selector,
	})
	if err != nil {
		t.Fatalf("listen early failed: %v", err)
	}
	defer listener.Close()

	payload := []byte("path selector invocation payload")

	serverConnCh := make(chan quic.EarlyConnection, 1)
	serverDone := make(chan error, 1)

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

		got := make([]byte, len(payload))
		if _, err := io.ReadFull(stream, got); err != nil {
			serverDone <- fmt.Errorf("read payload: %w", err)
			return
		}

		if !bytes.Equal(got, payload) {
			serverDone <- fmt.Errorf("unexpected payload: got=%q want=%q", got, payload)
			return
		}

		if _, err := stream.Write(got); err != nil {
			serverDone <- fmt.Errorf("write echo: %w", err)
			return
		}

		if err := stream.Close(); err != nil {
			serverDone <- fmt.Errorf("close server stream: %w", err)
			return
		}

		serverDone <- nil
	}()

	clientUDP := listenUDPForPathSelectorTest(t)
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
		t.Fatalf("server failed before connection accepted: %v", err)
	case <-ctx.Done():
		t.Fatalf("timeout waiting for server connection: %v", ctx.Err())
	}

	secondAddr, ok := secondUDP.LocalAddr().(*net.UDPAddr)
	if !ok {
		t.Fatalf("expected second UDP addr, got %T", secondUDP.LocalAddr())
	}

	if err := serverConn.AddPath(secondAddr, 1); err != nil {
		t.Fatalf("AddPath failed: %v", err)
	}

	sendAndExpectEchoForPathSelectorTest(t, ctx, clientConn, payload)

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

	if selector.LastSelectedPathID() != 1 {
		t.Fatalf("expected selector to return path ID 1, got %d", selector.LastSelectedPathID())
	}

	t.Logf(
		"PathSelector called %d times, max observed path count=%d",
		selector.Calls(),
		selector.MaxObservedPathCount(),
	)
}

func TestPathSelectorReturningInitialPathKeepsTrafficAlive(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), pathSelectorTestTimeout)
	defer cancel()

	serverUDP := listenUDPForPathSelectorTest(t)
	defer serverUDP.Close()

	secondUDP := listenUDPForPathSelectorTest(t)
	defer secondUDP.Close()

	selector := newPathSelectorRecorder(0)

	serverTLS := newIntegrationServerTLSConfig(t, pathSelectorTestProto)
	clientTLS := newIntegrationClientTLSConfig(t, pathSelectorTestProto)

	listener, err := quic.ListenEarly(serverUDP, serverTLS, &quic.Config{
		InitialMaxPathID: 1,
		MaxIdleTimeout:  5 * time.Second,
		PathSelector:    selector,
	})
	if err != nil {
		t.Fatalf("listen early failed: %v", err)
	}
	defer listener.Close()

	payloads := [][]byte{
		[]byte("before-add-path"),
		[]byte("after-add-path-with-initial-path-selector"),
	}

	serverConnCh := make(chan quic.EarlyConnection, 1)
	serverDone := make(chan error, 1)

	go func() {
		serverConn, err := listener.Accept(ctx)
		if err != nil {
			serverDone <- fmt.Errorf("accept connection: %w", err)
			return
		}

		serverConnCh <- serverConn

		for _, expected := range payloads {
			stream, err := serverConn.AcceptStream(ctx)
			if err != nil {
				serverDone <- fmt.Errorf("accept stream: %w", err)
				return
			}

			got := make([]byte, len(expected))
			if _, err := io.ReadFull(stream, got); err != nil {
				serverDone <- fmt.Errorf("read payload: %w", err)
				return
			}

			if !bytes.Equal(got, expected) {
				serverDone <- fmt.Errorf("unexpected payload: got=%q want=%q", got, expected)
				return
			}

			if _, err := stream.Write(got); err != nil {
				serverDone <- fmt.Errorf("write echo: %w", err)
				return
			}

			if err := stream.Close(); err != nil {
				serverDone <- fmt.Errorf("close server stream: %w", err)
				return
			}
		}

		serverDone <- nil
	}()

	clientUDP := listenUDPForPathSelectorTest(t)
	defer clientUDP.Close()

	clientConn, err := quic.DialEarly(ctx, clientUDP, listener.Addr(), clientTLS, &quic.Config{
		InitialMaxPathID: 1,
		MaxIdleTimeout:  5 * time.Second,
	})
	if err != nil {
		t.Fatalf("dial early failed: %v", err)
	}
	defer clientConn.CloseWithError(0, "test done")

	sendAndExpectEchoForPathSelectorTest(t, ctx, clientConn, payloads[0])

	var serverConn quic.EarlyConnection

	select {
	case serverConn = <-serverConnCh:
	case err := <-serverDone:
		t.Fatalf("server failed before AddPath: %v", err)
	case <-ctx.Done():
		t.Fatalf("timeout waiting for server connection: %v", ctx.Err())
	}

	secondAddr, ok := secondUDP.LocalAddr().(*net.UDPAddr)
	if !ok {
		t.Fatalf("expected second UDP addr, got %T", secondUDP.LocalAddr())
	}

	if err := serverConn.AddPath(secondAddr, 1); err != nil {
		t.Fatalf("AddPath failed: %v", err)
	}

	sendAndExpectEchoForPathSelectorTest(t, ctx, clientConn, payloads[1])

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

	if selector.LastSelectedPathID() != 0 {
		t.Fatalf("expected selector to return path ID 0, got %d", selector.LastSelectedPathID())
	}
}

func TestPathSelectorRecorderRecordsCalls(t *testing.T) {
	selector := newPathSelectorRecorder(1)

	got := selector.SelectPath(nil)
	if got != 1 {
		t.Fatalf("expected selected path ID 1, got %d", got)
	}

	if selector.Calls() != 1 {
		t.Fatalf("expected call count 1, got %d", selector.Calls())
	}

	if selector.LastSelectedPathID() != 1 {
		t.Fatalf("expected last selected path ID 1, got %d", selector.LastSelectedPathID())
	}

	if selector.Name() == "" {
		t.Fatal("expected selector name to be non-empty")
	}
}

func TestPathSelectorRecorderRecordsPathCounts(t *testing.T) {
	selector := newPathSelectorRecorder(1)

	selector.SelectPath(make([]quic.PathState, 1))
	selector.SelectPath(make([]quic.PathState, 2))
	selector.SelectPath(make([]quic.PathState, 1))

	if selector.Calls() != 3 {
		t.Fatalf("expected call count 3, got %d", selector.Calls())
	}

	if selector.MaxObservedPathCount() != 2 {
		t.Fatalf("expected max observed path count 2, got %d", selector.MaxObservedPathCount())
	}
}

func TestPathSelectorRecorderConcurrentCalls(t *testing.T) {
	selector := newPathSelectorRecorder(1)

	var wg sync.WaitGroup

	for worker := 0; worker < 8; worker++ {
		wg.Add(1)

		go func() {
			defer wg.Done()

			for i := 0; i < 1000; i++ {
				if got := selector.SelectPath(make([]quic.PathState, 2)); got != 1 {
					t.Errorf("expected selected path ID 1, got %d", got)
					return
				}
			}
		}()
	}

	wg.Wait()

	if selector.Calls() != 8000 {
		t.Fatalf("expected call count 8000, got %d", selector.Calls())
	}

	if selector.MaxObservedPathCount() != 2 {
		t.Fatalf("expected max observed path count 2, got %d", selector.MaxObservedPathCount())
	}
}

type pathSelectorRecorder struct {
	target quic.PathID

	calls        int64
	lastSelected int64

	mu         sync.Mutex
	pathCounts []int
}

func newPathSelectorRecorder(target quic.PathID) *pathSelectorRecorder {
	return &pathSelectorRecorder{
		target:       target,
		lastSelected: -1,
	}
}

func (s *pathSelectorRecorder) SelectPath(paths []quic.PathState) quic.PathID {
	atomic.AddInt64(&s.calls, 1)
	atomic.StoreInt64(&s.lastSelected, int64(s.target))

	s.mu.Lock()
	s.pathCounts = append(s.pathCounts, len(paths))
	s.mu.Unlock()

	return s.target
}

func (s *pathSelectorRecorder) Name() string {
	return "path-selector-recorder"
}

func (s *pathSelectorRecorder) Calls() int64 {
	return atomic.LoadInt64(&s.calls)
}

func (s *pathSelectorRecorder) LastSelectedPathID() quic.PathID {
	return quic.PathID(atomic.LoadInt64(&s.lastSelected))
}

func (s *pathSelectorRecorder) MaxObservedPathCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()

	maxCount := 0
	for _, count := range s.pathCounts {
		if count > maxCount {
			maxCount = count
		}
	}

	return maxCount
}

func sendAndExpectEchoForPathSelectorTest(
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

func listenUDPForPathSelectorTest(t *testing.T) *net.UDPConn {
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