package main

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

//go:embed index.html
var indexHTML string

var (
	listenAddr    = flag.String("addr", ":8080", "HTTP listen address")
	framesDir     = flag.String("frames", "frames", "path to frames directory")
	jetsonFrames  = flag.String("jetson-frames", "", "Jetson frames directory for remote SSH access (user@host:/path)")
	sshKey        = flag.String("ssh-key", "", "SSH private key path for Jetson access (default: ~/.ssh/id_rsa)")

	// Parsed from jetsonFrames
	sshHost         string
	remoteRoot      string
	sshKeyPath      string
	sshControlPath  string
)

type Stats struct {
	Timestamp     time.Time `json:"timestamp"`
	FPS           float64   `json:"fps"`            // aggregate frame serve rate
	RgbFPS        float64   `json:"rgb_fps"`        // RGB stream FPS
	DepthFPS      float64   `json:"depth_fps"`      // Depth stream FPS
	RgbBps        float64   `json:"rgb_bps"`        // RGB bytes per second
	DepthBps      float64   `json:"depth_bps"`      // Depth bytes per second
	TotalFrames   int       `json:"total_frames"`
	FrameSerial   int64     `json:"frame_serial"`   // latest frame mtime
	FrameRgbSize  int       `json:"frame_rgb_size"`
	FrameDepthSize int      `json:"frame_depth_size"`
	FrameTime     string    `json:"frame_time"`     // latest frame timestamp
}

var (
	statsLock     sync.Mutex
	stats         Stats
	latestInfo    frameInfo       // cached latest frame info, refreshed by updateStatsLoop
	infoRefresh   time.Time
	infoLock      sync.Mutex
	frameServe    int64            // total frames served (for real FPS calculation)
	frameServeMu  sync.Mutex
	streamCounts  = struct {
		sync.Mutex
		rgbFrames   int64
		depthFrames int64
		rgbBytes    int64
		depthBytes  int64
	}{}
)

func main() {
	flag.Parse()

	// Parse jetson-frames into sshHost and remoteRoot
	if *jetsonFrames != "" {
		parts := strings.SplitN(*jetsonFrames, ":", 2)
		sshHost = parts[0]
		if len(parts) == 2 {
			remoteRoot = strings.TrimRight(parts[1], "/")
		} else {
			remoteRoot = "."
		}
		sshKeyPath = *sshKey
		if sshKeyPath == "" {
			homeDir, err := os.UserHomeDir()
			if err == nil {
				sshKeyPath = filepath.Join(homeDir, ".ssh", "id_ed25519_mes")
			}
		}
		sshControlPath = filepath.Join(os.TempDir(), "vms-ssh-ctrl")
		// Start SSH ControlMaster for persistent connection (avoids handshake overhead per command)
		masterArgs := []string{"-o", "BatchMode=yes", "-o", "ControlMaster=yes", "-o", fmt.Sprintf("ControlPath=%s", sshControlPath), "-o", "ControlPersist=30"}
		if sshKeyPath != "" {
			masterArgs = append(masterArgs, "-i", sshKeyPath)
		}
		masterArgs = append(masterArgs, "-Nf", sshHost)
		if err := exec.Command("ssh", masterArgs...).Run(); err != nil {
			log.Printf("[VMS] SSH ControlMaster start failed (non-fatal): %v", err)
		} else {
			log.Printf("[VMS] SSH ControlMaster established: path=%s", sshControlPath)
		}
		log.Printf("[VMS] Remote SSH mode: host=%s root=%s key=%s", sshHost, remoteRoot, sshKeyPath)
		// Clean up ControlMaster on exit
		defer func() {
			if sshControlPath != "" {
				exec.Command("ssh", "-o", fmt.Sprintf("ControlPath=%s", sshControlPath), "-O", "exit", sshHost).Run()
			}
		}()
	}

	absDir, err := filepath.Abs(*framesDir)
	if err != nil {
		log.Fatalf("frames dir: %v", err)
	}

	if _, err := os.Stat(absDir); os.IsNotExist(err) {
		log.Fatalf("frames directory %s does not exist", absDir)
	}

	go updateStatsLoop(absDir)

	mux := http.NewServeMux()
	if *jetsonFrames != "" {
		mux.HandleFunc("/frames/", remoteFramesHandler)
	} else {
		mux.Handle("/frames/", http.StripPrefix("/frames/",
			http.FileServer(http.Dir(absDir))))
	}
	mux.HandleFunc("/api/latest", latestHandler)
	mux.HandleFunc("/api/stats", statsHandler)
	mux.HandleFunc("/stream/rgb", func(w http.ResponseWriter, r *http.Request) {
		serveMJPEG(w, r, filepath.Join(absDir, "rgb"), "rgb")
	})
	mux.HandleFunc("/stream/depth", func(w http.ResponseWriter, r *http.Request) {
		serveMJPEG(w, r, filepath.Join(absDir, "depth"), "depth")
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, indexHTML)
	})

	log.Printf("VMS listening on %s", *listenAddr)
	go func() {
		if err := http.ListenAndServe(*listenAddr, mux); err != nil {
			log.Fatalf("HTTP server error: %v", err)
		}
	}()

  select {}
}

func latestHandler(w http.ResponseWriter, r *http.Request) {
	infoLock.Lock()
	info := latestInfo
	infoLock.Unlock()

	// If cache is empty, refresh inline
	if info.Serial == 0 {
		info = getLatestFrames(*framesDir)
	}

	apiResp := struct {
		Depth     string `json:"depth"`
		RGB       string `json:"rgb"`
		Serial    int    `json:"serial"`
		Timestamp string `json:"timestamp"`
	}{
		Depth:     info.Depth,
		RGB:       info.RGB,
		Serial:    info.Serial,
		Timestamp: time.Now().Format(time.RFC3339),
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(apiResp)
}

func statsHandler(w http.ResponseWriter, r *http.Request) {
	statsLock.Lock()
	defer statsLock.Unlock()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(stats)
}

func updateStatsLoop(framesDir string) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	var prevServe, prevRgbFrames, prevDepthFrames, prevRgbBytes, prevDepthBytes int64
	lastTime := time.Now()

	for range ticker.C {
		now := time.Now()
		elapsed := now.Sub(lastTime).Seconds()

		frameServeMu.Lock()
		currentServe := frameServe
		frameServeMu.Unlock()

		streamCounts.Lock()
		curRgbF := streamCounts.rgbFrames
		curDepthF := streamCounts.depthFrames
		curRgbB := streamCounts.rgbBytes
		curDepthB := streamCounts.depthBytes
		streamCounts.Unlock()

		frameSizes := getFrameSizes()

		statsLock.Lock()
		stats.Timestamp = now
		if elapsed > 0 && currentServe > prevServe {
			stats.FPS = float64(currentServe-prevServe) / elapsed
		} else {
			stats.FPS = 0
		}
		if elapsed > 0 && curRgbF > prevRgbFrames {
			stats.RgbFPS = float64(curRgbF-prevRgbFrames) / elapsed
		} else {
			stats.RgbFPS = 0
		}
		if elapsed > 0 && curDepthF > prevDepthFrames {
			stats.DepthFPS = float64(curDepthF-prevDepthFrames) / elapsed
		} else {
			stats.DepthFPS = 0
		}
		if elapsed > 0 && curRgbB > prevRgbBytes {
			stats.RgbBps = float64(curRgbB-prevRgbBytes) / elapsed
		} else {
			stats.RgbBps = 0
		}
		if elapsed > 0 && curDepthB > prevDepthBytes {
			stats.DepthBps = float64(curDepthB-prevDepthBytes) / elapsed
		} else {
			stats.DepthBps = 0
		}

		stats.TotalFrames = int(currentServe)
		stats.FrameSerial = int64(frameSizes.serial)
		stats.FrameRgbSize = frameSizes.rgbSize
		stats.FrameDepthSize = frameSizes.depthSize
		stats.FrameTime = now.Format(time.RFC3339)

		info := getLatestFrames(framesDir)
		infoLock.Lock()
		latestInfo = info
		infoLock.Unlock()

		statsLock.Unlock()

		prevServe = currentServe
		prevRgbFrames = curRgbF
		prevDepthFrames = curDepthF
		prevRgbBytes = curRgbB
		prevDepthBytes = curDepthB
		lastTime = now
	}
}

type frameInfo struct {
	Depth  string `json:"depth"`
	RGB    string `json:"rgb"`
	Serial int    `json:"serial"`
}

type frameSizes struct {
	serial    int
	rgbSize   int
	depthSize int
}

func getFrameSizes() frameSizes {
	var fs frameSizes
	if *jetsonFrames != "" {
		for _, kind := range []string{"rgb", "depth"} {
			remoteFile := filepath.Join(remoteRoot, kind, "latest.jpg")
			cmd := fmt.Sprintf("stat -c '%%Y:%%s' %s 2>/dev/null || echo 0:0", remoteFile)
			out, err := exec.Command("ssh", sshArgs(cmd)...).CombinedOutput()
			if err != nil {
				continue
			}
			parts := strings.SplitN(strings.TrimSpace(string(out)), ":", 2)
			if len(parts) != 2 {
				continue
			}
			mtime, _ := strconv.Atoi(parts[0])
			size, _ := strconv.Atoi(parts[1])
			if kind == "rgb" {
				fs.rgbSize = size
				if mtime > fs.serial {
					fs.serial = mtime
				}
			} else {
				fs.depthSize = size
				if mtime > fs.serial {
					fs.serial = mtime
				}
			}
		}
	} else {
		for _, kind := range []string{"rgb", "depth"} {
			path := filepath.Join(*framesDir, kind, "latest.jpg")
			if fi, err := os.Stat(path); err == nil {
				size := int(fi.Size())
				mtime := int(fi.ModTime().Unix())
				if kind == "rgb" {
					fs.rgbSize = size
					if mtime > fs.serial {
						fs.serial = mtime
					}
				} else {
					fs.depthSize = size
					if mtime > fs.serial {
						fs.serial = mtime
					}
				}
			}
		}
	}
	return fs
}

func getLatestFrames(dir string) frameInfo {
	var depthFiles, rgbFiles []int

	if *jetsonFrames != "" {
		// Remote mode: check mtime of latest.jpg files via SSH (instant, no directory listing)
		for _, kind := range []string{"depth", "rgb"} {
			remoteFile := filepath.Join(remoteRoot, kind, "latest.jpg")
			cmd := fmt.Sprintf("stat -c %%Y %s 2>/dev/null || echo 0", remoteFile)
			output, err := exec.Command("ssh", sshArgs(cmd)...).CombinedOutput()
			if err != nil {
				continue
			}
			mtimeStr := strings.TrimSpace(string(output))
			mtime, err := strconv.Atoi(mtimeStr)
			if err != nil || mtime == 0 {
				continue
			}
			if kind == "depth" {
				depthFiles = append(depthFiles, mtime)
			} else {
				rgbFiles = append(rgbFiles, mtime)
			}
		}
	} else {
		for _, kind := range []string{"depth", "rgb"} {
			path := filepath.Join(dir, kind)
			entries, err := os.ReadDir(path)
			if err != nil {
				continue
			}
			for _, e := range entries {
				if e.IsDir() || !strings.HasSuffix(e.Name(), ".jpg") {
					continue
				}
				base := strings.TrimSuffix(e.Name(), ".jpg")
				n, err := strconv.Atoi(base)
				if err != nil {
					continue
				}
				if kind == "depth" {
					depthFiles = append(depthFiles, n)
				} else {
					rgbFiles = append(rgbFiles, n)
				}
			}
		}
	}

	if len(depthFiles) == 0 && len(rgbFiles) == 0 {
		return frameInfo{}
	}

	sort.Ints(depthFiles)
	sort.Ints(rgbFiles)

	info := frameInfo{}
	if len(depthFiles) > 0 {
		info.Depth = "/frames/depth/latest.jpg"
	}
	if len(rgbFiles) > 0 {
		info.RGB = "/frames/rgb/latest.jpg"
	}
	if len(depthFiles) > 0 && len(rgbFiles) > 0 {
		info.Serial = max(depthFiles[len(depthFiles)-1], rgbFiles[len(rgbFiles)-1])
	} else if len(depthFiles) > 0 {
		info.Serial = depthFiles[len(depthFiles)-1]
	} else {
		info.Serial = rgbFiles[len(rgbFiles)-1]
	}

	return info
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func sshArgs(cmd string) []string {
	args := []string{"-o", "BatchMode=yes", "-o", "ConnectTimeout=2"}
	if sshControlPath != "" {
		args = append(args, "-o", "ControlMaster=no", "-o", fmt.Sprintf("ControlPath=%s", sshControlPath))
	}
	if sshKeyPath != "" {
		args = append(args, "-i", sshKeyPath)
	}
	args = append(args, sshHost, cmd)
	return args
}

func remoteFramesHandler(w http.ResponseWriter, r *http.Request) {
	// /frames/rgb/123.jpg → path=rgb/123.jpg
	path := strings.TrimPrefix(r.URL.Path, "/frames/")
	if path == "" || strings.HasSuffix(path, "/") {
		http.NotFound(w, r)
		return
	}
	remoteFile := filepath.Join(remoteRoot, path)
	data, err := readJetsonFrame(remoteFile)
	if err != nil {
		log.Printf("[remoteFramesHandler] read %s failed: %v", remoteFile, err)
		http.NotFound(w, r)
		return
	}
	frameServeMu.Lock()
	frameServe++
	frameServeMu.Unlock()

	if strings.HasSuffix(path, ".jpg") || strings.HasSuffix(path, ".jpeg") {
		w.Header().Set("Content-Type", "image/jpeg")
	} else {
		w.Header().Set("Content-Type", "application/octet-stream")
	}
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	w.Write(data)
}

func readJetsonFrame(remotePath string) ([]byte, error) {
	catCmd := fmt.Sprintf("cat %s", remotePath)
	output, err := exec.Command("ssh", sshArgs(catCmd)...).CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("SSH read failed: %v, output: %s", err, string(output))
	}
	return output, nil
}

// applyJetColormap applies a jet colormap to a grayscale JPEG image.
// Near (dark) → red/yellow, Far (bright) → cyan/blue (standard depth viz).
func applyJetColormap(data []byte) ([]byte, error) {
	src, err := jpeg.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}
	bounds := src.Bounds()
	dst := image.NewRGBA(bounds)

	// Extract 8-bit intensity from either Gray or YCbCr JPEG
	var getGray func(x, y int) uint8
	switch img := src.(type) {
	case *image.Gray:
		getGray = func(x, y int) uint8 { return img.GrayAt(x, y).Y }
	case *image.YCbCr:
		getGray = func(x, y int) uint8 { return img.YCbCrAt(x, y).Y }
	default:
		return nil, fmt.Errorf("expected Gray or YCbCr JPEG, got %T", src)
	}

	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			v := getGray(x, y)

			var r, g, b uint8
			switch {
			case v < 64:
				// black→red (0→255)
				r = v * 4
				g = 0
				b = 0
			case v < 128:
				// red→yellow (R=255, G=0→255)
				r = 255
				g = (v - 64) * 4
				b = 0
			case v < 192:
				// yellow→green→cyan (R=255→0, G=255, B=0→255)
				r = uint8(255 - (v-128)*4)
				g = 255
				b = (v - 128) * 4
			default:
				// cyan→blue (G=255→0, B=255, R=0)
				r = 0
				g = uint8(255 - (v-192)*4)
				b = 255
			}

			dst.Set(x, y, color.RGBA{r, g, b, 255})
		}
	}

	var buf bytes.Buffer
	err = jpeg.Encode(&buf, dst, &jpeg.Options{Quality: 80})
	if err != nil {
		return nil, fmt.Errorf("encode: %w", err)
	}
	return buf.Bytes(), nil
}

func serveMJPEG(w http.ResponseWriter, r *http.Request, dir, name string) {
	log.Printf("[stream/%s] client connected - dir=%s, jetsonFrames=%v", name, dir, *jetsonFrames)

	defer func() {
		if r := recover(); r != nil {
			log.Printf("[stream/%s] panic: %v", name, r)
		}
	}()

	w.Header().Set("Content-Type", "multipart/x-mixed-replace; boundary=frame")
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	w.Header().Set("Connection", "keep-alive")

	flusher, ok := w.(http.Flusher)
	if !ok {
		log.Printf("[stream/%s] FAILED: http.Flusher not supported", name)
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}
	log.Printf("[stream/%s] OK: Flusher ready", name)

	lastSerial := 0
	var readFrame func(string) ([]byte, error)

	if *jetsonFrames != "" {
		log.Printf("[stream/%s] Using Jetson frames: host=%s, remoteRoot=%s", name, sshHost, remoteRoot)
		remoteLatest := filepath.Join(remoteRoot, name, "latest.jpg")
		// Set initial serial to current mtime
		cmd := fmt.Sprintf("stat -c %%Y %s 2>/dev/null || echo 0", remoteLatest)
		output, err := exec.Command("ssh", sshArgs(cmd)...).CombinedOutput()
		if err == nil {
			lastSerial, _ = strconv.Atoi(strings.TrimSpace(string(output)))
		}
		log.Printf("[stream/%s] initial mtime: %d", name, lastSerial)
		readFrame = func(filename string) ([]byte, error) {
			// Ignore filename, always read latest.jpg
			return readJetsonFrame(remoteLatest)
		}
	} else {
		log.Printf("[stream/%s] Using local frames dir: %s", name, dir)
		entries, err := os.ReadDir(dir)
		if err != nil {
			log.Printf("[stream/%s] OS.ReadDir error: %v", name, err)
			return
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".jpg") {
				continue
			}
			base := strings.TrimSuffix(e.Name(), ".jpg")
			n, err := strconv.Atoi(base)
			if err != nil {
				continue
			}
			if n > lastSerial {
				lastSerial = n
			}
		}
		log.Printf("[stream/%s] lastSerial after local read: %d", name, lastSerial)
		readFrame = func(filename string) ([]byte, error) {
			return os.ReadFile(filepath.Join(dir, filename))
		}
	}

	if lastSerial > 0 {
		log.Printf("[stream/%s] Reading frame #%d.jpg", name, lastSerial)
		data, err := readFrame(fmt.Sprintf("%d.jpg", lastSerial))
		if err != nil {
			log.Printf("[stream/%s] Read frame error: %v", name, err)
		} else {
			log.Printf("[stream/%s] Read frame OK: %d bytes", name, len(data))
			fmt.Fprintf(w, "--frame\r\nContent-Type: image/jpeg\r\nContent-Length: %d\r\n\r\n", len(data))
			w.Write(data)
			fmt.Fprintf(w, "\r\n")
			flusher.Flush()
			log.Printf("[stream/%s] Frame flushed", name)
		}
	} else {
		log.Printf("[stream/%s] No frames found (lastSerial=0)", name)
	}

	ticker := time.NewTicker(33 * time.Millisecond)
	defer ticker.Stop()

	var lastSig string
	emitCount := 0
	lastLog := time.Now()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			var data []byte
			if *jetsonFrames != "" {
				remoteLatest := filepath.Join(remoteRoot, name, "latest.jpg")
				// Check mtime+size via stat (lightweight, ~14ms) before full cat (~14ms)
				statCmd := fmt.Sprintf("stat -c '%%Y:%%s' %s 2>/dev/null || echo 0:0", remoteLatest)
				out, err := exec.Command("ssh", sshArgs(statCmd)...).CombinedOutput()
				if err != nil {
					continue
				}
				sig := strings.TrimSpace(string(out))
				if sig == lastSig || sig == "0:0" {
					continue
				}
				lastSig = sig
				data, err = readJetsonFrame(remoteLatest)
				if err != nil || len(data) == 0 {
					continue
				}
			} else {
				entries, err := os.ReadDir(dir)
				if err != nil {
					continue
				}
				var latest os.DirEntry
				for _, e := range entries {
					if e.IsDir() || !strings.HasSuffix(e.Name(), ".jpg") {
						continue
					}
					if latest == nil {
						latest = e
					} else {
						info1, _ := e.Info()
						info2, _ := latest.Info()
						if info1 != nil && info2 != nil && info1.ModTime().After(info2.ModTime()) {
							latest = e
						}
					}
				}
				if latest == nil {
					continue
				}
				data, err = os.ReadFile(filepath.Join(dir, latest.Name()))
				if err != nil || len(data) == 0 {
					continue
				}
			}

			if name == "depth" && len(data) > 0 {
				colored, err := applyJetColormap(data)
				if err == nil {
					data = colored
				} else {
					log.Printf("[stream/depth] colormap error: %v", err)
				}
			}

			frameServeMu.Lock()
			frameServe++
			frameServeMu.Unlock()

			streamCounts.Lock()
			if name == "rgb" {
				streamCounts.rgbFrames++
				streamCounts.rgbBytes += int64(len(data))
			} else {
				streamCounts.depthFrames++
				streamCounts.depthBytes += int64(len(data))
			}
			streamCounts.Unlock()

			fmt.Fprintf(w, "--frame\r\nContent-Type: image/jpeg\r\nContent-Length: %d\r\n\r\n", len(data))
			w.Write(data)
			fmt.Fprintf(w, "\r\n")
			flusher.Flush()
			emitCount++
			if time.Since(lastLog) > 5*time.Second {
				log.Printf("[stream/%s] Emitted %d frames in 5s (%.1f fps)", name, emitCount, float64(emitCount)/5)
				emitCount = 0
				lastLog = time.Now()
			}
		}
	}
}
