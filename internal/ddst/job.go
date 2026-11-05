package ddst

import (
	"errors"
	"fmt"
	"io"
	"math"

	"github.com/kipavy/ricoh-spc240/internal/halftone"
	"github.com/kipavy/ricoh-spc240/internal/raster"
)

// Duplex selects two-sided printing and its binding edge.
type Duplex int

const (
	DuplexNone Duplex = iota
	DuplexLongEdge
	DuplexShortEdge
)

// Options describe a print job.
type Options struct {
	Title   string
	Paper   PaperSize
	Tray    Tray
	Type    PaperType
	Color   bool // false: every page must be 1-channel (gray rendered as K)
	Quality halftone.Quality
	Duplex  Duplex
	Copies  int // >= 1; uncollated simplex copies are made by the printer
	Collate bool
	GrayToK bool                // move neutral CMY into K before halftoning
	Screens [4]*halftone.Screen // C, M, Y, K; a mono job needs only K
}

const maxU16 = 1<<16 - 1

// Job writes one DDST job. Pages are halftoned and JBIG-encoded as they are
// added; simplex pages are written immediately, a duplex front page is kept
// (encoded) until its back arrives. With several collated copies (or duplex
// copies) every encoded page is kept until Close to replay the document.
type Job struct {
	w             io.Writer
	o             Options
	bpp           int
	width, height int // expected page raster (real rows), pixels
	widthBits     int
	bands         int

	n       int          // pages added so far
	front   *encodedPage // duplex front waiting for its back
	frontNo int
	keep    bool
	kept    []*encodedPage
	enc     bandEncoder
	err     error // sticky write error
	closed  bool
}

// encodedPage is a page ready to send except for its GDIP side and number.
type encodedPage struct {
	planes int
	body   []byte // GDIB records with their plane data
}

// NewJob validates o and writes the GDIJ and GJET records. Nothing is
// written when o is invalid.
func NewJob(w io.Writer, o Options) (*Job, error) {
	if o.Copies < 1 || o.Copies > 999 {
		return nil, fmt.Errorf("ddst: copies must be between 1 and 999, got %d", o.Copies)
	}
	bpp := o.Quality.BPP()
	if bpp != 1 && bpp != 2 && bpp != 4 {
		return nil, fmt.Errorf("ddst: bits per pixel must be 1, 2 or 4, got %d", bpp)
	}
	switch o.Duplex {
	case DuplexNone:
	case DuplexLongEdge, DuplexShortEdge:
		if !o.Paper.Duplex {
			return nil, fmt.Errorf("ddst: duplex is not supported on %s paper", o.Paper.Name)
		}
		if !PaperTypeAllowsDuplex(o.Type) {
			return nil, fmt.Errorf("ddst: duplex is not supported on paper type %#x", byte(o.Type))
		}
	default:
		return nil, fmt.Errorf("ddst: invalid duplex mode %d", o.Duplex)
	}
	need := []int{3}
	if o.Color {
		need = []int{0, 1, 2, 3}
	}
	for _, i := range need {
		s := o.Screens[i]
		if s == nil {
			return nil, fmt.Errorf("ddst: missing %c screen", "CMYK"[i])
		}
		if s.Depth != bpp {
			return nil, fmt.Errorf("ddst: %c screen depth %d does not match %d bpp", "CMYK"[i], s.Depth, bpp)
		}
	}
	width, height := PageRaster(o.Paper)
	if width <= 0 || height <= 0 {
		return nil, fmt.Errorf("ddst: paper %s has no printable area", o.Paper.Name)
	}
	j := &Job{w: w, o: o, bpp: bpp, width: width, height: height,
		widthBits: width * bpp, bands: (height + bandLines - 1) / bandLines,
		keep: o.Copies > 1 && (o.Collate || o.Duplex != DuplexNone)}
	if j.widthBits > maxU16 || height+phantomLines > maxU16 {
		return nil, fmt.Errorf("ddst: page %dx%d at %d bpp exceeds the header limits", width, height, bpp)
	}
	hdr := JobHeader{Title: o.Title, Color: o.Color, BPP: bpp,
		Duplex: o.Duplex != DuplexNone, Collate: o.Collate && o.Copies > 1,
		Copies: j.printerCopies()}.Bytes()
	j.write(append(hdr, GJETBytes()...))
	if j.err != nil {
		return nil, j.err
	}
	return j, nil
}

// PageRaster returns the raster a page must have for paper p at 600 dpi:
// the printable width (raster.PrintableArea) and the rows the Windows
// driver really encodes, the GDIP media height minus 200 (A4 6814, A5 4760,
// Letter 6400). GDIP and the short band declare 2 more lines (protocol §5).
func PageRaster(p PaperSize) (w, h int) {
	w, _ = raster.PrintableArea(p.WidthPt, p.HeightPt, 600)
	h = int(math.Round(float64(p.MediaUM[1])*600/25400)) - 200
	return w, h
}

// AddPage halftones, encodes and queues page p, which must already be
// cropped to PageRaster(paper). p may be modified: long-edge duplex back
// pages are rotated in place, and GrayToK is applied in place.
func (j *Job) AddPage(p *raster.Page) error {
	if j.closed {
		return errors.New("ddst: AddPage after Close")
	}
	if j.err != nil {
		return j.err
	}
	if err := j.checkPage(p); err != nil {
		return err
	}
	n := j.n + 1
	if n > maxU16 {
		return fmt.Errorf("ddst: too many pages (%d)", n)
	}
	// Like the Windows driver: long-edge backs are rotated 180°,
	// short-edge backs go as they are.
	rotated := j.o.Duplex == DuplexLongEdge && n%2 == 0
	if rotated {
		p.Rotate180()
	}
	if j.o.GrayToK {
		p.GrayToK()
	}
	ep := j.encodePage(p, rotated)
	j.n = n
	if j.keep {
		j.kept = append(j.kept, ep)
	}
	j.place(ep, n)
	return j.err
}

func (j *Job) checkPage(p *raster.Page) error {
	if p == nil {
		return errors.New("ddst: nil page")
	}
	if p.W != j.width || p.H != j.height {
		return fmt.Errorf("ddst: page is %dx%d, want the %s page raster %dx%d",
			p.W, p.H, j.o.Paper.Name, j.width, j.height)
	}
	switch {
	case p.Channels == 4 && !j.o.Color:
		return errors.New("ddst: CMYK page in a mono job (render it as gray)")
	case p.Channels != 1 && p.Channels != 4:
		return fmt.Errorf("ddst: page has %d channels, want 1 or 4", p.Channels)
	}
	if len(p.Pix) != p.W*p.H*p.Channels {
		return fmt.Errorf("ddst: page has %d pixel bytes, want %d", len(p.Pix), p.W*p.H*p.Channels)
	}
	return nil
}

// place sends page n of the document following the Windows order: simplex
// in order, duplex back (even n) before front.
func (j *Job) place(ep *encodedPage, n int) {
	switch {
	case j.o.Duplex == DuplexNone:
		j.writePage(ep, SideSimplex, n)
	case n%2 == 1:
		j.front, j.frontNo = ep, n
	default:
		j.writePage(ep, SideBack, n)
		j.writePage(j.front, SideFront, j.frontNo)
		j.front = nil
	}
}

// printerCopies is the GDIJ copies field: uncollated simplex jobs send each
// page once and the printer repeats it (like Windows); collated and duplex
// copies replay the document instead.
func (j *Job) printerCopies() int {
	if j.o.Duplex == DuplexNone && !j.o.Collate {
		return j.o.Copies
	}
	return 1
}

// endCopy sends a lone duplex front page as a simplex sheet.
func (j *Job) endCopy() {
	if j.front != nil {
		j.writePage(j.front, SideSimplex, j.frontNo)
		j.front = nil
	}
}

func (j *Job) writePage(ep *encodedPage, side Side, no int) {
	h := PageHeader{Paper: j.o.Paper, Tray: j.o.Tray, Type: j.o.Type,
		WidthBits: j.widthBits, Height: j.height + phantomLines, Planes: ep.planes,
		Side: side, PageNo: no, Bands: j.bands, MediaUM: j.o.Paper.MediaUM}
	j.write(h.Bytes())
	j.write(ep.body)
}

func (j *Job) write(b []byte) {
	if j.err != nil {
		return
	}
	if _, err := j.w.Write(b); err != nil {
		j.err = fmt.Errorf("ddst: write: %w", err)
	}
}

// Close sends a pending duplex front page, the remaining copies and JIDG.
func (j *Job) Close() error {
	if j.closed {
		return errors.New("ddst: job already closed")
	}
	j.closed = true
	j.endCopy()
	if j.keep {
		for c := 1; c < j.o.Copies; c++ {
			for i, ep := range j.kept {
				j.place(ep, i+1)
			}
			j.endCopy()
		}
	}
	j.kept = nil
	j.write(JIDG)
	return j.err
}
