package camera

import (
	"context"
	"fmt"
	"log"
	"os"
	"syscall"
	"unsafe"
)

// V4L2 constants
const (
	VIDIOC_QUERYCAP   = 0x80685600
	VIDIOC_ENUM_FMT   = 0xc0405602
	VIDIOC_G_FMT      = 0xc0cc5604
	VIDIOC_S_FMT      = 0xc0cc5605
	VIDIOC_REQBUFS    = 0xc0145608
	// arm64: sizeof(struct v4l2_buffer)=84 (timeval=16B, m.union=8B)
	VIDIOC_QUERYBUF   = 0xc0545609
	VIDIOC_QBUF       = 0xc054560f
	VIDIOC_DQBUF      = 0xc0545611
	VIDIOC_STREAMON   = 0x40045612
	VIDIOC_STREAMOFF  = 0x40045613

	V4L2_MEMORY_MMAP  = 1
	V4L2_BUF_TYPE_VIDEO_CAPTURE = 1

	V4L2_PIX_FMT_YUYV = 0x56595559
	V4L2_PIX_FMT_Y16  = 0x20363159
)

// v4l2_capability matches struct v4l2_capability (104 bytes).
type v4l2_capability struct {
	driver      [16]byte
	card        [32]byte
	bus_info    [32]byte
	version     uint32
	capability  uint32
	device_caps uint32
	reserved    [3]uint32 // kernel has __u32 reserved[3] = 12 bytes
}

type v4l2_fmtdesc struct {
	index      uint32
	ctype      uint32
	flags      uint32
	description [32]byte
	pixfmt     uint32
	reserved   [4]uint32
}

// v4l2_pix_format matches struct v4l2_pix_format from linux/videodev2.h.
// All __u32 fields, 48 bytes total.
type v4l2_pix_format struct {
	Width        uint32
	Height       uint32
	PixelFormat  uint32
	Field        uint32
	BytesPerLine uint32
	SizeImage    uint32
	ColorSpace   uint32
	Priv         uint32
	Flags        uint32
	YCbCrEnc     uint32
	Quantization uint32
	XferFunc     uint32
}

// v4l2_format matches struct v4l2_format.
// type(4) + union(raw_data[200]) = 204 bytes.
// The union covers pix(48), pix_mp, win, vbi, sliced, sdr, meta.
type v4l2_fmt struct {
	Ctype uint32
	Pix   v4l2_pix_format // 48 bytes starting at offset 4
	Pad   [152]byte       // 204 - 4 - 48 = 152
}

type v4l2_requestbuffers struct {
	count      uint32
	ctype      uint32
	memory     uint32
	capability uint32
	reserved   [4]uint32
}

// v4l2_timecode matches struct v4l2_timecode (16 bytes).
type v4l2_timecode struct {
	Type     uint32
	Flags    uint32
	Frames   uint8
	Seconds  uint8
	Minutes  uint8
	Hours    uint8
	Userbits [4]uint8
}

// v4l2_buffer matches struct v4l2_buffer on arm64 (84 bytes).
// Kernel layout (arm64):
//   index(4) + type(4) + bytesused(4) + flags(4) + field(4)
//   + pad(4) + tv_sec(8) + tv_usec(8) + timecode(16)
//   + sequence(4) + memory(4) + m.union(8) + length(4)
//   + reserved2(4) + reserved(4) = 84
type v4l2_buffer struct {
	Index     uint32          // offset  0
	Type      uint32          // offset  4
	ByeUsed   uint32          // offset  8
	Flags     uint32          // offset 12
	Field     uint32          // offset 16
	_         [4]byte         // offset 20 (padding)
	TvSec     int64           // offset 24 (struct timeval.tv_sec)
	TvUsec    int64           // offset 32 (struct timeval.tv_usec)
	Timecode  v4l2_timecode   // offset 40 (16 bytes)
	Sequence  uint32          // offset 56
	Memory    uint32          // offset 60
	M_raw     uint64          // offset 64 (union m: offset in low 32 bits)
	Length    uint32          // offset 72
	Reserved1 uint32          // offset 76
	Reserved2 uint32          // offset 80
}

// m_offset returns the mmap buffer offset (lower 32 bits of m union).
func (b *v4l2_buffer) m_offset() uint32 {
	return uint32(b.M_raw)
}

func v4l2_ioctl(fd int, req uint64, arg uintptr) error {
	_, _, err := syscall.Syscall(syscall.SYS_IOCTL, uintptr(fd), uintptr(req), arg)
	if err != 0 {
		return err
	}
	return nil
}

func v4l2_querycap(fd int) (*v4l2_capability, error) {
	var cap v4l2_capability
	log.Printf("[V4L2] VIDIOC_QUERYCAP fd=%d", fd)
	err := v4l2_ioctl(fd, VIDIOC_QUERYCAP, uintptr(unsafe.Pointer(&cap)))
	if err != nil {
		log.Printf("[V4L2] VIDIOC_QUERYCAP fd=%d FAILED: %v", fd, err)
		return nil, fmt.Errorf("VIDIOC_QUERYCAP: %w", err)
	}
	log.Printf("[V4L2] VIDIOC_QUERYCAP fd=%d OK: driver=%s card=%s bus_info=%s version=%d caps=0x%x device_caps=0x%x",
		fd, string(cap.driver[:]), string(cap.card[:]), string(cap.bus_info[:]),
		cap.version, cap.capability, cap.device_caps)
	return &cap, nil
}

func v4l2_enum_fmt(fd int, index uint32, pixfmt uint32) (*v4l2_fmtdesc, error) {
	var desc v4l2_fmtdesc
	desc.index = index
	desc.ctype = V4L2_BUF_TYPE_VIDEO_CAPTURE
	desc.pixfmt = pixfmt
	err := v4l2_ioctl(fd, VIDIOC_ENUM_FMT, uintptr(unsafe.Pointer(&desc)))
	if err != nil {
		return nil, fmt.Errorf("VIDIOC_ENUM_FMT index=%d: %w", index, err)
	}
	return &desc, nil
}

func v4l2_set_fmt(fd int, pixelformat uint32, width, height int) error {
	var vfmt v4l2_fmt
	vfmt.Ctype = V4L2_BUF_TYPE_VIDEO_CAPTURE
	vfmt.Pix.Width = uint32(width)
	vfmt.Pix.Height = uint32(height)
	vfmt.Pix.PixelFormat = pixelformat
	vfmt.Pix.Field = 0 // V4L2_FIELD_NONE
	vfmt.Pix.BytesPerLine = uint32(width) * 2 // YUYV = 2 bytes/pixel
	vfmt.Pix.SizeImage = uint32(width * height * 2)

	log.Printf("[V4L2] VIDIOC_S_FMT fd=%d: req: pixfmt=0x%x W=%d H=%d bpl=%d size=%d",
		fd, pixelformat, width, height, vfmt.Pix.BytesPerLine, vfmt.Pix.SizeImage)
	err := v4l2_ioctl(fd, VIDIOC_S_FMT, uintptr(unsafe.Pointer(&vfmt)))
	if err != nil {
		log.Printf("[V4L2] VIDIOC_S_FMT fd=%d FAILED: %v", fd, err)
		return fmt.Errorf("VIDIOC_S_FMT: %w", err)
	}
	log.Printf("[V4L2] VIDIOC_S_FMT fd=%d OK: actual: pixfmt=0x%x W=%d H=%d bpl=%d size=%d",
		fd, vfmt.Pix.PixelFormat, vfmt.Pix.Width, vfmt.Pix.Height, vfmt.Pix.BytesPerLine, vfmt.Pix.SizeImage)
	return nil
}

func v4l2_get_fmt(fd int) (*v4l2_fmt, error) {
	var vfmt v4l2_fmt
	vfmt.Ctype = V4L2_BUF_TYPE_VIDEO_CAPTURE

	log.Printf("[V4L2] VIDIOC_G_FMT fd=%d", fd)
	err := v4l2_ioctl(fd, VIDIOC_G_FMT, uintptr(unsafe.Pointer(&vfmt)))
	if err != nil {
		log.Printf("[V4L2] VIDIOC_G_FMT fd=%d FAILED: %v", fd, err)
		return nil, fmt.Errorf("VIDIOC_G_FMT: %w", err)
	}
	log.Printf("[V4L2] VIDIOC_G_FMT fd=%d OK: pixfmt=0x%x W=%d H=%d field=%d bpl=%d size=%d colorspace=%d",
		fd, vfmt.Pix.PixelFormat, vfmt.Pix.Width, vfmt.Pix.Height,
		vfmt.Pix.Field, vfmt.Pix.BytesPerLine, vfmt.Pix.SizeImage, vfmt.Pix.ColorSpace)
	return &vfmt, nil
}

func v4l2_reqbufs(fd int, count uint32) error {
	var req v4l2_requestbuffers
	req.count = count
	req.ctype = V4L2_BUF_TYPE_VIDEO_CAPTURE
	req.memory = V4L2_MEMORY_MMAP

	log.Printf("[V4L2] VIDIOC_REQBUFS fd=%d: count=%d", fd, count)
	err := v4l2_ioctl(fd, VIDIOC_REQBUFS, uintptr(unsafe.Pointer(&req)))
	if err != nil {
		log.Printf("[V4L2] VIDIOC_REQBUFS fd=%d FAILED: %v", fd, err)
		return fmt.Errorf("VIDIOC_REQBUFS: %w", err)
	}
	log.Printf("[V4L2] VIDIOC_REQBUFS fd=%d OK: actual_count=%d", fd, req.count)
	return nil
}

func v4l2_querybuf(fd int, index uint32) (uint32, uint32, error) {
	var buf v4l2_buffer
	buf.Index = index
	buf.Type = V4L2_BUF_TYPE_VIDEO_CAPTURE
	buf.Memory = V4L2_MEMORY_MMAP

	log.Printf("[V4L2] VIDIOC_QUERYBUF fd=%d: index=%d", fd, index)
	err := v4l2_ioctl(fd, VIDIOC_QUERYBUF, uintptr(unsafe.Pointer(&buf)))
	if err != nil {
		log.Printf("[V4L2] VIDIOC_QUERYBUF fd=%d index=%d FAILED: %v", fd, index, err)
		return 0, 0, fmt.Errorf("VIDIOC_QUERYBUF: %w", err)
	}
	log.Printf("[V4L2] VIDIOC_QUERYBUF fd=%d index=%d OK: m_offset=%d length=%d bye_used=%d sequence=%d memory=%d",
		fd, index, buf.m_offset(), buf.Length, buf.ByeUsed, buf.Sequence, buf.Memory)
	return buf.m_offset(), buf.Length, nil
}

func v4l2_qbuf(fd int, index uint32) error {
	var buf v4l2_buffer
	buf.Index = index
	buf.Type = V4L2_BUF_TYPE_VIDEO_CAPTURE
	buf.Memory = V4L2_MEMORY_MMAP

	log.Printf("[V4L2] VIDIOC_QBUF fd=%d: index=%d", fd, index)
	err := v4l2_ioctl(fd, VIDIOC_QBUF, uintptr(unsafe.Pointer(&buf)))
	if err != nil {
		log.Printf("[V4L2] VIDIOC_QBUF fd=%d index=%d FAILED: %v", fd, index, err)
		return fmt.Errorf("VIDIOC_QBUF: %w", err)
	}
	log.Printf("[V4L2] VIDIOC_QBUF fd=%d index=%d OK", fd, index)
	return nil
}

func v4l2_dqbuf(fd int) (uint32, error) {
	var buf v4l2_buffer
	buf.Type = V4L2_BUF_TYPE_VIDEO_CAPTURE
	buf.Memory = V4L2_MEMORY_MMAP

	log.Printf("[V4L2] VIDIOC_DQBUF fd=%d (blocking)...", fd)
	err := v4l2_ioctl(fd, VIDIOC_DQBUF, uintptr(unsafe.Pointer(&buf)))
	if err != nil {
		log.Printf("[V4L2] VIDIOC_DQBUF fd=%d FAILED: %v", fd, err)
		return 0, fmt.Errorf("VIDIOC_DQBUF: %w", err)
	}
	log.Printf("[V4L2] VIDIOC_DQBUF fd=%d OK: index=%d bye_used=%d sequence=%d flags=0x%x",
		fd, buf.Index, buf.ByeUsed, buf.Sequence, buf.Flags)
	return buf.Index, nil
}

func v4l2_streamon(fd int) error {
	var typ uint32 = V4L2_BUF_TYPE_VIDEO_CAPTURE
	log.Printf("[V4L2] VIDIOC_STREAMON fd=%d", fd)
	err := v4l2_ioctl(fd, VIDIOC_STREAMON, uintptr(unsafe.Pointer(&typ)))
	if err != nil {
		log.Printf("[V4L2] VIDIOC_STREAMON fd=%d FAILED: %v", fd, err)
		return fmt.Errorf("VIDIOC_STREAMON: %w", err)
	}
	log.Printf("[V4L2] VIDIOC_STREAMON fd=%d OK", fd)
	return nil
}

func v4l2_streamoff(fd int) error {
	var typ uint32 = V4L2_BUF_TYPE_VIDEO_CAPTURE
	log.Printf("[V4L2] VIDIOC_STREAMOFF fd=%d", fd)
	err := v4l2_ioctl(fd, VIDIOC_STREAMOFF, uintptr(unsafe.Pointer(&typ)))
	if err != nil {
		log.Printf("[V4L2] VIDIOC_STREAMOFF fd=%d FAILED: %v", fd, err)
		return fmt.Errorf("VIDIOC_STREAMOFF: %w", err)
	}
	log.Printf("[V4L2] VIDIOC_STREAMOFF fd=%d OK", fd)
	return nil
}

func mmap_v4l2(fd int, offset uint32, length uint32) ([]byte, error) {
	log.Printf("[V4L2] mmap fd=%d offset=%d length=%d", fd, offset, length)
	data, err := syscall.Mmap(fd, int64(offset), int(length),
		syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_SHARED)
	if err != nil {
		log.Printf("[V4L2] mmap fd=%d offset=%d length=%d FAILED: %v", fd, offset, length, err)
		return nil, fmt.Errorf("mmap: %w", err)
	}
	log.Printf("[V4L2] mmap fd=%d offset=%d length=%d OK: addr=%p len=%d", fd, offset, length, &data[0], len(data))
	return data, nil
}

func munmap_v4l2(data []byte) error {
	return syscall.Munmap(data)
}

type V4L2Config struct {
	DevicePath string
	Kind       FrameKind
	Width      int
	Height     int
	PixelFmt   PixelFormat
	FrameRate  int
}

func ValidateV4L2Config(cfg V4L2Config) error {
	if cfg.DevicePath == "" {
		return fmt.Errorf("empty device path")
	}
	if cfg.Width <= 0 {
		return fmt.Errorf("invalid width %d", cfg.Width)
	}
	if cfg.Height <= 0 {
		return fmt.Errorf("invalid height %d", cfg.Height)
	}
	if cfg.FrameRate <= 0 {
		return fmt.Errorf("invalid frame rate %d", cfg.FrameRate)
	}
	if !IsSupportedV4L2PixelFormat(cfg.PixelFmt) {
		return fmt.Errorf("unsupported pixel format: %s", cfg.PixelFmt)
	}
	if cfg.Kind != FrameKindRGB && cfg.Kind != FrameKindDepth {
		return fmt.Errorf("invalid frame kind: %d", cfg.Kind)
	}
	if cfg.Kind == FrameKindRGB {
		if cfg.PixelFmt == PixelFormatZ16 {
			return fmt.Errorf("RGB kind with Z16 pixel format is invalid")
		}
	}
	if cfg.Kind == FrameKindDepth {
		if cfg.PixelFmt == PixelFormatYUYV || cfg.PixelFmt == PixelFormatRGB24 {
			return fmt.Errorf("Depth kind with %s pixel format is invalid", cfg.PixelFmt)
		}
	}
	return nil
}

type V4L2Source struct {
	config V4L2Config
	file   *os.File
	started bool
}

func NewV4L2Source(config V4L2Config) *V4L2Source {
	return &V4L2Source{config: config}
}

func (s *V4L2Source) Start(ctx context.Context) error {
	if err := ValidateV4L2Config(s.config); err != nil {
		return fmt.Errorf("invalid config: %w", err)
	}

	file, err := os.Open(s.config.DevicePath)
	if err != nil {
		return fmt.Errorf("open device: %w", err)
	}
	s.file = file
	s.started = true

	return nil
}

func (s *V4L2Source) ReadFrame(ctx context.Context) (Frame, error) {
	if !s.started {
		return Frame{}, fmt.Errorf("source not started")
	}
	// TODO: Implement buffer queuing and frame capture
	return Frame{}, fmt.Errorf("not implemented")
}

func (s *V4L2Source) Close() error {
	s.started = false
	if s.file != nil {
		return s.file.Close()
	}
	return nil
}

func IsSupportedV4L2PixelFormat(fmt PixelFormat) bool {
	switch fmt {
	case PixelFormatYUYV, PixelFormatZ16, PixelFormatRGB24:
		return true
	default:
		return false
	}
}

func ExpectedV4L2FrameSize(cfg V4L2Config) (int, error) {
	if !IsSupportedV4L2PixelFormat(cfg.PixelFmt) {
		return 0, fmt.Errorf("unsupported pixel format: %s", cfg.PixelFmt)
	}
	switch cfg.PixelFmt {
	case PixelFormatYUYV, PixelFormatZ16:
		return cfg.Width * cfg.Height * 2, nil
	case PixelFormatRGB24:
		return cfg.Width * cfg.Height * 3, nil
	default:
		return 0, fmt.Errorf("unsupported pixel format: %s", cfg.PixelFmt)
	}
}
