package camera

import (
	"sync/atomic"
)

// PatternProvider generates synthetic test patterns for depth and RGB.
type PatternProvider struct {
	width  int
	height int
	serial atomic.Uint64
}

func NewPatternProvider() *PatternProvider {
	return NewPatternProviderWithSize(320, 240)
}

func NewPatternProviderWithSize(width, height int) *PatternProvider {
	return &PatternProvider{width: width, height: height}
}

func (p *PatternProvider) Next() (Frame, Frame, error) {
	serial := p.serial.Add(1)

	depth := p.genDepth(serial)
	rgb := p.genRGB(serial)

	return depth, rgb, nil
}

func (p *PatternProvider) genDepth(serial uint64) Frame {
	data := make([]byte, p.width*p.height*2)
	phase := float64(serial%100) / 100.0
	for y := 0; y < p.height; y++ {
		for x := 0; x < p.width; x++ {
			v := uint32(float64(x)/float64(p.width)*60000 + 2000)
			wave := uint32(2000 * (sin8(phase + float64(y)/float64(p.height)*2)))
			depth := clampU32(v + wave)
			idx := (y*p.width + x) * 2
			data[idx] = byte(depth)       // low byte
			data[idx+1] = byte(depth >> 8) // high byte
		}
	}
	return Frame{Kind: FrameKindDepth, Data: data, Width: p.width, Height: p.height, Channels: 1, Serial: serial}
}

func (p *PatternProvider) genRGB(serial uint64) Frame {
	data := make([]byte, p.width*p.height*2)
	phase := float64(serial%100) / 100.0
	barW := p.width / 6
	type rgbTriple struct{ r, g, b uint8 }
	bars := []rgbTriple{
		{255, 0, 0},
		{0, 255, 0},
		{0, 0, 255},
		{255, 255, 0},
		{255, 0, 255},
		{0, 255, 255},
	}

	for y := 0; y < p.height; y++ {
		scan := uint8(20 * sin8(phase+float64(y)/float64(p.height)*4))
		for x := 0; x < p.width; x++ {
			bi := (x / barW) % len(bars)
			bar := bars[bi]
			r := clampU16(uint16(bar.r) + uint16(scan))
			g := clampU16(uint16(bar.g) + uint16(scan))
			b := clampU16(uint16(bar.b) + uint16(scan))
			yy, cb, cr := rgbToYCbCr(r, g, b)
			idx := (y*p.width + x) * 2
			if x%2 == 0 {
				data[idx] = yy   // Y0
				data[idx+1] = cb // U (shared across the pair)
			} else {
				data[idx] = yy   // Y1
				data[idx+1] = cr // V (shared across the pair)
			}
		}
	}
	return Frame{Kind: FrameKindRGB, Data: data, Width: p.width, Height: p.height, Channels: 2, Serial: serial}
}

func (p *PatternProvider) Close() error { return nil }

func sin8(v float64) float64 {
	x := v * 3.14159 * 2
	s := x - x*x*x/6 + x*x*x*x*x/120
	if s > 1 {
		return 1
	}
	if s < -1 {
		return -1
	}
	return s
}

func clampU16(v uint16) uint8 {
	if v > 255 {
		return 255
	}
	return uint8(v)
}

func clampU32(v uint32) uint16 {
	if v > 65535 {
		return 65535
	}
	return uint16(v)
}

// rgbToYCbCr converts 8-bit RGB to Y'CbCr per BT.601 (full range).
// Uses the same coefficients as Go's image/color.RGBToYCbCr.
func rgbToYCbCr(r, g, b uint8) (uint8, uint8, uint8) {
	r1 := int32(r)
	g1 := int32(g)
	b1 := int32(b)
	yy := (19595*r1 + 38470*g1 + 7471*b1 + 32768) >> 16
	cb := (-11058*r1 - 21710*g1 + 32768*b1 + 8421376) >> 16
	cr := (32768*r1 - 27439*g1 - 5329*b1 + 8421376) >> 16
	return uint8(yy), uint8(cb), uint8(cr)
}

var _ Provider = (*PatternProvider)(nil)
