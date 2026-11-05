package ddst

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"runtime"
	"strings"
	"testing"

	"github.com/kipavy/ricoh-spc240/internal/halftone"
	"github.com/kipavy/ricoh-spc240/internal/jbig"
	"github.com/kipavy/ricoh-spc240/internal/raster"
)

// A4 at 600 dpi as Windows sends it: 6814 real rows in 27 bands (26 x 256
// + 158), GDIP declaring 6816 lines and the short band 160.
const a4W, a4H, a4Decl, a4Bands, a4Rem = 4768, 6814, 6816, 27, 158

// identityScreens returns 1x1 synthetic screens of the given depth mapping
// level v to v & (2^depth-1), so a page of small levels passes through
// halftoning unchanged.
func identityScreens(t testing.TB, depth int) [4]*halftone.Screen {
	t.Helper()
	data := []byte("1 1 1 8 " + string(rune('0'+depth)))
	data = append(data, 0)
	packed := make([]byte, 256*depth/8)
	for v := 0; v < 256; v++ {
		bit := v * depth
		packed[bit/8] |= byte(v&(1<<depth-1)) << (8 - depth - bit%8)
	}
	s, err := halftone.ParseScreen(append(data, packed...))
	if err != nil {
		t.Fatal(err)
	}
	return [4]*halftone.Screen{s, s, s, s}
}

func a4(t testing.TB) PaperSize {
	t.Helper()
	p, err := LookupPaper("A4")
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func opts(t testing.TB, color bool, d Duplex) Options {
	return Options{Title: "test", Paper: a4(t), Color: color, Quality: halftone.Fast,
		Duplex: d, Copies: 1, Screens: identityScreens(t, 1)}
}

// page returns an A4 page with ink 1 in channel ch at the given pixels.
func page(channels, ch int, pts ...[2]int) *raster.Page {
	return sizedPage(a4W, a4H, channels, ch, pts...)
}

func sizedPage(w, h, channels, ch int, pts ...[2]int) *raster.Page {
	p := &raster.Page{W: w, H: h, Channels: channels, Pix: make([]byte, w*h*channels)}
	for _, xy := range pts {
		p.Pix[(xy[1]*w+xy[0])*channels+ch] = 1
	}
	return p
}

// band is one GDIB as sent: flags, declared lines, and the JBIG YD and L0
// of K (Windows: L0 = min(256, YD)).
type band struct{ flags, lines, yd, l0 int }

func bandList(t testing.TB, p sentPage) []band {
	t.Helper()
	var out []band
	for _, b := range p.bands {
		_, yd, _, err := jbig.Decode(b.Planes[0])
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, band{int(b.Header[7]), int(binary.BigEndian.Uint16(b.Header[26:])), yd,
			int(binary.BigEndian.Uint32(b.Planes[0][12:]))})
	}
	return out
}

// wantBands builds the Windows band list for r real rows: 256-row bands,
// the remainder band declared 2 lines more than it encodes, placed last
// (normal) or first (rotated back page).
func wantBands(r int, rotated bool) []band {
	n := (r + 255) / 256
	rem := r - (n-1)*256
	var out []band
	for i := 0; i < n; i++ {
		b := band{lines: 256, yd: 256, l0: 256}
		if (!rotated && i == n-1) || (rotated && i == 0) {
			b = band{lines: rem + 2, yd: rem, l0: rem}
		}
		if i == 0 {
			b.flags |= 1
		}
		if i == n-1 {
			b.flags |= 2
		}
		out = append(out, b)
	}
	return out
}

func checkBands(t testing.TB, what string, p sentPage, want []band) {
	t.Helper()
	got := bandList(t, p)
	if len(got) != len(want) {
		t.Errorf("%s: %d bands, want %d", what, len(got), len(want))
		return
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("%s: band %d = %+v, want %+v", what, i, got[i], want[i])
		}
	}
}

// run writes the pages as one job and parses the result.
func run(t testing.TB, o Options, pages ...*raster.Page) []Record {
	t.Helper()
	var buf bytes.Buffer
	j, err := NewJob(&buf, o)
	if err != nil {
		t.Fatal(err)
	}
	for i, p := range pages {
		if err := j.AddPage(p); err != nil {
			t.Fatalf("page %d: %v", i+1, err)
		}
	}
	if err := j.Close(); err != nil {
		t.Fatal(err)
	}
	recs, err := Parse(&buf)
	if err != nil {
		t.Fatal(err)
	}
	return recs
}

type sentPage struct {
	hdr   []byte
	bands []Record
}

func (p sentPage) side() byte   { return p.hdr[33] }
func (p sentPage) no() int      { return int(binary.BigEndian.Uint16(p.hdr[34:])) }
func (p sentPage) planes() byte { return p.hdr[32] }

func pagesOf(t testing.TB, recs []Record) []sentPage {
	t.Helper()
	var out []sentPage
	for _, r := range recs {
		switch r.Kind {
		case "GDIP":
			out = append(out, sentPage{hdr: r.Header})
		case "GDIB":
			if len(out) == 0 {
				t.Fatal("GDIB before GDIP")
			}
			out[len(out)-1].bands = append(out[len(out)-1].bands, r)
		}
	}
	return out
}

func order(ps []sentPage) [][2]int {
	var o [][2]int
	for _, p := range ps {
		o = append(o, [2]int{p.no(), int(p.side())})
	}
	return o
}

func equalOrder(a, b [][2]int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// plane decodes plane i (0=K,1=Y,2=M,3=C) of a whole page into rows.
func plane(t testing.TB, p sentPage, i int) []byte {
	t.Helper()
	var all []byte
	for _, b := range p.bands {
		_, _, rows, err := jbig.Decode(b.Planes[i])
		if err != nil {
			t.Fatal(err)
		}
		all = append(all, rows...)
	}
	return all
}

func setBits(rows []byte, stride int) [][2]int {
	var pts [][2]int
	for i, b := range rows {
		for k := 0; k < 8; k++ {
			if b&(0x80>>k) != 0 {
				pts = append(pts, [2]int{(i%stride)*8 + k, i / stride})
			}
		}
	}
	return pts
}

func TestJobSimplexStructure(t *testing.T) {
	o := opts(t, true, DuplexNone)
	recs := run(t, o, page(4, 3, [2]int{5, 7}, [2]int{a4W - 1, a4H - 1}))
	if len(recs) != 3+a4Bands+1 {
		t.Fatalf("%d records, want %d", len(recs), 3+a4Bands+1)
	}
	if recs[0].Kind != "GDIJ" || !bytes.Equal(recs[0].Header, JobHeader{Title: "test", Color: true, BPP: 1}.Bytes()) {
		t.Errorf("GDIJ = %x", recs[0].Header)
	}
	if recs[1].Kind != "GJET" || !bytes.Equal(recs[1].Header, GJETBytes()) {
		t.Errorf("GJET = %x", recs[1].Header)
	}
	want := PageHeader{Paper: o.Paper, WidthBits: a4W, Height: a4Decl, Planes: 4, Side: SideSimplex,
		PageNo: 1, Bands: a4Bands, MediaUM: o.Paper.MediaUM}.Bytes()
	if recs[2].Kind != "GDIP" || !bytes.Equal(recs[2].Header, want) {
		t.Errorf("GDIP = %x\nwant   %x", recs[2].Header, want)
	}
	for i, r := range recs[3 : 3+a4Bands] {
		h := r.Header
		if r.Kind != "GDIB" {
			t.Fatalf("record %d is %s", 3+i, r.Kind)
		}
		var flags byte
		if i == 0 {
			flags |= 1
		}
		if i == a4Bands-1 {
			flags |= 2
		}
		lines := 256
		if i == a4Bands-1 {
			lines = a4Rem + 2 // two phantom lines, as Windows declares
		}
		if h[7] != flags || binary.BigEndian.Uint16(h[24:]) != a4W || int(binary.BigEndian.Uint16(h[26:])) != lines || h[30] != 0 {
			t.Errorf("band %d header %x", i, h)
		}
		for k := 0; k < 4; k++ {
			n := binary.BigEndian.Uint32(h[8+4*k:])
			if n == 0 || n%8 != 0 || int(n) != len(r.Planes[k]) {
				t.Errorf("band %d plane %d length %d (data %d)", i, k, n, len(r.Planes[k]))
			}
		}
	}
	if recs[len(recs)-1].Kind != "JIDG" {
		t.Errorf("last record %s", recs[len(recs)-1].Kind)
	}
	// K plane holds the two marks, C/M/Y are empty.
	p := pagesOf(t, recs)[0]
	checkBands(t, "simplex", p, wantBands(a4H, false))
	if got := setBits(plane(t, p, 0), a4W/8); len(got) != 2 || got[0] != [2]int{5, 7} || got[1] != [2]int{a4W - 1, a4H - 1} {
		t.Errorf("K marks = %v", got)
	}
	for k := 1; k < 4; k++ {
		if got := setBits(plane(t, p, k), a4W/8); len(got) != 0 {
			t.Errorf("plane %d marks = %v", k, got)
		}
	}
}

// The data order is K, Y, M, C while the page channels are C, M, Y, K.
func TestJobPlaneOrder(t *testing.T) {
	pg := page(4, 0, [2]int{0, 0}) // C at x=0
	pg.Pix[(0*a4W+1)*4+1] = 1      // M at x=1
	pg.Pix[(0*a4W+2)*4+2] = 1      // Y at x=2
	pg.Pix[(0*a4W+3)*4+3] = 1      // K at x=3
	p := pagesOf(t, run(t, opts(t, true, DuplexNone), pg))[0]
	for i, x := range []int{3, 2, 1, 0} { // K, Y, M, C
		if got := setBits(plane(t, p, i), a4W/8); len(got) != 1 || got[0] != [2]int{x, 0} {
			t.Errorf("stream plane %d marks = %v, want x=%d", i, got, x)
		}
	}
}

func TestJobDuplexEvenLongEdge(t *testing.T) {
	o := opts(t, false, DuplexLongEdge)
	recs := run(t, o, page(1, 0, [2]int{1, 1}), page(1, 0, [2]int{a4W - 1, a4H - 1}))
	if got := binary.BigEndian.Uint16(recs[0].Header[16:]); got != 0x0200 {
		t.Errorf("GDIJ flags %#x, want 0x0200", got)
	}
	ps := pagesOf(t, recs)
	if want := [][2]int{{2, 0x0D}, {1, 5}}; !equalOrder(order(ps), want) {
		t.Fatalf("order %v, want %v", order(ps), want)
	}
	// Back is sent first and is rotated 180 for long edge (Windows): the
	// bottom-right mark ends up top-left, the short band comes first.
	if got := setBits(plane(t, ps[0], 0), a4W/8); len(got) != 1 || got[0] != [2]int{0, 0} {
		t.Errorf("back marks = %v", got)
	}
	checkBands(t, "long-edge back", ps[0], wantBands(a4H, true))
	if got := setBits(plane(t, ps[1], 0), a4W/8); len(got) != 1 || got[0] != [2]int{1, 1} {
		t.Errorf("front marks = %v", got)
	}
	checkBands(t, "front", ps[1], wantBands(a4H, false))
}

// A long-edge back page is the 180° rotation of the page given, pixel for
// pixel, including marks in the short band and at the band boundaries.
func TestJobLongEdgeBackIsRotated(t *testing.T) {
	pts := [][2]int{{0, 0}, {5, 7}, {a4W - 1, 0}, {17, 255}, {18, 256}, {19, a4Rem - 1},
		{20, a4Rem}, {21, a4H - a4Rem - 1}, {22, a4H - a4Rem}, {a4W - 1, a4H - 1}}
	recs := run(t, opts(t, true, DuplexLongEdge), page(4, 3), page(4, 3, pts...))
	back := pagesOf(t, recs)[0]
	if back.side() != byte(SideBack) {
		t.Fatalf("first page side %#x", back.side())
	}
	got := setBits(plane(t, back, 0), a4W/8)
	want := map[[2]int]bool{}
	for _, p := range pts {
		want[[2]int{a4W - 1 - p[0], a4H - 1 - p[1]}] = true
	}
	if len(got) != len(want) {
		t.Fatalf("back marks = %v, want %v", got, want)
	}
	for _, p := range got {
		if !want[p] {
			t.Errorf("unexpected back mark %v", p)
		}
	}
	checkBands(t, "long-edge back", back, wantBands(a4H, true))
}

func TestJobDuplexOddShortEdge(t *testing.T) {
	o := opts(t, true, DuplexShortEdge)
	back := page(4, 3, [2]int{a4W - 1, a4H - 1})
	recs := run(t, o, page(4, 3, [2]int{1, 1}), back, page(4, 3, [2]int{2, 2}))
	ps := pagesOf(t, recs)
	if want := [][2]int{{2, 0x0D}, {1, 5}, {3, 1}}; !equalOrder(order(ps), want) {
		t.Fatalf("order %v, want %v", order(ps), want)
	}
	// Short-edge back is not rotated (Windows), its bands are laid out as
	// the front's.
	if got := setBits(plane(t, ps[0], 0), a4W/8); len(got) != 1 || got[0] != [2]int{a4W - 1, a4H - 1} {
		t.Errorf("short-edge back marks = %v", got)
	}
	for i, p := range ps {
		checkBands(t, fmt.Sprintf("short-edge page %d", i), p, wantBands(a4H, false))
	}
	// The odd last page is a simplex sheet and is not rotated.
	if got := setBits(plane(t, ps[2], 0), a4W/8); len(got) != 1 || got[0] != [2]int{2, 2} {
		t.Errorf("last page marks = %v", got)
	}
}

func TestPageRaster(t *testing.T) {
	// Real rows Windows encodes; GDIP declares 2 more (6816, 4762, 6402).
	cases := map[string][2]int{"A4": {4768, 6814}, "A5": {3296, 4760}, "Letter": {4928, 6400}}
	for name, want := range cases {
		p, _ := LookupPaper(name)
		if w, h := PageRaster(p); w != want[0] || h != want[1] {
			t.Errorf("%s: %dx%d, want %dx%d", name, w, h, want[0], want[1])
		}
	}
}

// Letter is 25 full bands of real rows: the last band (first on a rotated
// back) is declared 258 lines with YD 256. A5 ends with 154 / 152.
func TestJobBandsLetterAndA5(t *testing.T) {
	letter, _ := LookupPaper("Letter")
	a5, _ := LookupPaper("A5")
	for _, c := range []struct {
		paper  PaperSize
		duplex Duplex
		r      int
		bands  int
	}{
		{letter, DuplexLongEdge, 6400, 25},
		{letter, DuplexShortEdge, 6400, 25},
		{a5, DuplexNone, 4760, 19},
	} {
		o := opts(t, false, c.duplex)
		o.Paper = c.paper
		w, _ := PageRaster(c.paper)
		pages := []*raster.Page{sizedPage(w, c.r, 1, 0, [2]int{1, 2}), sizedPage(w, c.r, 1, 0, [2]int{3, 4})}
		ps := pagesOf(t, run(t, o, pages...))
		for _, p := range ps {
			name := fmt.Sprintf("%s duplex %d page %d", c.paper.Name, c.duplex, p.no())
			if ht := int(binary.BigEndian.Uint16(p.hdr[14:])); ht != c.r+2 {
				t.Errorf("%s: GDIP height %d, want %d", name, ht, c.r+2)
			}
			if nb := int(binary.BigEndian.Uint16(p.hdr[54:])); nb != c.bands {
				t.Errorf("%s: GDIP bands %d, want %d", name, nb, c.bands)
			}
			rotated := c.duplex == DuplexLongEdge && p.no() == 2
			checkBands(t, name, p, wantBands(c.r, rotated))
			x, y := 2*p.no()-1, 2*p.no()
			if rotated {
				x, y = w-1-x, c.r-1-y
			}
			if got := setBits(plane(t, p, 0), w/8); len(got) != 1 || got[0] != [2]int{x, y} {
				t.Errorf("%s: marks %v, want %v", name, got, [2]int{x, y})
			}
		}
	}
}

func TestPageBlankIsSinglePlane(t *testing.T) {
	recs := run(t, opts(t, true, DuplexNone), page(4, 0), page(4, 0, [2]int{3, 3}), page(1, 0, [2]int{4, 4}))
	ps := pagesOf(t, recs)
	if ps[0].planes() != 1 || ps[1].planes() != 4 || ps[2].planes() != 1 {
		t.Fatalf("planes = %d %d %d, want 1 4 1", ps[0].planes(), ps[1].planes(), ps[2].planes())
	}
	for i, b := range ps[0].bands {
		h := b.Header
		last := i == len(ps[0].bands)-1
		if (h[30] == 1) != last {
			t.Errorf("blank band %d: blank byte %d", i, h[30])
		}
		for k := 1; k < 4; k++ {
			if binary.BigEndian.Uint32(h[8+4*k:]) != 0 || len(b.Planes[k]) != 0 {
				t.Errorf("blank band %d: plane %d not empty", i, k)
			}
		}
	}
	if got := setBits(plane(t, ps[0], 0), a4W/8); len(got) != 0 {
		t.Errorf("blank page K marks %v", got)
	}
	for _, p := range ps[1:] {
		if p.bands[len(p.bands)-1].Header[30] != 0 {
			t.Error("non-blank page flagged blank")
		}
	}
	// A 1-channel page in a colour job goes through the K screen.
	if got := setBits(plane(t, ps[2], 0), a4W/8); len(got) != 1 || got[0] != [2]int{4, 4} {
		t.Errorf("gray page K marks = %v", got)
	}
	if recs[0].Header[24] != 1 {
		t.Error("colour byte should stay 1")
	}
}

func TestJobMono(t *testing.T) {
	recs := run(t, opts(t, false, DuplexNone), page(1, 0, [2]int{1, 1}), page(1, 0))
	if recs[0].Header[24] != 0 {
		t.Errorf("colour byte %d", recs[0].Header[24])
	}
	for i, p := range pagesOf(t, recs) {
		if p.planes() != 1 {
			t.Errorf("page %d planes %d", i+1, p.planes())
		}
	}
	j, err := NewJob(io.Discard, opts(t, false, DuplexNone))
	if err != nil {
		t.Fatal(err)
	}
	if err := j.AddPage(page(4, 0)); err == nil {
		t.Error("mono job accepted a CMYK page")
	}
}

func TestJobCopiesCollate(t *testing.T) {
	pages := func() []*raster.Page { return []*raster.Page{page(1, 0, [2]int{1, 1}), page(1, 0, [2]int{2, 2})} }
	cases := []struct {
		name    string
		collate bool
		duplex  Duplex
		n       int
		flags   uint16
		want    [][2]int
	}{
		{"collated", true, DuplexNone, 2, 0x0008, [][2]int{{1, 1}, {2, 1}, {1, 1}, {2, 1}}},
		{"uncollated", false, DuplexNone, 2, 0, [][2]int{{1, 1}, {1, 1}, {2, 1}, {2, 1}}},
		{"duplex odd", true, DuplexLongEdge, 3, 0x0208,
			[][2]int{{2, 0x0D}, {1, 5}, {3, 1}, {2, 0x0D}, {1, 5}, {3, 1}}},
		{"duplex uncollated", false, DuplexLongEdge, 2, 0x0200,
			[][2]int{{2, 0x0D}, {1, 5}, {2, 0x0D}, {1, 5}}},
	}
	for _, c := range cases {
		o := opts(t, false, c.duplex)
		o.Copies, o.Collate = 2, c.collate
		ps := pages()
		if c.n == 3 {
			ps = append(ps, page(1, 0, [2]int{3, 3}))
		}
		recs := run(t, o, ps...)
		if got := binary.BigEndian.Uint16(recs[0].Header[16:]); got != c.flags {
			t.Errorf("%s: flags %#x, want %#x", c.name, got, c.flags)
		}
		if binary.BigEndian.Uint16(recs[0].Header[10:]) != 1 {
			t.Errorf("%s: GDIJ copies must stay 1", c.name)
		}
		sent := pagesOf(t, recs)
		if !equalOrder(order(sent), c.want) {
			t.Errorf("%s: order %v, want %v", c.name, order(sent), c.want)
		}
		// Each page number always carries the same content (long-edge backs
		// rotated 180°).
		for _, p := range sent {
			want := [2]int{p.no(), p.no()}
			if p.side() == byte(SideBack) {
				want = [2]int{a4W - 1 - p.no(), a4H - 1 - p.no()}
			}
			if got := setBits(plane(t, p, 0), a4W/8); len(got) != 1 || got[0] != want {
				t.Errorf("%s: page %d marks %v", c.name, p.no(), got)
			}
		}
	}
	o := opts(t, false, DuplexNone)
	o.Copies = 0
	if _, err := NewJob(io.Discard, o); err == nil {
		t.Error("copies 0 accepted")
	}
}

func TestJobRejectsDuplexA5(t *testing.T) {
	a5, _ := LookupPaper("A5")
	thick, _ := LookupPaperType("thick")
	bad := map[string]func(o *Options){
		"A5 duplex":    func(o *Options) { o.Paper = a5; o.Duplex = DuplexLongEdge },
		"thick duplex": func(o *Options) { o.Type = thick; o.Duplex = DuplexShortEdge },
		"bpp 3":        func(o *Options) { o.Quality = 3 },
		"nil screen":   func(o *Options) { o.Screens[0] = nil },
		"depth":        func(o *Options) { o.Quality = halftone.Standard },
		"bad duplex":   func(o *Options) { o.Duplex = 7 },
		"custom paper": func(o *Options) { o.Paper, _ = LookupPaper("Custom") },
	}
	for name, mod := range bad {
		o := opts(t, true, DuplexNone)
		mod(&o)
		var buf bytes.Buffer
		if _, err := NewJob(&buf, o); err == nil {
			t.Errorf("%s: accepted", name)
		}
		if buf.Len() != 0 {
			t.Errorf("%s: %d bytes written before the error", name, buf.Len())
		}
	}
	// Mono needs only the K screen.
	o := opts(t, false, DuplexNone)
	o.Screens[0], o.Screens[1], o.Screens[2] = nil, nil, nil
	if _, err := NewJob(io.Discard, o); err != nil {
		t.Errorf("mono with K screen only: %v", err)
	}
}

func TestJobRejectsWrongPageSize(t *testing.T) {
	j, err := NewJob(io.Discard, opts(t, true, DuplexNone))
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []*raster.Page{
		nil,
		{W: a4W, H: a4H + 1, Channels: 4, Pix: make([]byte, a4W*(a4H+1)*4)},
		{W: a4W, H: a4H, Channels: 3, Pix: make([]byte, a4W*a4H*3)},
		{W: a4W, H: a4H, Channels: 4, Pix: make([]byte, 10)},
	} {
		if err := j.AddPage(p); err == nil {
			t.Errorf("accepted page %+v", p)
		}
	}
}

type countWriter struct{ n int }

func (c *countWriter) Write(p []byte) (int, error) { c.n += len(p); return len(p), nil }

func TestJobWritesSimplexPageImmediately(t *testing.T) {
	var buf bytes.Buffer
	j, err := NewJob(&buf, opts(t, false, DuplexNone))
	if err != nil {
		t.Fatal(err)
	}
	if buf.Len() != 120+168 {
		t.Fatalf("after NewJob %d bytes, want 288", buf.Len())
	}
	if err := j.AddPage(page(1, 0, [2]int{1, 1})); err != nil {
		t.Fatal(err)
	}
	recs, err := Parse(bytes.NewReader(buf.Bytes()))
	if err == nil {
		t.Error("stream without JIDG parsed without error")
	}
	if len(recs) != 3+a4Bands {
		t.Errorf("after page 1: %d records, want %d", len(recs), 3+a4Bands)
	}
}

func TestJobStreamsPagesIncrementally(t *testing.T) {
	if testing.Short() {
		t.Skip("12 A4 colour pages")
	}
	var cw countWriter
	j, err := NewJob(&cw, opts(t, true, DuplexNone))
	if err != nil {
		t.Fatal(err)
	}
	var ms runtime.MemStats
	last := cw.n
	for i := 0; i < 12; i++ {
		p := page(4, i%4)
		for y := 0; y < a4H; y += 37 { // some ink on every band
			p.Pix[(y*a4W+(y*7)%a4W)*4+i%4] = 1
		}
		if err := j.AddPage(p); err != nil {
			t.Fatal(err)
		}
		p = nil
		if cw.n <= last {
			t.Fatalf("page %d not written by AddPage", i+1)
		}
		last = cw.n
		runtime.GC()
		runtime.ReadMemStats(&ms)
		if ms.HeapInuse > 400<<20 {
			t.Fatalf("page %d: heap in use %d MB", i+1, ms.HeapInuse>>20)
		}
	}
	if err := j.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestDecodeRoundTrip(t *testing.T) {
	o := opts(t, true, DuplexNone)
	o.Quality = halftone.Standard
	o.Screens = identityScreens(t, 2)
	pg := page(4, 0)
	seed := uint32(1)
	for i := range pg.Pix {
		seed = seed*1664525 + 1013904223
		if seed>>28 == 0 { // sparse, all channels
			pg.Pix[i] = byte(seed>>8) & 3
		}
	}
	// Expected 2-bpp planes in stream order K, Y, M, C.
	stride := a4W * 2 / 8
	var want [4][]byte
	for k := 0; k < 4; k++ {
		ch := 3 - k
		want[k] = make([]byte, stride*a4H)
		vals := make([]byte, a4W)
		for y := 0; y < a4H; y++ {
			for x := 0; x < a4W; x++ {
				vals[x] = pg.Pix[(y*a4W+x)*4+ch]
			}
			o.Screens[ch].Row(vals, y, want[k][y*stride:])
		}
	}
	p := pagesOf(t, run(t, o, pg))[0]
	if binary.BigEndian.Uint16(p.hdr[12:]) != a4W*2 {
		t.Fatalf("width bits %d", binary.BigEndian.Uint16(p.hdr[12:]))
	}
	for k := 0; k < 4; k++ {
		if got := plane(t, p, k); !bytes.Equal(got, want[k]) {
			t.Errorf("plane %d differs after round trip", k)
		}
	}
}

func TestParseErrors(t *testing.T) {
	var buf bytes.Buffer
	j, _ := NewJob(&buf, opts(t, false, DuplexNone))
	if err := j.AddPage(page(1, 0, [2]int{1, 1})); err != nil {
		t.Fatal(err)
	}
	j.Close()
	good := buf.Bytes()
	if _, err := Parse(bytes.NewReader(good)); err != nil {
		t.Fatal(err)
	}
	// Every truncation fails cleanly (no panic).
	for n := 0; n < len(good); n += 1 + n/16 {
		if _, err := Parse(bytes.NewReader(good[:n])); err == nil {
			t.Errorf("truncated to %d: no error", n)
		}
	}
	bad := func(off int, v ...byte) []byte {
		b := append([]byte{}, good...)
		copy(b[off:], v)
		return b
	}
	cases := map[string][]byte{
		"magic":       bad(0, 'X'),
		"gdij length": bad(4, 0, 0, 0, 1),
		"gdib length": bad(288+64+8, 0xff, 0xff, 0xff, 0xff),
		"after jidg":  append(append([]byte{}, good...), 0),
		"unknown":     bad(288, 'Z'),
	}
	for name, b := range cases {
		_, err := Parse(bytes.NewReader(b))
		if err == nil {
			t.Errorf("%s: no error", name)
		} else if !strings.Contains(err.Error(), "offset") {
			t.Errorf("%s: error without offset: %v", name, err)
		}
	}
	if _, err := Parse(iotestErr{}); err == nil {
		t.Error("read error not reported")
	}
}

type iotestErr struct{}

func (iotestErr) Read([]byte) (int, error) { return 0, errors.New("boom") }

type failWriter struct{ left int }

func (f *failWriter) Write(p []byte) (int, error) {
	if len(p) > f.left {
		return 0, errors.New("disk full")
	}
	f.left -= len(p)
	return len(p), nil
}

func TestJobWriteErrorIsSticky(t *testing.T) {
	j, err := NewJob(&failWriter{left: 400}, opts(t, false, DuplexNone))
	if err != nil {
		t.Fatal(err)
	}
	if err := j.AddPage(page(1, 0)); err == nil {
		t.Fatal("write error not reported")
	}
	if err := j.Close(); err == nil {
		t.Error("Close after write error returned nil")
	}
}
