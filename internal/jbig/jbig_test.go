package jbig

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// bitmap builds a w×h bi-level image (1 = ink, MSB first, zero padding bits).
func bitmap(w, h int, ink func(x, y int) bool) []byte {
	stride := (w + 7) / 8
	rows := make([]byte, stride*h)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			if ink(x, y) {
				rows[y*stride+x/8] |= 0x80 >> (x % 8)
			}
		}
	}
	return rows
}

type golden struct {
	name string
	w, h int
	ink  func(x, y int) bool
	// Either a stored file (testdata/<name>.jbg) or size + sha256 hex prefix.
	stored bool
	size   int
	sha    string
}

var goldens = []golden{
	{name: "blank_64x1", w: 64, h: 1, ink: func(x, y int) bool { return false }, stored: true},
	{name: "checker_4768x256", w: 4768, h: 256, ink: func(x, y int) bool { return ((x/4)+(y/4))%2 == 0 }, stored: true},
	{name: "dot_9x3", w: 9, h: 3, ink: func(x, y int) bool { return x == 4 && y == 1 }, stored: true},
	{name: "bars_4768x600", w: 4768, h: 600, ink: func(x, y int) bool { return (x/37)%3 == 0 && (y/50)%2 == 1 }, size: 772, sha: "fb8039065cfe1a15"},
	{name: "diag_1000x300", w: 1000, h: 300, ink: func(x, y int) bool { return (x*7+y*13)%29 < 5 }, size: 9774, sha: "3e2ccd8d9a3af46f"},
}

func TestEncodeMatchesJbigkit(t *testing.T) {
	for _, g := range goldens {
		t.Run(g.name, func(t *testing.T) {
			got := Encode(g.w, g.h, bitmap(g.w, g.h, g.ink))
			if g.stored {
				want, err := os.ReadFile(filepath.Join("testdata", g.name+".jbg"))
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(got, want) {
					t.Fatalf("mismatch\n got % x\nwant % x", head(got), head(want))
				}
				return
			}
			sum := sha256.Sum256(got)
			if len(got) != g.size || !strings.HasPrefix(hex.EncodeToString(sum[:]), g.sha) {
				t.Fatalf("got %d bytes sha256 %x, want %d bytes sha256 %s…", len(got), sum[:8], g.size, g.sha)
			}
		})
	}
}

func head(b []byte) []byte {
	if len(b) > 64 {
		return b[:64]
	}
	return b
}

// randomBitmaps returns n seeded random images of random size and texture.
func randomBitmaps(n int) []golden {
	r := rand.New(rand.NewSource(20261105))
	out := make([]golden, n)
	for i := range out {
		w, h := 1+r.Intn(5000), 1+r.Intn(600)
		stride := (w + 7) / 8
		rows := make([]byte, stride*h)
		switch i % 3 {
		case 0: // uniform noise
			r.Read(rows)
		case 1: // sparse noise
			for k := 0; k < w*h/50; k++ {
				x, y := r.Intn(w), r.Intn(h)
				rows[y*stride+x/8] |= 0x80 >> (x % 8)
			}
		case 2: // random rectangles (text-like structure)
			for k := 0; k < 40; k++ {
				x0, y0 := r.Intn(w), r.Intn(h)
				x1, y1 := x0+r.Intn(200), y0+r.Intn(60)
				for y := y0; y < y1 && y < h; y++ {
					for x := x0; x < x1 && x < w; x++ {
						rows[y*stride+x/8] |= 0x80 >> (x % 8)
					}
				}
			}
		}
		// Clear padding bits so round-trip comparison is exact.
		if w%8 != 0 {
			mask := byte(0xff << (8 - w%8))
			for y := 0; y < h; y++ {
				rows[y*stride+stride-1] &= mask
			}
		}
		out[i] = golden{name: fmt.Sprintf("rand%02d_%dx%d", i, w, h), w: w, h: h}
		rowsCopy := rows
		out[i].ink = func(x, y int) bool { return rowsCopy[y*stride+x/8]&(0x80>>(x%8)) != 0 }
	}
	return out
}

var tinyCases = []golden{
	{name: "1x1ink", w: 1, h: 1, ink: func(x, y int) bool { return true }},
	{name: "2x5", w: 2, h: 5, ink: func(x, y int) bool { return (x+y)%2 == 0 }},
	{name: "3x300", w: 3, h: 300, ink: func(x, y int) bool { return y%3 == x }},
	{name: "8x257", w: 8, h: 257, ink: func(x, y int) bool { return (x*y)%5 == 1 }},
	{name: "13x512", w: 13, h: 512, ink: func(x, y int) bool { return (x+2*y)%7 < 3 }},
}

func TestDecodeRoundTrip(t *testing.T) {
	cases := append(append([]golden{}, goldens...), randomBitmaps(50)...)
	cases = append(cases, tinyCases...)
	for _, c := range cases {
		rows := bitmap(c.w, c.h, c.ink)
		w, h, got, err := Decode(Encode(c.w, c.h, rows))
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if w != c.w || h != c.h || !bytes.Equal(got, rows) {
			t.Fatalf("%s: round trip mismatch (got %dx%d)", c.name, w, h)
		}
	}
}

func TestDecodeGoldenFiles(t *testing.T) {
	for _, g := range goldens[:3] {
		data, err := os.ReadFile(filepath.Join("testdata", g.name+".jbg"))
		if err != nil {
			t.Fatal(err)
		}
		w, h, got, err := Decode(data)
		if err != nil {
			t.Fatalf("%s: %v", g.name, err)
		}
		if w != g.w || h != g.h || !bytes.Equal(got, bitmap(g.w, g.h, g.ink)) {
			t.Fatalf("%s: decoded image differs", g.name)
		}
	}
}

func TestDecodeToleratesZeroPadding(t *testing.T) {
	g := goldens[4]
	rows := bitmap(g.w, g.h, g.ink)
	enc := append(Encode(g.w, g.h, rows), 0, 0, 0, 0, 0)
	_, _, got, err := Decode(enc)
	if err != nil || !bytes.Equal(got, rows) {
		t.Fatalf("padded stream: err=%v equal=%v", err, bytes.Equal(got, rows))
	}
}

func TestDecodeRejectsUnsupported(t *testing.T) {
	valid := Encode(9, 3, bitmap(9, 3, goldens[2].ink))
	cases := []struct {
		name string
		mod  func(b []byte) []byte
		want string
	}{
		{"TPBON", func(b []byte) []byte { b[19] = 0x48; return b }, "options"},
		{"TPDON", func(b []byte) []byte { b[19] = 0x50; return b }, "options"},
		{"VLENGTH", func(b []byte) []byte { b[19] = 0x60; return b }, "options"},
		{"DPON", func(b []byte) []byte { b[19] = 0x44; return b }, "options"},
		{"D=1", func(b []byte) []byte { b[1] = 1; return b }, "layers"},
		{"P=3", func(b []byte) []byte { b[2] = 3; return b }, "planes"},
		{"MX=8", func(b []byte) []byte { b[16] = 8; return b }, "MX"},
		{"short", func(b []byte) []byte { return b[:10] }, "short"},
		{"XD=0", func(b []byte) []byte { b[4], b[5], b[6], b[7] = 0, 0, 0, 0; return b }, "size"},
		{"NEWLEN", func(b []byte) []byte {
			b = b[:len(b)-2]
			return append(b, 0xff, 0x05, 0, 0, 0, 3, 0xff, 0x02)
		}, "NEWLEN"},
		{"truncated", func(b []byte) []byte { return b[:len(b)-2] }, "stripe"},
	}
	for _, c := range cases {
		b := c.mod(append([]byte{}, valid...))
		_, _, _, err := Decode(b)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err = %v, want containing %q", c.name, err, c.want)
		}
	}
}

func TestJbigkitDecodesOurs(t *testing.T) {
	tool, err := exec.LookPath("jbgtopbm")
	if err != nil {
		t.Skip("jbgtopbm not on PATH")
	}
	dir := t.TempDir()
	for _, c := range randomBitmaps(50) {
		rows := bitmap(c.w, c.h, c.ink)
		in := filepath.Join(dir, c.name+".jbg")
		out := filepath.Join(dir, c.name+".pbm")
		if err := os.WriteFile(in, Encode(c.w, c.h, rows), 0o644); err != nil {
			t.Fatal(err)
		}
		if msg, err := exec.Command(tool, in, out).CombinedOutput(); err != nil {
			t.Fatalf("%s: jbgtopbm: %v\n%s", c.name, err, msg)
		}
		w, h, got, err := readPBM(out)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if w != c.w || h != c.h || !bytes.Equal(got, rows) {
			t.Fatalf("%s: jbgtopbm output differs", c.name)
		}
	}
}

// readPBM reads a binary (P4) PBM file.
func readPBM(path string) (w, h int, rows []byte, err error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, 0, nil, err
	}
	br := bufio.NewReader(bytes.NewReader(data))
	var magic string
	if _, err := fmt.Fscan(br, &magic, &w, &h); err != nil || magic != "P4" {
		return 0, 0, nil, fmt.Errorf("bad PBM header (%v)", err)
	}
	if _, err := br.ReadByte(); err != nil { // single whitespace after header
		return 0, 0, nil, err
	}
	rows = make([]byte, (w+7)/8*h)
	if _, err := io.ReadFull(br, rows); err != nil {
		return 0, 0, nil, err
	}
	return w, h, rows, nil
}

func BenchmarkEncodeBand(b *testing.B) {
	rows := bitmap(4768, 256, goldens[4].ink)
	b.SetBytes(int64(len(rows)))
	for i := 0; i < b.N; i++ {
		Encode(4768, 256, rows)
	}
}

// The three-line template (options 0x00) is never produced by Encode; check
// that Decode handles it using the test-only encoder path.
func TestDecodeThreeLineTemplate(t *testing.T) {
	cases := append(append(append([]golden{}, goldens...), tinyCases...), randomBitmaps(10)...)
	for _, c := range cases {
		rows := bitmap(c.w, c.h, c.ink)
		enc := encodeImage(c.w, c.h, rows, 0x00)
		if enc[19] != 0x00 {
			t.Fatalf("%s: options byte 0x%02x", c.name, enc[19])
		}
		w, h, got, err := Decode(enc)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if w != c.w || h != c.h || !bytes.Equal(got, rows) {
			t.Fatalf("%s: three-line round trip mismatch", c.name)
		}
	}
}
