// Package raster reads the page rasters rendered by Ghostscript (PAM CMYK or
// PGM gray) and provides the geometry and pixel transforms the job writer needs.
package raster

import (
	"bufio"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
)

// Page is a chunky 8-bit raster; 0 means no ink.
type Page struct {
	W, H     int
	Channels int // 4 = CMYK, 1 = K
	Pix      []byte
}

const maxDim = 1 << 20

// ReadPAM reads a P7 CMYK PAM (DEPTH 4, MAXVAL 255) or a P5 PGM (MAXVAL 255,
// inverted to a K channel).
func ReadPAM(r io.Reader) (*Page, error) {
	br := bufio.NewReaderSize(r, 1<<20)
	magic, err := br.ReadString('\n')
	if err != nil {
		return nil, fmt.Errorf("raster: reading magic: %w", err)
	}
	switch strings.TrimSpace(magic) {
	case "P7":
		return readP7(br)
	case "P5":
		return readP5(br)
	}
	return nil, fmt.Errorf("raster: unsupported magic %q (want P7 or P5)", strings.TrimSpace(magic))
}

func readP7(br *bufio.Reader) (*Page, error) {
	var w, h, depth, maxval int
	var tupl string
	for {
		line, err := br.ReadString('\n')
		if err != nil && line == "" {
			return nil, fmt.Errorf("raster: PAM header ended before ENDHDR: %w", err)
		}
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if line == "ENDHDR" {
			break
		}
		key, val, _ := strings.Cut(line, " ")
		val = strings.TrimSpace(val)
		switch key {
		case "WIDTH", "HEIGHT", "DEPTH", "MAXVAL":
			n, e := strconv.Atoi(val)
			if e != nil {
				return nil, fmt.Errorf("raster: bad %s %q", key, val)
			}
			switch key {
			case "WIDTH":
				w = n
			case "HEIGHT":
				h = n
			case "DEPTH":
				depth = n
			default:
				maxval = n
			}
		case "TUPLTYPE":
			tupl = val
		}
	}
	if maxval != 255 {
		return nil, fmt.Errorf("raster: PAM MAXVAL %d unsupported (want 255)", maxval)
	}
	if depth != 4 {
		return nil, fmt.Errorf("raster: PAM DEPTH %d unsupported (want 4, CMYK)", depth)
	}
	if tupl != "" && tupl != "CMYK" {
		return nil, fmt.Errorf("raster: PAM TUPLTYPE %q unsupported (want CMYK)", tupl)
	}
	return readPixels(br, w, h, 4)
}

// pnmToken returns the next whitespace-delimited header token, skipping
// comments. It consumes exactly one whitespace byte after the token.
func pnmToken(br *bufio.Reader) (string, error) {
	var sb strings.Builder
	for {
		c, err := br.ReadByte()
		if err != nil {
			return "", fmt.Errorf("raster: truncated PGM header: %w", err)
		}
		switch {
		case c == '#' && sb.Len() == 0:
			if _, err := br.ReadString('\n'); err != nil {
				return "", fmt.Errorf("raster: truncated PGM header: %w", err)
			}
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			if sb.Len() > 0 {
				return sb.String(), nil
			}
		default:
			sb.WriteByte(c)
		}
	}
}

func readP5(br *bufio.Reader) (*Page, error) {
	var v [3]int
	for i := range v {
		tok, err := pnmToken(br)
		if err != nil {
			return nil, err
		}
		n, err := strconv.Atoi(tok)
		if err != nil {
			return nil, fmt.Errorf("raster: bad PGM header value %q", tok)
		}
		v[i] = n
	}
	if v[2] != 255 {
		return nil, fmt.Errorf("raster: PGM maxval %d unsupported (want 255)", v[2])
	}
	p, err := readPixels(br, v[0], v[1], 1)
	if err != nil {
		return nil, err
	}
	for i, g := range p.Pix {
		p.Pix[i] = 255 - g
	}
	return p, nil
}

func readPixels(br *bufio.Reader, w, h, ch int) (*Page, error) {
	if w <= 0 || h <= 0 || w > maxDim || h > maxDim {
		return nil, fmt.Errorf("raster: invalid dimensions %dx%d", w, h)
	}
	pix := make([]byte, w*h*ch)
	if _, err := io.ReadFull(br, pix); err != nil {
		return nil, fmt.Errorf("raster: short pixel data for %dx%d (%d channels): %w", w, h, ch, err)
	}
	return &Page{W: w, H: h, Channels: ch, Pix: pix}, nil
}

// PrintableArea returns the raster size sent to the printer for a page of the
// given size in points: w = roundup32(px-200), h = px-200.
func PrintableArea(widthPt, heightPt float64, dpi int) (w, h int) {
	px := func(pt float64) int { return int(math.Round(pt / 72 * float64(dpi))) }
	w = (px(widthPt) - 200 + 31) &^ 31
	h = px(heightPt) - 200
	return w, h
}

// Crop returns a new w×h page, centred on the source: surplus columns/rows are
// dropped (floor of half on the left/top); a smaller source is padded with no ink.
func (p *Page) Crop(w, h int) *Page {
	ch := p.Channels
	out := &Page{W: w, H: h, Channels: ch, Pix: make([]byte, w*h*ch)}
	offX, offY := (p.W-w)/2, (p.H-h)/2 // Go truncates toward zero
	if p.W < w {
		offX = -((w - p.W) / 2)
	}
	if p.H < h {
		offY = -((h - p.H) / 2)
	}
	// copy range in destination coordinates
	x0, x1 := max(0, -offX), min(w, p.W-offX)
	if x1 <= x0 {
		return out
	}
	for y := 0; y < h; y++ {
		sy := y + offY
		if sy < 0 || sy >= p.H {
			continue
		}
		copy(out.Pix[(y*w+x0)*ch:(y*w+x1)*ch], p.Pix[(sy*p.W+x0+offX)*ch:(sy*p.W+x1+offX)*ch])
	}
	return out
}

// GrayToK moves neutral CMY (C == M == Y != 0) into K (protocol.md §8).
// No-op for single-channel pages.
func (p *Page) GrayToK() {
	if p.Channels != 4 {
		return
	}
	for i := 0; i+3 < len(p.Pix); i += 4 {
		c := p.Pix[i]
		if c != 0 && c == p.Pix[i+1] && c == p.Pix[i+2] {
			k := int(p.Pix[i+3]) + int(c)
			if k > 255 {
				k = 255
			}
			p.Pix[i], p.Pix[i+1], p.Pix[i+2], p.Pix[i+3] = 0, 0, 0, byte(k)
		}
	}
}

// Blank reports whether no pixel carries ink.
func (p *Page) Blank() bool {
	for _, b := range p.Pix {
		if b != 0 {
			return false
		}
	}
	return true
}

// Rotate180 rotates the page in place.
func (p *Page) Rotate180() {
	ch := p.Channels
	n := p.W * p.H
	for i, j := 0, n-1; i < j; i, j = i+1, j-1 {
		for c := 0; c < ch; c++ {
			p.Pix[i*ch+c], p.Pix[j*ch+c] = p.Pix[j*ch+c], p.Pix[i*ch+c]
		}
	}
}

// Rotate90CW returns a new page rotated 90 degrees clockwise: the source top
// edge becomes the right edge. Result pixel (x, y) is source (y, H-1-x).
func (p *Page) Rotate90CW() *Page {
	ch := p.Channels
	out := &Page{W: p.H, H: p.W, Channels: ch, Pix: make([]byte, len(p.Pix))}
	for y := 0; y < out.H; y++ {
		for x := 0; x < out.W; x++ {
			copy(out.Pix[(y*out.W+x)*ch:(y*out.W+x+1)*ch], p.Pix[((p.H-1-x)*p.W+y)*ch:((p.H-1-x)*p.W+y+1)*ch])
		}
	}
	return out
}
