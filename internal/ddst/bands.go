package ddst

import (
	"bytes"

	"github.com/kipavy/ricoh-spc240/internal/halftone"
	"github.com/kipavy/ricoh-spc240/internal/jbig"
	"github.com/kipavy/ricoh-spc240/internal/raster"
)

const bandLines = 256

// bandEncoder holds buffers reused for every band of every page.
type bandEncoder struct {
	vals []byte // one plane's 8-bit levels for one row
	rows []byte // one halftoned band of one plane
}

// encodePage halftones p and encodes it as GDIB records (protocol §7).
// Blank and 1-channel pages are sent as a single K plane.
func (j *Job) encodePage(p *raster.Page) *encodedPage {
	stride := j.widthBits / 8 // width is a multiple of 32 pixels
	e := &j.enc
	if len(e.rows) != stride*bandLines {
		e.rows = make([]byte, stride*bandLines)
		e.vals = make([]byte, j.width)
	}
	blank := p.Blank()
	planes := 4
	if blank || p.Channels == 1 {
		planes = 1
	}
	var body bytes.Buffer
	for b := 0; b < j.bands; b++ {
		y0 := b * bandLines
		lines := min(bandLines, j.height-y0)
		var data [4][]byte // stream order K, Y, M, C
		for k := 0; k < planes; k++ {
			rows := e.rows[:lines*stride]
			switch {
			case blank:
				clear(rows)
			case planes == 1: // K only; 1-channel page
				e.halftone(p, 0, j.o.Screens[3], y0, lines, stride)
			default:
				ch := 3 - k // channels are C, M, Y, K
				e.halftone(p, ch, j.o.Screens[ch], y0, lines, stride)
			}
			data[k] = pad8(jbig.Encode(j.widthBits, lines, rows))
		}
		last := b == j.bands-1
		h := BandHeader{First: b == 0, Last: last, BlankPage: blank && last,
			WidthBits: j.widthBits, Lines: lines}
		for k, d := range data {
			h.Lens[k] = uint32(len(d))
		}
		body.Write(h.Bytes())
		for _, d := range data {
			body.Write(d)
		}
	}
	return &encodedPage{planes: planes, body: body.Bytes()}
}

// halftone screens channel ch of rows y0..y0+lines-1 into e.rows.
func (e *bandEncoder) halftone(p *raster.Page, ch int, s *halftone.Screen, y0, lines, stride int) {
	nc := p.Channels
	for i := 0; i < lines; i++ {
		y := y0 + i
		src := p.Pix[y*p.W*nc : (y+1)*p.W*nc]
		vals := src
		if nc > 1 {
			vals = e.vals[:p.W]
			for x := range vals {
				vals[x] = src[x*nc+ch]
			}
		}
		s.Row(vals, y, e.rows[i*stride:(i+1)*stride])
	}
}

// pad8 zero-pads a JBIG stream to a multiple of 8 bytes.
func pad8(b []byte) []byte {
	if r := len(b) % 8; r != 0 {
		b = append(b, make([]byte, 8-r)...)
	}
	return b
}
