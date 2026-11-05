// Package testpage builds a one-sheet duplex test job as rasters: the front
// carries colour checks, the back mono checks, all in the top third of the
// page so that ink use stays tiny.
package testpage

import (
	"fmt"

	"github.com/kipavy/ricoh-spc240/internal/ddst"
	"github.com/kipavy/ricoh-spc240/internal/raster"
)

const (
	mm10  = 236       // 10 mm at 600 dpi
	arm   = 118       // 5 mm
	rampW = 12 * mm10 // 120 mm
	rampH = 189       // 8 mm
)

// a4 is the test page paper.
func a4() ddst.PaperSize {
	p, err := ddst.LookupPaper("A4")
	if err != nil {
		panic(err)
	}
	return p
}

// canvas wraps a page for drawing with per-channel ink values.
type canvas struct{ *raster.Page }

func newCanvas(channels int) canvas {
	w, h := ddst.PageRaster(a4())
	return canvas{&raster.Page{W: w, H: h, Channels: channels, Pix: make([]byte, w*h*channels)}}
}

// set writes ink (one value per channel) at (x, y), ignoring out-of-range points.
func (c canvas) set(x, y int, ink ...byte) {
	if x < 0 || y < 0 || x >= c.W || y >= c.H {
		return
	}
	copy(c.Pix[(y*c.W+x)*c.Channels:], ink)
}

func (c canvas) rect(x, y, w, h int, ink ...byte) {
	for j := y; j < y+h; j++ {
		for i := x; i < x+w; i++ {
			c.set(i, j, ink...)
		}
	}
}

// text draws s with the 5x7 font at the given pixel scale in black.
func (c canvas) text(x, y, scale int, s string, ink ...byte) {
	for _, r := range s {
		g, ok := glyphs[r]
		if !ok {
			panic(fmt.Sprintf("testpage: no glyph for %q", r))
		}
		for row, line := range g {
			for col, ch := range line {
				if ch == '#' {
					c.rect(x+col*scale, y+row*scale, scale, scale, ink...)
				}
			}
		}
		x += 6 * scale
	}
}

// ramp draws a horizontal 0→255 ramp; f maps a level to the ink values.
func (c canvas) ramp(x, y, w, h int, f func(v byte) []byte) {
	for i := 0; i < w; i++ {
		ink := f(byte(i * 255 / (w - 1)))
		for j := 0; j < h; j++ {
			c.set(x+i, y+j, ink...)
		}
	}
}

// Pages returns fresh pages: [0] the colour front (CMYK), [1] the mono back (K).
func Pages() [2]*raster.Page {
	return [2]*raster.Page{front(), back()}
}

func front() *raster.Page {
	c := newCanvas(4)
	k := []byte{0, 0, 0, 255}
	c.text(100, 40, 6, "FRONT COLOUR TOP", k...)

	// Registration crosses: each separation draws 1 px lines through one point.
	for i, ch := range "CMYK" {
		cx, cy := 900+i*300, 250
		ink := make([]byte, 4)
		ink[i] = 255
		for d := -arm; d <= arm; d++ {
			c.set(cx+d, cy, ink...)
			c.set(cx, cy+d, ink...)
		}
		c.text(cx-12, cy+arm+20, 4, string(ch), k...)
	}

	type patch struct {
		name string
		ink  [4]byte // at 100 %
	}
	patches := []patch{
		{"C", [4]byte{1, 0, 0, 0}}, {"M", [4]byte{0, 1, 0, 0}},
		{"Y", [4]byte{0, 0, 1, 0}}, {"K", [4]byte{0, 0, 0, 1}},
		{"R=M+Y", [4]byte{0, 1, 1, 0}}, {"G=C+Y", [4]byte{1, 0, 1, 0}},
		{"B=C+M", [4]byte{1, 1, 0, 0}},
	}
	for row, level := range []byte{255, 128} {
		y := 520 + row*400
		for i, p := range patches {
			x := 100 + i*400
			ink := []byte{p.ink[0] * level, p.ink[1] * level, p.ink[2] * level, p.ink[3] * level}
			c.rect(x, y, mm10, mm10, ink...)
			cap := p.name
			if level != 255 {
				cap += " 50%"
			}
			c.text(x, y+mm10+14, 3, cap, k...)
		}
	}

	c.ramp(100, 1400, rampW, rampH, func(v byte) []byte { return []byte{0, 0, 0, v} })
	c.text(100, 1400+rampH+14, 4, "K RAMP", k...)
	c.ramp(100, 1750, rampW, rampH, func(v byte) []byte { return []byte{v, v, v, 0} })
	c.text(100, 1750+rampH+14, 4, "CMY RAMP", k...)
	return c.Page
}

func back() *raster.Page {
	c := newCanvas(1)
	k := []byte{255}
	c.text(100, 40, 6, "BACK MONO TOP", k...)

	// Arrow pointing up (triangle) under the label.
	cx, top, hgt := 250, 160, 200
	for j := 0; j < hgt; j++ {
		half := j / 2
		for i := -half; i <= half; i++ {
			c.set(cx+i, top+j, 255)
		}
	}
	c.rect(cx-15, top+hgt, 31, 60, 255)
	c.text(cx+150, top+90, 4, "UP", k...)

	c.ramp(100, 650, rampW, rampH, func(v byte) []byte { return []byte{v} })
	c.text(100, 650+rampH+14, 4, "K RAMP", k...)

	// 1, 2 and 4 px lines, 5 mm long: horizontal then vertical.
	for i, wd := range []int{1, 2, 4} {
		x := 100 + i*250
		c.rect(x, 1100, arm, wd, 255)           // horizontal
		c.rect(x+160, 1100-arm/2, wd, arm, 255) // vertical
	}
	c.text(100, 1250, 4, "1PX 2PX 4PX", k...)
	return c.Page
}
