package ddst

import (
	"bytes"

	"github.com/kipavy/ricoh-spc240/internal/halftone"
	"github.com/kipavy/ricoh-spc240/internal/jbig"
	"github.com/kipavy/ricoh-spc240/internal/raster"
)

const bandLines = 256

// phantomLines is how many lines the Windows driver declares beyond the
// real rows, in GDIP and in the short (remainder) band, whose JBIG images
// encode only the real rows.
const phantomLines = 2

// bandEncoder holds buffers reused for every band of every page.
type bandEncoder struct {
	vals []byte // one plane's 8-bit levels for one row
	rows []byte // one halftoned band of one plane
}

// encodePage halftones p and encodes it as GDIB records (protocol §7).
// Blank and 1-channel pages are sent as a single K plane. Bands hold 256
// rows except the remainder band (1..256 rows), which is last, or first
// when the page was rotated 180° (it is then the rotated bottom band).
func (j *Job) encodePage(p *raster.Page, rotated bool) *encodedPage {
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
	rem := j.height - (j.bands-1)*bandLines
	short := j.bands - 1 // index of the remainder band
	if rotated {
		short = 0
	}
	var body bytes.Buffer
	y0 := 0
	for b := 0; b < j.bands; b++ {
		lines, declared := bandLines, bandLines
		if b == short {
			lines, declared = rem, rem+phantomLines
		}
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
			// L0 = band height (min(256, lines)), as Windows writes it.
			data[k] = pad8(jbig.EncodeStripe(j.widthBits, lines, lines, rows))
		}
		last := b == j.bands-1
		h := BandHeader{First: b == 0, Last: last, BlankPage: blank && last,
			WidthBits: j.widthBits, Lines: declared}
		for k, d := range data {
			h.Lens[k] = uint32(len(d))
		}
		body.Write(h.Bytes())
		for _, d := range data {
			body.Write(d)
		}
		y0 += lines
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
