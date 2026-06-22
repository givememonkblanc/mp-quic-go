package integration

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"testing"
	"time"

	quic "github.com/quic-go/quic-go"
)

const (
	quicConnectTestProto   = "mp-quic-connect-test"
	quicConnectTestTimeout = 5 * time.Second
)

func TestQUICConnect(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), quicConnectTestTimeout)
	defer cancel()

	serverUDP := listenUDPForQUICConnectTest(t)
	defer serverUDP.Close()

	serverTLS := newIntegrationServerTLSConfig(t, quicConnectTestProto)
	clientTLS := newIntegrationClientTLSConfig(t, quicConnectTestProto)

	listener, err := quic.ListenEarly(serverUDP, serverTLS, &quic.Config{
		MaxIdleTimeout:  3 * time.Second,
		InitialMaxPathID: 1,
	})
	if err != nil {
		t.Fatalf("listen early failed: %v", err)
	}
	defer listener.Close()

	payload := []byte("hello quic connect test")

	serverDone := make(chan error, 1)

	go func() {
		conn, err := listener.Accept(ctx)
		if err != nil {
			serverDone <- fmt.Errorf("accept connection: %w", err)
			return
		}
		defer conn.CloseWithError(0, "server done")

		stream, err := conn.AcceptStream(ctx)
		if err != nil {
			serverDone <- fmt.Errorf("accept stream: %w", err)
			return
		}

		received := make([]byte, len(payload))
		if _, err := io.ReadFull(stream, received); err != nil {
			serverDone <- fmt.Errorf("read payload: %w", err)
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

	clientUDP := listenUDPForQUICConnectTest(t)
	defer clientUDP.Close()

	clientConn, err := quic.DialEarly(ctx, clientUDP, listener.Addr(), clientTLS, &quic.Config{
		MaxIdleTimeout:  3 * time.Second,
		InitialMaxPathID: 1,
	})
	if err != nil {
		t.Fatalf("dial early failed: %v", err)
	}
	defer clientConn.CloseWithError(0, "client done")

	sendAndExpectEchoForQUICConnectTest(t, ctx, clientConn, payload)

	select {
	case err := <-serverDone:
		if err != nil {
			t.Fatalf("server failed: %v", err)
		}
	case <-ctx.Done():
		t.Fatalf("timeout waiting for server completion: %v", ctx.Err())
	}
}

func TestQUICConnectMultipleStreams(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), quicConnectTestTimeout)
	defer cancel()

	serverUDP := listenUDPForQUICConnectTest(t)
	defer serverUDP.Close()

	serverTLS := newIntegrationServerTLSConfig(t, quicConnectTestProto)
	clientTLS := newIntegrationClientTLSConfig(t, quicConnectTestProto)

	listener, err := quic.ListenEarly(serverUDP, serverTLS, &quic.Config{
		MaxIdleTimeout:  3 * time.Second,
		InitialMaxPathID: 1,
	})
	if err != nil {
		t.Fatalf("listen early failed: %v", err)
	}
	defer listener.Close()

	payloads := [][]byte{
		[]byte("stream-1"),
		[]byte("stream-2"),
		[]byte("stream-3"),
	}

	serverDone := make(chan error, 1)

	go func() {
		conn, err := listener.Accept(ctx)
		if err != nil {
			serverDone <- fmt.Errorf("accept connection: %w", err)
			return
		}
		defer conn.CloseWithError(0, "server done")

		for _, expected := range payloads {
			stream, err := conn.AcceptStream(ctx)
			if err != nil {
				serverDone <- fmt.Errorf("accept stream: %w", err)
				return
			}

			got := make([]byte, len(expected))
			if _, err := io.ReadFull(stream, got); err != nil {
				serverDone <- fmt.Errorf("read stream payload: %w", err)
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
				serverDone <- fmt.Errorf("close stream: %w", err)
				return
			}
		}

		serverDone <- nil
	}()

	clientUDP := listenUDPForQUICConnectTest(t)
	defer clientUDP.Close()

	clientConn, err := quic.DialEarly(ctx, clientUDP, listener.Addr(), clientTLS, &quic.Config{
		MaxIdleTimeout:  3 * time.Second,
		InitialMaxPathID: 1,
	})
	if err != nil {
		t.Fatalf("dial early failed: %v", err)
	}
	defer clientConn.CloseWithError(0, "client done")

	for _, payload := range payloads {
		sendAndExpectEchoForQUICConnectTest(t, ctx, clientConn, payload)
	}

	select {
	case err := <-serverDone:
		if err != nil {
			t.Fatalf("server failed: %v", err)
		}
	case <-ctx.Done():
		t.Fatalf("timeout waiting for server completion: %v", ctx.Err())
	}
}

func TestQUICConnectServerAcceptTimesOutWithoutClient(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	serverUDP := listenUDPForQUICConnectTest(t)
	defer serverUDP.Close()

	serverTLS := newIntegrationServerTLSConfig(t, quicConnectTestProto)

	listener, err := quic.ListenEarly(serverUDP, serverTLS, &quic.Config{
		MaxIdleTimeout:  3 * time.Second,
		InitialMaxPathID: 1,
	})
	if err != nil {
		t.Fatalf("listen early failed: %v", err)
	}
	defer listener.Close()

	_, err = listener.Accept(ctx)
	if err == nil {
		t.Fatal("expected Accept to fail when context times out without client")
	}
}

func TestQUICConnectLocalUDPBind(t *testing.T) {
	conn := listenUDPForQUICConnectTest(t)
	defer conn.Close()

	addr, ok := conn.LocalAddr().(*net.UDPAddr)
	if !ok {
		t.Fatalf("expected UDP local addr, got %T", conn.LocalAddr())
	}

	if addr.Port == 0 {
		t.Fatalf("expected assigned UDP port, got %s", addr)
	}

	if !addr.IP.Equal(net.IPv4(127, 0, 0, 1)) {
		t.Fatalf("expected loopback IP, got %s", addr.IP)
	}
}

func sendAndExpectEchoForQUICConnectTest(
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

	n, err := stream.Write(payload)
	if err != nil {
		t.Fatalf("write payload failed: %v", err)
	}
	if n != len(payload) {
		t.Fatalf("short write: got=%d want=%d", n, len(payload))
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
}

func listenUDPForQUICConnectTest(t *testing.T) *net.UDPConn {
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