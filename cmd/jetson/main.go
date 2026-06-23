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
	"syscall"
	"time"

	"github.com/quic-go/quic-go"
	"github.com/quic-go/quic-go/qlog"
	"mp-quic-go/internal/camera"
	"mp-quic-go/internal/handler"
	"mp-quic-go/internal/mpquic/pqi"
	"mp-quic-go/internal/mpquic/scheduler"
	"mp-quic-go/internal/mpquic/session"
	"mp-quic-go/pkg/protocols"
)

var (
	serverAddr    = flag.String("addr", "localhost:4433", "QUIC server address")
	secondAddr    = flag.String("path1", "", "Second server address for path 1 (e.g., localhost:4434)")
	path0Iface    = flag.String("path0-iface", "", "bind path 0 (main) to this network interface, e.g. wlP1p1s0 (needs root)")
	path1Iface    = flag.String("path1-iface", "", "bind path 1 to this network interface, e.g. the cellular hotspot (needs root)")
	fps           = flag.Int("fps", 10, "Frames per second to send")
	depthWidth    = flag.Int("depth-width", 320, "Depth frame width")
	depthHeight   = flag.Int("depth-height", 240, "Depth frame height")
	rgbWidth      = flag.Int("rgb-width", 640, "RGB frame width")
	rgbHeight     = flag.Int("rgb-height", 480, "RGB frame height")
	schedName     = flag.String("scheduler", "rssi", "path scheduler: pqi|min-rtt|round-robin|rssi")

	// PQI scheduler parameters (reported in the paper; tunable for reproducibility).
	pqiAlpha  = flag.Float64("pqi-alpha", 0.5, "PQI cost weight for RTT")
	pqiBeta   = flag.Float64("pqi-beta", 0.3, "PQI cost weight for loss")
	pqiGamma  = flag.Float64("pqi-gamma", 0.2, "PQI cost weight for bandwidth")
	pqiLambda = flag.Float64("pqi-lambda", 0.3, "PQI EWMA smoothing factor")
	pqiWindow = flag.Int("pqi-window", 10, "PQI trend sliding-window size (samples)")
	pqiTdeg   = flag.Float64("pqi-tdeg", 40, "PQI degradation threshold")
	pqiMargin = flag.Float64("pqi-margin", 10, "PQI handover safety margin")
	pqiStable = flag.Duration("pqi-stable", 500*time.Millisecond, "PQI stability interval")
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

	// Select the path scheduler (PQI by default). Switching schedulers via this
	// flag, with the rest of the stack identical, supports the fair baseline
	// comparison (PQI vs min-rtt vs round-robin) requested by the reviewers.
	var sched scheduler.Scheduler
	if *schedName == "pqi" {
		sched = scheduler.NewPQIScheduler(scheduler.PQIConfig{
			Weights: pqi.Weights{Alpha: *pqiAlpha, Beta: *pqiBeta, Gamma: *pqiGamma},
			Lambda:  *pqiLambda,
			Window:  *pqiWindow,
			Hyst: pqi.HysteresisParams{
				DegradationThreshold: *pqiTdeg,
				SafetyMargin:         *pqiMargin,
				StabilityInterval:    *pqiStable,
			},
		})
	} else {
		var err error
		sched, err = scheduler.New(*schedName)
		if err != nil {
			log.Fatalf("scheduler: %v", err)
		}
	}
	log.Printf("Using path scheduler: %s", sched.Name())

	quicConf := &quic.Config{
		MaxIdleTimeout:      30 * time.Second,
		KeepAlivePeriod:     10 * time.Second,
		InitialMaxPathID:    1,
		PathSelector:        session.NewQuicPathSelector(sched),
		Tracer:              qlog.DefaultConnectionTracer,
	}

	// Create the path-0 (main) UDP socket. Optionally bind it to a specific
	// interface (e.g. Wi-Fi) so path 0 is pinned to that NIC; otherwise listen on
	// all interfaces and let the OS route.
	clientConn, err := listenUDP(*path0Iface)
	if err != nil {
		log.Fatalf("create client UDP: %v", err)
	}
	defer clientConn.Close()
	if *path0Iface != "" {
		log.Printf("path 0 bound to interface %s", *path0Iface)
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
		} else if *path1Iface != "" {
			// Heterogeneous path: path 1 sends/receives on its own socket bound to
			// a dedicated interface (e.g. cellular), independent of path 0 (Wi-Fi).
			pconn, perr := listenUDP(*path1Iface)
			if perr != nil {
				log.Printf("Warning: bind path 1 to %s failed: %v", *path1Iface, perr)
			} else if err := conn.AddPathConn(addr2, 1, pconn); err != nil {
				log.Printf("Warning: AddPathConn(1) failed: %v", err)
				pconn.Close()
			} else {
				log.Printf("Added path 1 on interface %s: -> %s", *path1Iface, addr2)
			}
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

// listenUDP creates a UDP socket with an 8MB receive buffer. If iface is
// non-empty, the socket is bound to that network interface via SO_BINDTODEVICE
// (Linux; needs CAP_NET_RAW / root) so its packets egress that interface
// regardless of the routing table. This is what lets path 0 use Wi-Fi and path 1
// use the cellular hotspot simultaneously.
func listenUDP(iface string) (*net.UDPConn, error) {
	lc := net.ListenConfig{}
	if iface != "" {
		lc.Control = func(_, _ string, c syscall.RawConn) error {
			var serr error
			if err := c.Control(func(fd uintptr) {
				serr = syscall.SetsockoptString(int(fd), syscall.SOL_SOCKET, syscall.SO_BINDTODEVICE, iface)
			}); err != nil {
				return err
			}
			return serr
		}
	}
	pc, err := lc.ListenPacket(context.Background(), "udp4", ":0")
	if err != nil {
		return nil, err
	}
	uc := pc.(*net.UDPConn)
	if err := uc.SetReadBuffer(8 * 1024 * 1024); err != nil {
		log.Printf("warning: set UDP read buffer: %v", err)
	}
	return uc, nil
}
