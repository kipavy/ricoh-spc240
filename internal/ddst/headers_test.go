package ddst

import (
	"bytes"
	"math"
	"testing"

	"github.com/kipavy/ricoh-spc240/internal/raster"
)

// expect builds a zero buffer of n bytes with the given offset->bytes patches.
func expect(n int, patches map[int][]byte) []byte {
	b := make([]byte, n)
	for off, v := range patches {
		copy(b[off:], v)
	}
	return b
}

func TestJobHeaderMatchesWindows(t *testing.T) {
	title := make([]byte, 64)
	copy(title, "c1_color_simplex")
	base := map[int][]byte{
		0: []byte("GDIJ"), 4: {0, 0, 0, 0x78}, 8: {0, 1}, 10: {0, 1}, 18: {0, 0xa8},
		24: {1}, 32: {1}, 33: {0}, 34: {0, 2}, 56: title,
	}
	with := func(over map[int][]byte) map[int][]byte {
		m := map[int][]byte{}
		for k, v := range base {
			m[k] = v
		}
		for k, v := range over {
			m[k] = v
		}
		return m
	}
	const ttl = "c1_color_simplex"
	tests := []struct {
		name string
		h    JobHeader
		want map[int][]byte
	}{
		{"base", JobHeader{Title: ttl, Color: true, BPP: 1}, base},
		{"duplex", JobHeader{Title: ttl, Color: true, BPP: 1, Duplex: true}, with(map[int][]byte{16: {2, 0}})},
		{"collate", JobHeader{Title: ttl, Color: true, BPP: 1, Collate: true}, with(map[int][]byte{16: {0, 8}})},
		{"both", JobHeader{Title: ttl, Color: true, BPP: 1, Duplex: true, Collate: true}, with(map[int][]byte{16: {2, 8}})},
		{"bpp4", JobHeader{Title: ttl, Color: true, BPP: 4}, with(map[int][]byte{33: {3}})},
		{"mono", JobHeader{Title: ttl, Color: false, BPP: 1}, with(map[int][]byte{24: {0}})},
	}
	for _, tc := range tests {
		got := tc.h.Bytes()
		if len(got) != 120 {
			t.Fatalf("%s: len = %d, want 120", tc.name, len(got))
		}
		if want := expect(120, tc.want); !bytes.Equal(got, want) {
			t.Errorf("%s:\n got %x\nwant %x", tc.name, got, want)
		}
	}
}

func TestJobHeaderLongTitleTruncated(t *testing.T) {
	got := JobHeader{Title: string(bytes.Repeat([]byte("x"), 100)), BPP: 1}.Bytes()
	if len(got) != 120 || got[119] != 'x' {
		t.Fatalf("title not truncated to 64 bytes: len %d", len(got))
	}
}

func TestGJETMatchesWindows(t *testing.T) {
	want := expect(168, map[int][]byte{
		0: []byte("GJET"), 4: {0, 0, 0, 0xa8}, 148: {1, 1, 1, 1},
	})
	if got := GJETBytes(); !bytes.Equal(got, want) {
		t.Errorf("got %x\nwant %x", got, want)
	}
}

func TestPageHeaderMatchesWindows(t *testing.T) {
	a4, err := LookupPaper("A4")
	if err != nil {
		t.Fatal(err)
	}
	h := PageHeader{Paper: a4, WidthBits: 4768, Height: 6816, Planes: 4, Side: SideSimplex,
		PageNo: 1, Bands: 27, MediaUM: [2]uint32{209974, 296926}}
	var want []byte
	want = append(want, "GDIP"...)
	want = append(want, 0, 0, 0, 0x40, 0x09, 0, 0, 0, 0x12, 0xa0, 0x1a, 0xa0)
	for i := 0; i < 8; i++ {
		want = append(want, 0, 0x64)
	}
	want = append(want, 0x04, 0x01, 0x00, 0x01)
	want = append(want, make([]byte, 18)...)
	want = append(want, 0x00, 0x1b, 0x00, 0x03, 0x34, 0x36, 0x00, 0x04, 0x87, 0xde)
	if len(want) != 64 {
		t.Fatalf("test bug: want len %d", len(want))
	}
	if got := h.Bytes(); !bytes.Equal(got, want) {
		t.Errorf("got %x\nwant %x", got, want)
	}
}

func TestPageHeaderTrayTypeSide(t *testing.T) {
	h := PageHeader{Tray: 2, Type: 0x0C, Side: SideBack}.Bytes()
	if h[9] != 2 || h[10] != 0x0C || h[33] != 0x0D {
		t.Errorf("tray/type/side = %x %x %x", h[9], h[10], h[33])
	}
	if SideSimplex != 1 || SideFront != 5 || SideBack != 0x0D {
		t.Error("side constants")
	}
}

func TestBandHeader(t *testing.T) {
	flag := func(b BandHeader) byte { return b.Bytes()[7] }
	if flag(BandHeader{First: true}) != 1 || flag(BandHeader{Last: true}) != 2 || flag(BandHeader{First: true, Last: true}) != 3 {
		t.Error("flags")
	}
	got := BandHeader{Last: true, BlankPage: true, Lens: [4]uint32{1, 2, 3, 0x01020304}, WidthBits: 4768, Lines: 256}.Bytes()
	want := expect(32, map[int][]byte{
		0: []byte("GDIB"), 7: {2},
		8: {0, 0, 0, 1}, 12: {0, 0, 0, 2}, 16: {0, 0, 0, 3}, 20: {1, 2, 3, 4},
		24: {0x12, 0xa0}, 26: {1, 0}, 30: {1},
	})
	if !bytes.Equal(got, want) {
		t.Errorf("got %x\nwant %x", got, want)
	}
}

func TestJIDG(t *testing.T) {
	if string(JIDG) != "JIDG" {
		t.Error("JIDG")
	}
}

func TestLookupPaper(t *testing.T) {
	a4, err := LookupPaper("a4")
	if err != nil || a4.Code != 9 || !a4.Duplex {
		t.Errorf("A4 = %+v, %v", a4, err)
	}
	if !near(a4.WidthPt, 595.2756) || !near(a4.HeightPt, 841.8898) {
		t.Errorf("A4 points = %v x %v, want 595.2756 x 841.8898", a4.WidthPt, a4.HeightPt)
	}
	if a4.MediaUM != [2]uint32{209974, 296926} {
		t.Errorf("A4 MediaUM = %v", a4.MediaUM)
	}
	a5, err := LookupPaper("A5")
	if err != nil || a5.Code != 0x0B || a5.Duplex {
		t.Errorf("A5 = %+v, %v", a5, err)
	}
	if !near(a5.WidthPt, 419.5276) || !near(a5.HeightPt, 595.2756) || a5.MediaUM != [2]uint32{147998, 209974} {
		t.Errorf("A5 = %+v", a5)
	}
	if l, _ := LookupPaper("LETTER"); l.Code != 1 || l.MediaUM != [2]uint32{215900, 279400} || !near(l.WidthPt, 612) || !near(l.HeightPt, 792) {
		t.Errorf("Letter = %+v", l)
	}
	if _, err := LookupPaper("nope"); err == nil {
		t.Error("expected error for unknown paper")
	}
}

func TestPaperTable(t *testing.T) {
	codes := map[string]byte{
		"Letter": 1, "Legal": 5, "Ledger": 0x11, "Executive": 7, "HalfLetter": 6, "A3": 8, "A4": 9,
		"A5": 0x0B, "A6": 0x46, "B4": 0x0C, "B5": 0x0D, "B6": 0x58, "FS": 0x10, "Folio": 0x0F,
		"Foolscap": 0x0E, "Env10": 0x14, "Monarch": 0x25, "EnvDL": 0x1B, "EnvC5": 0x1C,
		"EnvC6": 0x1F, "8K": 0x5C, "16K": 0x5D, "Postcard": 0x2B, "ReplyPostcard": 0x45, "Custom": 0xFF,
	}
	noDuplex := map[string]bool{"A5": true, "B6": true, "A6": true, "HalfLetter": true, "Env10": true,
		"Monarch": true, "EnvDL": true, "EnvC5": true, "EnvC6": true, "Postcard": true,
		"ReplyPostcard": true, "Custom": true}
	for name, code := range codes {
		p, err := LookupPaper(name)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if p.Code != code {
			t.Errorf("%s: code %#x, want %#x", name, p.Code, code)
		}
		if p.Duplex == noDuplex[name] {
			t.Errorf("%s: Duplex = %v", name, p.Duplex)
		}
	}
}

func TestLookupPaperType(t *testing.T) {
	codes := map[string]PaperType{
		"plainrecycled": 0, "Plain": 1, "plain90": 0x10, "recycled": 2, "color": 3, "preprinted": 5,
		"prepunched": 6, "thick": 0x0C, "thick160": 0x0D, "letterhead": 4, "bond": 8, "cardstock": 9,
		"labels": 7, "envelope": 0x0E, "thin": 0x0F, "thinner": 0x12,
	}
	for name, code := range codes {
		got, err := LookupPaperType(name)
		if err != nil || got != code {
			t.Errorf("%s = %#x, %v; want %#x", name, got, err, code)
		}
	}
	if _, err := LookupPaperType("x"); err == nil {
		t.Error("expected error")
	}
	for _, n := range []string{"plain90", "thick", "thick160", "bond", "cardstock", "labels", "envelope"} {
		pt, _ := LookupPaperType(n)
		if PaperTypeAllowsDuplex(pt) {
			t.Errorf("%s should refuse duplex", n)
		}
	}
	for _, n := range []string{"plainrecycled", "plain", "recycled", "color", "thin", "thinner", "letterhead"} {
		pt, _ := LookupPaperType(n)
		if !PaperTypeAllowsDuplex(pt) {
			t.Errorf("%s should allow duplex", n)
		}
	}
}

func TestLookupTray(t *testing.T) {
	want := map[string]Tray{"auto": 0, "bypass": 1, "manual": 1, "tray1": 2, "TRAY2": 3}
	for n, c := range want {
		if got, err := LookupTray(n); err != nil || got != c {
			t.Errorf("%s = %d, %v", n, got, err)
		}
	}
	if _, err := LookupTray("tray9"); err == nil {
		t.Error("expected error")
	}
}

// near compares points to 4 decimals.
func near(a, b float64) bool { return math.Abs(a-b) < 1e-4 }

// Exact points give the Windows printable area (A4 4768 x 6816).
func TestPaperPrintableArea(t *testing.T) {
	cases := map[string][2]int{"A4": {4768, 6816}, "A5": {3296, 4761}, "Letter": {4928, 6400}}
	for name, want := range cases {
		p, _ := LookupPaper(name)
		if w, h := raster.PrintableArea(p.WidthPt, p.HeightPt, 600); w != want[0] || h != want[1] {
			t.Errorf("%s: %dx%d, want %dx%d", name, w, h, want[0], want[1])
		}
	}
}
