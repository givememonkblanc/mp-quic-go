package integration

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"sync"
	"testing"
	"time"

	quic "github.com/quic-go/quic-go"
)

const (
	streamSendReceiveProto   = "mp-quic-stream-send-receive-test"
	streamSendReceiveTimeout = 10 * time.Second
)

func TestStreamSendReceive(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), streamSendReceiveTimeout)
	defer cancel()

	serverAddr, serverDone, cleanupServer := startStreamEchoServerForStreamTest(t, ctx, 1)
	defer cleanupServer()

	clientConn, cleanupClient := dialStreamTestClient(t, ctx, serverAddr)
	defer cleanupClient()

	payload := []byte("hello stream send receive")

	sendAndExpectStreamEcho(t, ctx, clientConn, payload)

	waitStreamTestServerDone(t, ctx, serverDone)
}

func TestStreamSendReceiveBinaryPayload(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), streamSendReceiveTimeout)
	defer cancel()

	serverAddr, serverDone, cleanupServer := startStreamEchoServerForStreamTest(t, ctx, 1)
	defer cleanupServer()

	clientConn, cleanupClient := dialStreamTestClient(t, ctx, serverAddr)
	defer cleanupClient()

	payload := make([]byte, 4096)
	for i := range payload {
		payload[i] = byte(i % 256)
	}

	sendAndExpectStreamEcho(t, ctx, clientConn, payload)

	waitStreamTestServerDone(t, ctx, serverDone)
}

func TestStreamSendReceiveLargePayload(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), streamSendReceiveTimeout)
	defer cancel()

	serverAddr, serverDone, cleanupServer := startStreamEchoServerForStreamTest(t, ctx, 1)
	defer cleanupServer()

	clientConn, cleanupClient := dialStreamTestClient(t, ctx, serverAddr)
	defer cleanupClient()

	payload := make([]byte, 256*1024)
	for i := range payload {
		payload[i] = byte((i * 31) % 251)
	}

	sendAndExpectStreamEcho(t, ctx, clientConn, payload)

	waitStreamTestServerDone(t, ctx, serverDone)
}

func TestStreamSendReceiveEmptyPayload(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), streamSendReceiveTimeout)
	defer cancel()

	serverAddr, serverDone, cleanupServer := startStreamEchoServerForStreamTest(t, ctx, 1)
	defer cleanupServer()

	clientConn, cleanupClient := dialStreamTestClient(t, ctx, serverAddr)
	defer cleanupClient()

	sendAndExpectStreamEcho(t, ctx, clientConn, nil)

	waitStreamTestServerDone(t, ctx, serverDone)
}

func TestStreamSendReceiveMultipleSequentialStreams(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), streamSendReceiveTimeout)
	defer cancel()

	payloads := [][]byte{
		[]byte("stream-1"),
		[]byte("stream-2"),
		[]byte("stream-3"),
		[]byte("stream-4"),
		[]byte("stream-5"),
	}

	serverAddr, serverDone, cleanupServer := startStreamEchoServerForStreamTest(t, ctx, len(payloads))
	defer cleanupServer()

	clientConn, cleanupClient := dialStreamTestClient(t, ctx, serverAddr)
	defer cleanupClient()

	for _, payload := range payloads {
		sendAndExpectStreamEcho(t, ctx, clientConn, payload)
	}

	waitStreamTestServerDone(t, ctx, serverDone)
}

func TestStreamSendReceiveParallelStreams(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), streamSendReceiveTimeout)
	defer cancel()

	const streamCount = 16

	serverAddr, serverDone, cleanupServer := startStreamEchoServerForStreamTest(t, ctx, streamCount)
	defer cleanupServer()

	clientConn, cleanupClient := dialStreamTestClient(t, ctx, serverAddr)
	defer cleanupClient()

	var wg sync.WaitGroup
	errCh := make(chan error, streamCount)

	for i := 0; i < streamCount; i++ {
		wg.Add(1)

		go func(index int) {
			defer wg.Done()

			payload := []byte(fmt.Sprintf("parallel-stream-payload-%02d", index))

			if err := sendAndExpectStreamEchoErr(ctx, clientConn, payload); err != nil {
				errCh <- fmt.Errorf("stream %d failed: %w", index, err)
			}
		}(i)
	}

	wg.Wait()
	close(errCh)

	for err := range errCh {
		if err != nil {
			t.Fatal(err)
		}
	}

	waitStreamTestServerDone(t, ctx, serverDone)
}

func TestStreamSendReceiveClientCloseBeforeServerAcceptFinishes(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), streamSendReceiveTimeout)
	defer cancel()

	serverAddr, serverDone, cleanupServer := startStreamEchoServerForStreamTest(t, ctx, 1)
	defer cleanupServer()

	clientConn, cleanupClient := dialStreamTestClient(t, ctx, serverAddr)
	defer cleanupClient()

	stream, err := clientConn.OpenStreamSync(ctx)
	if err != nil {
		t.Fatalf("open stream failed: %v", err)
	}

	payload := []byte("client closes write side after payload")

	if _, err := stream.Write(payload); err != nil {
		t.Fatalf("write payload failed: %v", err)
	}

	if err := stream.Close(); err != nil {
		t.Fatalf("close client write side failed: %v", err)
	}

	echo, err := io.ReadAll(stream)
	if err != nil {
		t.Fatalf("read echo failed: %v", err)
	}

	if !bytes.Equal(echo, payload) {
		t.Fatalf("unexpected echo: got=%q want=%q", echo, payload)
	}

	waitStreamTestServerDone(t, ctx, serverDone)
}

func startStreamEchoServerForStreamTest(
	t *testing.T,
	ctx context.Context,
	streamCount int,
) (net.Addr, <-chan error, func()) {
	t.Helper()

	serverUDP := listenUDPForStreamSendReceiveTest(t)

	serverTLS := newIntegrationServerTLSConfig(t, streamSendReceiveProto)

	listener, err := quic.ListenEarly(serverUDP, serverTLS, &quic.Config{
		MaxIdleTimeout:   5 * time.Second,
		InitialMaxPathID: 1,
	})
	if err != nil {
		serverUDP.Close()
		t.Fatalf("listen early failed: %v", err)
	}

	done := make(chan error, 1)

	go func() {
		conn, err := listener.Accept(ctx)
		if err != nil {
			done <- fmt.Errorf("accept connection: %w", err)
			return
		}
		defer conn.CloseWithError(0, "server done")

		errCh := make(chan error, streamCount)
		var wg sync.WaitGroup

		for i := 0; i < streamCount; i++ {
			stream, err := conn.AcceptStream(ctx)
			if err != nil {
				done <- fmt.Errorf("accept stream %d: %w", i, err)
				return
			}

			wg.Add(1)

			go func(index int, stream quic.Stream) {
				defer wg.Done()

				if err := echoOneStreamForStreamTest(stream); err != nil {
					errCh <- fmt.Errorf("echo stream %d: %w", index, err)
				}
			}(i, stream)
		}

		wg.Wait()
		close(errCh)

		for err := range errCh {
			if err != nil {
				done <- err
				return
			}
		}

		done <- nil
	}()

	cleanup := func() {
		_ = listener.Close()
		_ = serverUDP.Close()
	}

	return listener.Addr(), done, cleanup
}

func echoOneStreamForStreamTest(stream quic.Stream) error {
	payload, err := io.ReadAll(stream)
	if err != nil {
		return fmt.Errorf("read stream payload: %w", err)
	}

	n, err := stream.Write(payload)
	if err != nil {
		return fmt.Errorf("write echo: %w", err)
	}
	if n != len(payload) {
		return fmt.Errorf("short echo write: got=%d want=%d", n, len(payload))
	}

	if err := stream.Close(); err != nil {
		return fmt.Errorf("close server stream: %w", err)
	}

	return nil
}

func dialStreamTestClient(
	t *testing.T,
	ctx context.Context,
	serverAddr net.Addr,
) (quic.EarlyConnection, func()) {
	t.Helper()

	clientUDP := listenUDPForStreamSendReceiveTest(t)

	clientTLS := newIntegrationClientTLSConfig(t, streamSendReceiveProto)

	clientConn, err := quic.DialEarly(ctx, clientUDP, serverAddr, clientTLS, &quic.Config{
		MaxIdleTimeout:   5 * time.Second,
		InitialMaxPathID: 1,
	})
	if err != nil {
		_ = clientUDP.Close()
		t.Fatalf("dial early failed: %v", err)
	}

	cleanup := func() {
		_ = clientConn.CloseWithError(0, "client done")
		_ = clientUDP.Close()
	}

	return clientConn, cleanup
}

func sendAndExpectStreamEcho(
	t *testing.T,
	ctx context.Context,
	conn quic.EarlyConnection,
	payload []byte,
) {
	t.Helper()

	if err := sendAndExpectStreamEchoErr(ctx, conn, payload); err != nil {
		t.Fatal(err)
	}
}

func sendAndExpectStreamEchoErr(
	ctx context.Context,
	conn quic.EarlyConnection,
	payload []byte,
) error {
	stream, err := conn.OpenStreamSync(ctx)
	if err != nil {
		return fmt.Errorf("open stream: %w", err)
	}

	n, err := stream.Write(payload)
	if err != nil {
		return fmt.Errorf("write payload: %w", err)
	}
	if n != len(payload) {
		return fmt.Errorf("short write: got=%d want=%d", n, len(payload))
	}

	if err := stream.Close(); err != nil {
		return fmt.Errorf("close client write side: %w", err)
	}

	echo, err := io.ReadAll(stream)
	if err != nil {
		return fmt.Errorf("read echo: %w", err)
	}

	if !bytes.Equal(echo, payload) {
		return fmt.Errorf("unexpected echo: got=%q want=%q", echo, payload)
	}

	return nil
}

func waitStreamTestServerDone(
	t *testing.T,
	ctx context.Context,
	serverDone <-chan error,
) {
	t.Helper()

	select {
	case err := <-serverDone:
		if err != nil {
			t.Fatalf("server failed: %v", err)
		}

	case <-ctx.Done():
		t.Fatalf("timeout waiting for stream test server: %v", ctx.Err())
	}
}

func listenUDPForStreamSendReceiveTest(t *testing.T) *net.UDPConn {
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