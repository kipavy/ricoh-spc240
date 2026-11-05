package ddst

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kipavy/ricoh-spc240/internal/halftone"
	"github.com/kipavy/ricoh-spc240/internal/raster"
)

// TestAgainstWindowsCorpus re-encodes captures of the official Windows
// driver (RICOH_CORPUS = directory of .prn files) and compares them with
// ours: every header byte except the GDIJ title and the GDIB data lengths,
// and every JBIG stream up to its final FF 02 once the zero bytes Windows
// keeps before it are stripped. Planes are fed back through identity
// screens, so halftoning is bypassed. The stream carries no binding edge:
// a duplex capture named *long* / *short* must match that mode, any other
// passes if it matches either.
func TestAgainstWindowsCorpus(t *testing.T) {
	dir := os.Getenv("RICOH_CORPUS")
	if dir == "" {
		t.Skip("RICOH_CORPUS not set")
	}
	files, err := filepath.Glob(filepath.Join(dir, "*.prn"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no .prn files in %s (%v)", dir, err)
	}
	for _, f := range files {
		t.Run(filepath.Base(f), func(t *testing.T) {
			data, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			compareWithCorpus(t, filepath.Base(f), data)
		})
	}
}

func compareWithCorpus(t *testing.T, name string, data []byte) {
	theirs, err := Parse(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if len(theirs) < 3 || theirs[0].Kind != "GDIJ" {
		t.Fatal("stream does not start with GDIJ")
	}
	jh := theirs[0].Header
	flags := binary.BigEndian.Uint16(jh[16:])
	bpp := int(jh[33]) + 1
	duplex, collate := flags&0x0200 != 0, flags&0x0008 != 0
	sent := pagesOf(t, theirs)
	if len(sent) == 0 {
		t.Fatal("no pages")
	}
	ph := sent[0].hdr
	paper, ok := paperByMedia(ph[8], binary.BigEndian.Uint32(ph[56:]), binary.BigEndian.Uint32(ph[60:]))
	if !ok {
		t.Skipf("paper code %#x with media %x not in our table", ph[8], ph[56:64])
	}
	// h real rows; GDIP declares h+2 (phantom lines).
	w, h := PageRaster(paper)
	for _, p := range sent {
		wb, ht := int(binary.BigEndian.Uint16(p.hdr[12:])), int(binary.BigEndian.Uint16(p.hdr[14:]))
		if wb != w*bpp || ht != h+phantomLines || len(p.bands) != (h+255)/256 {
			t.Fatalf("raster %d bits x %d lines in %d bands, want %s %d bits x %d (+%d) in %d",
				wb, ht, len(p.bands), paper.Name, w*bpp, h, phantomLines, (h+255)/256)
		}
	}

	// Back to document order: a duplex sheet is sent back then front.
	var doc []sentPage
	for i := 0; i < len(sent); i++ {
		if sent[i].side() == byte(SideBack) {
			if i+1 == len(sent) || sent[i+1].side() != byte(SideFront) {
				t.Fatalf("back page %d not followed by its front", i)
			}
			doc = append(doc, sent[i+1], sent[i])
			i++
			continue
		}
		doc = append(doc, sent[i])
	}
	// Copies: a GDIJ copies field > 1 means the printer repeats each page
	// (uncollated, pages appear once); collated/duplex jobs replay the document.
	copies := int(binary.BigEndian.Uint16(jh[10:]))
	if copies > 1 {
		collate = false
	} else if collate || duplex {
		period := len(doc)
		for i := 1; i < len(doc); i++ {
			if doc[i].no() == 1 {
				period = i
				break
			}
		}
		copies = len(doc) / period
		doc = doc[:period]
	}

	o := Options{Title: "corpus", Paper: paper, Tray: Tray(ph[9]), Type: PaperType(ph[10]),
		Color: jh[24] == 1, Quality: halftone.Quality(bpp), Copies: copies, Collate: collate,
		Screens: identityScreens(t, bpp)}
	modes := []Duplex{DuplexNone}
	lname := strings.ToLower(name)
	switch {
	case !duplex:
	case strings.Contains(lname, "long"):
		modes = []Duplex{DuplexLongEdge}
	case strings.Contains(lname, "short"):
		modes = []Duplex{DuplexShortEdge}
	default:
		modes = []Duplex{DuplexLongEdge, DuplexShortEdge}
	}
	var diffs []string
	for _, mode := range modes {
		o.Duplex = mode
		var pages []*raster.Page
		for _, p := range doc {
			// The decoded planes hold the h real rows as sent.
			pg := rasterOf(t, p, w, h, bpp)
			if mode == DuplexLongEdge && p.side() == byte(SideBack) {
				pg.Rotate180() // undo the rotation Windows applied
			}
			pages = append(pages, pg)
		}
		diffs = compareRecords(run(t, o, pages...), theirs)
		if len(diffs) == 0 {
			return
		}
	}
	for i, d := range diffs {
		if i == 20 {
			t.Errorf("... %d more differences", len(diffs)-20)
			break
		}
		t.Error(d)
	}
}

func paperByMedia(code byte, wUM, hUM uint32) (PaperSize, bool) {
	for _, p := range papers {
		if p.Code == code && p.MediaUM == [2]uint32{wUM, hUM} {
			return p, true
		}
	}
	return PaperSize{}, false
}

// rasterOf decodes a sent page into an 8-bit page holding the raw d-bit
// pixel values (identity screens turn them back into the same planes).
func rasterOf(t *testing.T, p sentPage, w, h, bpp int) *raster.Page {
	t.Helper()
	ch := 1
	if p.planes() == 4 {
		ch = 4
	}
	pg := &raster.Page{W: w, H: h, Channels: ch, Pix: make([]byte, w*h*ch)}
	stride := w * bpp / 8
	mask := byte(1<<bpp - 1)
	for k := 0; k < int(p.planes()); k++ {
		c := 3 - k // stream K, Y, M, C -> channel C, M, Y, K
		if ch == 1 {
			c = 0
		}
		rows := plane(t, p, k)
		if len(rows) != stride*h {
			t.Fatalf("page %d plane %d: %d bytes, want %d", p.no(), k, len(rows), stride*h)
		}
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				bit := x * bpp
				v := rows[y*stride+bit/8] >> (8 - bpp - bit%8) & mask
				pg.Pix[(y*w+x)*ch+c] = v
			}
		}
	}
	return pg
}

// compareRecords lists the differences between our records and Windows'.
func compareRecords(ours, theirs []Record) []string {
	var d []string
	if len(ours) != len(theirs) {
		d = append(d, fmt.Sprintf("%d records, Windows has %d", len(ours), len(theirs)))
	}
	for i := 0; i < min(len(ours), len(theirs)); i++ {
		a, b := ours[i], theirs[i]
		if a.Kind != b.Kind {
			d = append(d, fmt.Sprintf("record %d: %s, Windows %s", i, a.Kind, b.Kind))
			continue
		}
		ha, hb := append([]byte{}, a.Header...), append([]byte{}, b.Header...)
		switch a.Kind {
		case "GDIJ":
			clear(ha[56:]) // title
			clear(hb[56:])
		case "GDIB":
			clear(ha[8:24]) // lengths depend on padding
			clear(hb[8:24])
		}
		if !bytes.Equal(ha, hb) {
			d = append(d, fmt.Sprintf("record %d (%s at %d): header\n ours %x\n  win %x", i, a.Kind, b.Offset, ha, hb))
		}
		for k := 0; k < 4; k++ {
			if !bytes.Equal(jbigCore(a.Planes[k]), jbigCore(b.Planes[k])) {
				d = append(d, fmt.Sprintf("record %d (GDIB at %d): %c stream differs", i, b.Offset, "KYMC"[k]))
			}
		}
	}
	return d
}

// jbigCore cuts a stream after its final FF 02 and drops the zero bytes
// right before it (Windows keeps them, jbigkit and we do not).
func jbigCore(s []byte) []byte {
	end := bytes.LastIndex(s, []byte{0xFF, 0x02})
	if end < 20 {
		return s
	}
	head := s[:end]
	for len(head) > 20 && head[len(head)-1] == 0 {
		head = head[:len(head)-1]
	}
	return append(append([]byte{}, head...), 0xFF, 0x02)
}

// The corpus comparison accepts our own output (a stand-in capture with
// Windows' layout), so its page reconstruction (phantom lines, rotated
// long-edge backs) is exercised without the corpus.
func TestCorpusComparisonOnOwnOutput(t *testing.T) {
	if testing.Short() {
		t.Skip("several A4 pages")
	}
	for _, c := range []struct {
		name   string
		duplex Duplex
	}{{"own_simplex.prn", DuplexNone}, {"own_long.prn", DuplexLongEdge}, {"own_short.prn", DuplexShortEdge}} {
		t.Run(c.name, func(t *testing.T) {
			var buf bytes.Buffer
			j, err := NewJob(&buf, opts(t, false, c.duplex))
			if err != nil {
				t.Fatal(err)
			}
			for _, p := range []*raster.Page{page(1, 0, [2]int{1, 1}, [2]int{9, a4H - 3}),
				page(1, 0, [2]int{2, 2}, [2]int{a4W - 5, a4H - 1}), page(1, 0, [2]int{3, 3})} {
				if err := j.AddPage(p); err != nil {
					t.Fatal(err)
				}
			}
			if err := j.Close(); err != nil {
				t.Fatal(err)
			}
			compareWithCorpus(t, c.name, buf.Bytes())
		})
	}
}
