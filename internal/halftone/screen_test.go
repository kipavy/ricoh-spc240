package halftone

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// buildScreen packs fn(y,x,level) values into a screen file image.
func buildScreen(header string, w, h, depth int, fn func(y, x, v int) int) []byte {
	n := (w*h*256*depth + 7) / 8
	data := make([]byte, n)
	i := 0
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			for v := 0; v < 256; v++ {
				val := fn(y, x, v)
				bit := i * depth
				data[bit/8] |= byte(val << (8 - depth - bit%8))
				i++
			}
		}
	}
	return append(append([]byte(header), 0), data...)
}

func thresh1(y, x, v int) int {
	t := 64
	if x == 0 && y == 0 {
		t = 128
	}
	if v >= t {
		return 1
	}
	return 0
}

func screen1(t *testing.T) *Screen {
	t.Helper()
	s, err := ParseScreen(buildScreen("1 2 2 8 1", 2, 2, 1, thresh1))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestParseScreenHeader(t *testing.T) {
	s := screen1(t)
	if s.W != 2 || s.H != 2 || s.Depth != 1 {
		t.Fatalf("got %d %d %d", s.W, s.H, s.Depth)
	}
	good := buildScreen("1 2 2 8 1", 2, 2, 1, thresh1)
	cases := map[string][]byte{
		"short":   good[:len(good)-1],
		"long":    append(append([]byte{}, good...), 0),
		"version": buildScreen("2 2 2 8 1", 2, 2, 1, thresh1),
		"depth3":  buildScreen("1 2 2 8 3", 2, 2, 1, thresh1),
		"input4":  buildScreen("1 2 2 4 1", 2, 2, 1, thresh1),
		"nonul":   []byte("1 2 2 8 1"),
		"junk":    append([]byte("hello"), 0),
		"zero":    buildScreen("1 0 2 8 1", 1, 1, 1, thresh1),
	}
	for name, d := range cases {
		if _, err := ParseScreen(d); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}

func TestRowThreshold(t *testing.T) {
	out := []byte{0xFF}
	screen1(t).Row([]byte{0, 100, 200, 255}, 0, out)
	// x=0: 0 ->0 ; x=1: 100>=64 ->1 ; x=2 (cell 0,0): 200>=128 ->1 ; x=3: 255 ->1
	if out[0] != 0x70 {
		t.Fatalf("got %#02x want 0x70", out[0])
	}
}

func TestRowDepth2(t *testing.T) {
	s, err := ParseScreen(buildScreen("1 1 1 8 2", 1, 1, 2, func(y, x, v int) int { return v / 64 }))
	if err != nil {
		t.Fatal(err)
	}
	out := make([]byte, 3)
	out[2] = 0xAA
	s.Row([]byte{0, 64, 128, 255, 70}, 0, out)
	// 0,1,2,3 -> 00 01 10 11 = 0x1B ; 70 -> 01 then zero padding
	if !bytes.Equal(out, []byte{0x1B, 0x40, 0xAA}) {
		t.Fatalf("got %x", out)
	}
}

func TestRowDepth4(t *testing.T) {
	s, err := ParseScreen(buildScreen("1 1 1 8 4", 1, 1, 4, func(y, x, v int) int { return v >> 4 }))
	if err != nil {
		t.Fatal(err)
	}
	out := make([]byte, 2)
	s.Row([]byte{0x10, 0xF0, 0x80}, 0, out)
	if !bytes.Equal(out, []byte{0x1F, 0x80}) {
		t.Fatalf("got %x", out)
	}
}

func TestRowWrapsCells(t *testing.T) {
	s := screen1(t)
	a, b := make([]byte, 1), make([]byte, 1)
	in := []byte{100, 100, 100, 100, 100, 100, 100, 100}
	s.Row(in, 0, a)
	s.Row(in, 2, b) // y=2 wraps to row 0
	if a[0] != b[0] {
		t.Fatalf("y wrap: %x vs %x", a[0], b[0])
	}
	// row 0: cell (0,0) needs >=128, so even x are 0 for v=100: 01010101
	if a[0] != 0x55 {
		t.Fatalf("got %#02x want 0x55", a[0])
	}
	s.Row(in, 1, b) // row 1 all threshold 64
	if b[0] != 0xFF {
		t.Fatalf("got %#02x want 0xFF", b[0])
	}
}

func TestRowNoAlloc(t *testing.T) {
	s := screen1(t)
	in, out := make([]byte, 4768), make([]byte, 596)
	if n := testing.AllocsPerRun(10, func() { s.Row(in, 3, out) }); n != 0 {
		t.Fatalf("allocs %v", n)
	}
}

func TestParseQuality(t *testing.T) {
	for in, want := range map[string]Quality{"fast": Fast, "Standard": Standard, "FINE": Fine} {
		q, err := ParseQuality(in)
		if err != nil || q != want {
			t.Errorf("%s: %v %v", in, q, err)
		}
	}
	if _, err := ParseQuality("best"); err == nil {
		t.Error("expected error")
	}
	if Fast.BPP() != 1 || Standard.BPP() != 2 || Fine.BPP() != 4 {
		t.Error("bpp")
	}
}

func writeFake(t *testing.T, dir, name string, w, depth int) {
	t.Helper()
	d := buildScreen("1 "+itoa(w)+" 1 8 "+itoa(depth), w, 1, depth, func(y, x, v int) int { return 0 })
	if err := os.WriteFile(filepath.Join(dir, name), d, 0o644); err != nil {
		t.Fatal(err)
	}
}

func itoa(n int) string { return strings.TrimSpace(strings.Repeat(" ", 0) + string(rune('0'+n))) }

func TestLoadScreenSetMonoUsesK(t *testing.T) {
	dir := t.TempDir()
	writeFake(t, dir, "RPSC4SK2_Mac.bin", 4, 2) // only K exists
	set, err := LoadScreenSet(dir, Standard, false)
	if err != nil {
		t.Fatal(err)
	}
	for i, s := range set {
		if s == nil || s.W != 4 || s.Depth != 2 {
			t.Fatalf("slot %d: %+v", i, s)
		}
	}
}

func TestLoadScreenSetColorOrder(t *testing.T) {
	dir := t.TempDir()
	for i, c := range "CMYK" {
		writeFake(t, dir, "RPSC4S"+string(c)+"1_Mac.bin", i+1, 1)
	}
	set, err := LoadScreenSet(dir, Fast, true)
	if err != nil {
		t.Fatal(err)
	}
	for i, s := range set {
		if s.W != i+1 {
			t.Fatalf("slot %d has W=%d", i, s.W)
		}
	}
	if _, err := LoadScreenSet(t.TempDir(), Fast, true); err == nil {
		t.Error("expected error for missing files")
	}
}
