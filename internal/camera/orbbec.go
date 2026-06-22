//go:build !testpattern

package camera

import (
	"log"
	"os"
	"strconv"
)

// NewOrbbecProvider creates an OpenCV-based camera provider.
// This replaces the old raw-V4L2 implementation with OpenCV's battle-tested V4L2 backend.
func NewOrbbecProvider(depthIdx, rgbIdx int) (*OpenCVProvider, error) {
	log.Printf("[OrbbecProvider] Delegating to OpenCV: depth=/dev/video%d rgb=/dev/video%d", depthIdx, rgbIdx)
	return NewOpenCVProvider(depthIdx, rgbIdx)
}

// NewProvider creates a camera provider based on available devices.
// Uses OpenCV (via CGO) for camera capture, falling back to PatternProvider.
func NewProvider() (Provider, error) {
	if provider := tryOpenCVProvider(); provider != nil {
		log.Printf("[NewProvider] Using OpenCV camera provider")
		return provider, nil
	}
	log.Printf("[NewProvider] No camera found, using PatternProvider (test pattern fallback)")
	return NewPatternProvider(), nil
}

// tryOpenCVProvider scans /dev/videoN for depth (16-bit) and RGB cameras via OpenCV.
func tryOpenCVProvider() *OpenCVProvider {
	depthIdx := -1
	rgbIdx := -1

	for i := 0; i < 64; i++ {
		path := "/dev/video" + strconv.Itoa(i)
		if _, err := os.Stat(path); err != nil {
			continue
		}
		if depthIdx < 0 {
			h := camOpen(i, 1)
			if h != nil {
				camClose(h)
				depthIdx = i
				log.Printf("[NewProvider] Found depth camera at /dev/video%d", i)
				continue
			}
		}
		if rgbIdx < 0 && i != depthIdx {
			// Skip devices whose sysfs name contains "Depth" (secondary depth interfaces)
			if camIsDepthDevice(i) != 0 {
				continue
			}
			h := camOpen(i, 0)
			if h != nil {
				camClose(h)
				rgbIdx = i
				log.Printf("[NewProvider] Found RGB camera at /dev/video%d", i)
			}
		}
	}

	if depthIdx < 0 && rgbIdx < 0 {
		return nil
	}
	if depthIdx < 0 {
		depthIdx = rgbIdx
	}
	if rgbIdx < 0 {
		rgbIdx = depthIdx
	}

	provider, err := NewOpenCVProvider(depthIdx, rgbIdx)
	if err != nil {
		log.Printf("[NewProvider] OpenCV provider init failed: %v", err)
		return nil
	}
	return provider
}
