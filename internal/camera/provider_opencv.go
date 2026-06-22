package camera

/*
#cgo CXXFLAGS: -I/usr/include/opencv4
#cgo LDFLAGS: -lopencv_core -lopencv_videoio -lopencv_imgcodecs -lopencv_imgproc
#include "camera_wrapper.h"
*/
import "C"
import (
	"fmt"
	"log"
	"sync"
	"time"
	"unsafe"
)

// OpenCVProvider implements Provider using OpenCV's cv::VideoCapture with CAP_V4L2.
type OpenCVProvider struct {
	mu          sync.Mutex
	depthHandle C.cam_handle_t
	rgbHandle   C.cam_handle_t
	depthW      int
	depthH      int
	rgbW        int
	rgbH        int
	depthSeq    int
	rgbSeq      int
}

// NewOpenCVProvider creates camera handles for depth and RGB via OpenCV.
// depthIdx: /dev/videoN for depth (e.g., 0)
// rgbIdx:   /dev/videoN for RGB (e.g., 4)
func NewOpenCVProvider(depthIdx, rgbIdx int) (*OpenCVProvider, error) {
	log.Printf("[OpenCVProvider] opening depth camera /dev/video%d ...", depthIdx)
	depthH := C.cam_open(C.int(depthIdx), C.int(1))
	if depthH == nil {
		return nil, fmt.Errorf("depth camera /dev/video%d open failed", depthIdx)
	}
	depthW := int(C.cam_get_width(depthH))
	depthHgt := int(C.cam_get_height(depthH))
	log.Printf("[OpenCVProvider] depth camera opened: %dx%d", depthW, depthHgt)

	log.Printf("[OpenCVProvider] opening RGB camera /dev/video%d ...", rgbIdx)
	rgbH := C.cam_open(C.int(rgbIdx), C.int(0))
	if rgbH == nil {
		C.cam_close(depthH)
		return nil, fmt.Errorf("RGB camera /dev/video%d open failed", rgbIdx)
	}
	rgbW := int(C.cam_get_width(rgbH))
	rgbHgt := int(C.cam_get_height(rgbH))
	log.Printf("[OpenCVProvider] RGB camera opened: %dx%d", rgbW, rgbHgt)

	return &OpenCVProvider{
		depthHandle: depthH,
		rgbHandle:   rgbH,
		depthW:      depthW,
		depthH:      depthHgt,
		rgbW:        rgbW,
		rgbH:        rgbHgt,
	}, nil
}

func (p *OpenCVProvider) Next() (Frame, Frame, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	// Default buffer size: 1MB per frame
	const bufSize = 1 << 20
	buf := make([]byte, bufSize)

	// Capture depth
	p.depthSeq++
	n := C.cam_capture(p.depthHandle, (*C.uchar)(unsafe.Pointer(&buf[0])), C.int(bufSize))
	if n <= 0 {
		log.Printf("[OpenCVProvider] depth capture failed: %d", int(n))
		return p.nextFallback()
	}
	depthData := make([]byte, int(n))
	copy(depthData, buf[:n])

	// Capture RGB
	p.rgbSeq++
	n = C.cam_capture(p.rgbHandle, (*C.uchar)(unsafe.Pointer(&buf[0])), C.int(bufSize))
	if n <= 0 {
		log.Printf("[OpenCVProvider] RGB capture failed: %d", int(n))
		return p.nextFallback()
	}
	rgbData := make([]byte, int(n))
	copy(rgbData, buf[:n])

	return Frame{
			Kind:     FrameKindDepth,
			Data:     depthData,
			Width:    p.depthW,
			Height:   p.depthH,
			Channels: 1,
			Serial:   uint64(p.depthSeq),
		}, Frame{
			Kind:      FrameKindRGB,
			Data:      rgbData,
			Width:     p.rgbW,
			Height:    p.rgbH,
			Channels:  3,
			Serial:    uint64(p.rgbSeq),
			Timestamp: time.Now(),
		}, nil
}

func (p *OpenCVProvider) nextFallback() (Frame, Frame, error) {
	return NewPatternProviderWithSize(320, 240).Next()
}

func (p *OpenCVProvider) Close() error {
	if p.depthHandle != nil {
		C.cam_close(p.depthHandle)
		p.depthHandle = nil
	}
	if p.rgbHandle != nil {
		C.cam_close(p.rgbHandle)
		p.rgbHandle = nil
	}
	return nil
}

// Go wrapper functions for non-CGO callers (used by orbbec.go)
func camOpen(index, isDepth int) unsafe.Pointer {
	return unsafe.Pointer(C.cam_open(C.int(index), C.int(isDepth)))
}

func camClose(h unsafe.Pointer) {
	if h != nil {
		C.cam_close(C.cam_handle_t(h))
	}
}

func camIsDepthDevice(index int) int {
	return int(C.cam_is_depth_device(C.int(index)))
}

var _ Provider = (*OpenCVProvider)(nil)
