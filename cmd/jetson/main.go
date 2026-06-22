package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/binary"
	"flag"
	"image"
	"image/color"
	"image/jpeg"
	"io"
	"log"
	"net"
	"sync"
	"time"

	"github.com/quic-go/quic-go"
	"github.com/quic-go/quic-go/qlog"
	"mp-quic-go/internal/camera"
	"mp-quic-go/internal/handler"
	"mp-quic-go/pkg/protocols"
)

var (
	serverAddr    = flag.String("addr", "localhost:4433", "QUIC server address")
	secondAddr    = flag.String("path1", "", "Second server address for path 1 (e.g., localhost:4434)")
	fps           = flag.Int("fps", 10, "Frames per second to send")
	depthWidth    = flag.Int("depth-width", 320, "Depth frame width")
	depthHeight   = flag.Int("depth-height", 240, "Depth frame height")
	rgbWidth      = flag.Int("rgb-width", 640, "RGB frame width")
	rgbHeight     = flag.Int("rgb-height", 480, "RGB frame height")
)

func main() {
	flag.Parse()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	tlsConf := &tls.Config{
		InsecureSkipVerify: true,
		NextProtos:         []string{protocols.ALPN},
		ServerName:         "mp-quic-server",
	}

	quicConf := &quic.Config{
		MaxIdleTimeout:      30 * time.Second,
		KeepAlivePeriod:     10 * time.Second,
		InitialMaxPathID:    1,
		PathSelector:        &roundRobinSelector{},
		Tracer:              qlog.DefaultConnectionTracer,
	}

	// Create client UDP connection for DialEarly - listen on all interfaces
	clientConn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4zero, Port: 0})
	if err != nil {
		log.Fatalf("create client UDP: %v", err)
	}
	defer clientConn.Close()

	// Increase the UDP receive buffer to 8MB to reduce packet loss under bursty
	// depth+RGB traffic (quic-go has no Config field for this; set it on the conn).
	if err := clientConn.SetReadBuffer(8 * 1024 * 1024); err != nil {
		log.Printf("warning: set UDP read buffer: %v", err)
	}

	serverAddrResolved, err := net.ResolveUDPAddr("udp", *serverAddr)
	if err != nil {
		log.Fatalf("resolve server address: %v", err)
	}

	// Use an explicit Transport with a non-zero connection ID length. draft-21
	// multipath requires non-zero Source/Destination Connection IDs (the
	// per-path connection ID carries the path identity); the package-level
	// DialEarly would otherwise use zero-length client connection IDs.
	tr := &quic.Transport{
		Conn:               clientConn,
		ConnectionIDLength: 8,
	}
	defer tr.Close()

	log.Printf("Connecting to QUIC server at %s ...", *serverAddr)
	conn, err := tr.DialEarly(ctx, serverAddrResolved, tlsConf, quicConf)
	if err != nil {
		log.Fatalf("dial: %v", err)
	}
	log.Printf("Connected (path 0): %s -> %s", conn.LocalAddr(), conn.RemoteAddr())

	if *secondAddr != "" {
		addr2, err := net.ResolveUDPAddr("udp", *secondAddr)
		if err != nil {
			log.Printf("Warning: cannot resolve second address %s: %v", *secondAddr, err)
		} else {
			if err := conn.AddPath(addr2, 1); err != nil {
				log.Printf("Warning: AddPath(1) failed: %v", err)
			} else {
				log.Printf("Added path 1: -> %s", addr2)
			}
		}
	}

	log.Printf("Creating camera provider...")
	cam, err := camera.NewProvider()
	if err != nil {
		log.Fatalf("create camera: %v", err)
	}
	defer cam.Close()
	log.Printf("Camera provider created: %T", cam)

	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		sendStream(ctx, conn, handler.StreamDepth, cam, *fps, "depth")
	}()

	go func() {
		defer wg.Done()
		sendStream(ctx, conn, handler.StreamRGB, cam, *fps, "rgb")
	}()

	wg.Wait()
	log.Println("Done")
}

func sendStream(ctx context.Context, conn quic.Connection, st handler.StreamType, cam camera.Provider, fps int, name string) {
	interval := time.Second / time.Duration(fps)
	serial := uint64(0)

	// Open ONE persistent stream for this frame type
	stream, err := conn.OpenStreamSync(ctx)
	if err != nil {
		log.Printf("[%s] open stream: %v", name, err)
		return
	}
	log.Printf("[%s] stream %d opened (persistent)", name, stream.StreamID())
	defer stream.Close()

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		depth, rgb, err := cam.Next()
		if err != nil {
			log.Printf("[%s] capture error: %v", name, err)
			time.Sleep(interval)
			continue
		}

		var rawFrameData []byte
		if st == handler.StreamDepth {
			rawFrameData = depth.Data
		} else {
			rawFrameData = rgb.Data
		}

		// Skip JPEG re-encode if data is already JPEG
		var frameData []byte
		if len(rawFrameData) > 2 && rawFrameData[0] == 0xFF && rawFrameData[1] == 0xD8 {
			frameData = rawFrameData
		} else {
			var convertErr error
			if st == handler.StreamDepth {
				frameData, convertErr = encodeToJPEG(rawFrameData, depth.Width, depth.Height, handler.FrameKindDepth)
			} else {
				frameData, convertErr = encodeToJPEG(rawFrameData, rgb.Width, rgb.Height, handler.FrameKindRGB)
			}
			if convertErr != nil {
				log.Printf("[%s] encode error: %v", name, convertErr)
				time.Sleep(interval)
				continue
			}
		}

		// Wire format: [Magic:4][Length:4][Kind:1][JPEG...]
		payload := make([]byte, 4+4+1+len(frameData))
		copy(payload[0:4], []byte("MPQ1"))
		binary.BigEndian.PutUint32(payload[4:8], uint32(1+len(frameData)))
		payload[8] = byte(st)
		copy(payload[9:], frameData)

		if _, err := stream.Write(payload); err != nil {
			log.Printf("[%s] write error: %v", name, err)
			return
		}

		ack := make([]byte, 2)
		if _, err := io.ReadFull(stream, ack); err != nil {
			log.Printf("[%s] ack error: %v", name, err)
			return
		}

		serial++
		log.Printf("[%s] sent #%d (%d bytes)", name, serial, len(frameData))
		time.Sleep(interval)
	}
}

func encodeToJPEG(data []byte, width, height int, kind handler.FrameKind) ([]byte, error) {
	if kind == handler.FrameKindDepth {
		img := image.NewGray(image.Rect(0, 0, width, height))
		for y := 0; y < height; y++ {
			for x := 0; x < width; x++ {
				idx := y*width + x
				gray := byte(idx % 256)
				if idx*2+1 < len(data) {
					gray = data[idx*2]
				}
				img.SetGray(x, y, color.Gray{Y: gray})
			}
		}
		var buf bytes.Buffer
		if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 85}); err != nil {
			return nil, err
		}
		return buf.Bytes(), nil
	}

	// YUYV 4:2:2 byte layout for each pair: [Y0, U, Y1, V]
	//   - Y0/Y1: luma for even/odd pixel
	//   - U/V:   chroma shared between the two pixels
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			idx := y*width + x
			if idx*2+1 >= len(data) {
				continue
			}
			pairBase := (idx / 2) * 4
			yComp := data[pairBase+(idx%2)*2]
			u := data[pairBase+1]
			v := data[pairBase+3]
			r, g, b := color.YCbCrToRGB(yComp, u, v)
			img.SetRGBA(x, y, color.RGBA{R: uint8(r), G: uint8(g), B: uint8(b), A: 255})
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 85}); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// roundRobinSelector distributes packets across paths evenly.
type roundRobinSelector struct {
	mu    sync.Mutex
	index int
}

func (s *roundRobinSelector) SelectPath(paths []quic.PathState) quic.PathID {
	if len(paths) == 0 {
		return 0
	}
	s.mu.Lock()
	idx := s.index % len(paths)
	s.index++
	s.mu.Unlock()
	return paths[idx].ID
}

func (s *roundRobinSelector) Name() string {
	return "round-robin"
}
