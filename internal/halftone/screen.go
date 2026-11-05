// Package halftone applies Ricoh's precomputed screen tables to 8-bit planes.
package halftone

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Quality selects the output depth (gradation) of the screens.
type Quality int

const (
	Fast     Quality = 1 // 1 bit per pixel
	Standard Quality = 2 // 2 bits per pixel
	Fine     Quality = 4 // 4 bits per pixel
)

// BPP returns the bits per pixel of the quality.
func (q Quality) BPP() int { return int(q) }

// ParseQuality parses "fast", "standard" or "fine" (case-insensitive).
func ParseQuality(s string) (Quality, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "fast":
		return Fast, nil
	case "standard":
		return Standard, nil
	case "fine":
		return Fine, nil
	}
	return 0, fmt.Errorf("halftone: unknown quality %q (want fast, standard or fine)", s)
}

// Screen is one colorant's screen table, unpacked to one byte per entry.
type Screen struct {
	W, H, Depth int
	table       []byte // table[(y%H)*W*256 + (x%W)*256 + level]
}

// ParseScreen parses a Ricoh screen file (docs/protocol.md section 9).
func ParseScreen(data []byte) (*Screen, error) {
	lim := len(data)
	if lim > 1024 {
		lim = 1024
	}
	nul := bytes.IndexByte(data[:lim], 0)
	if nul < 0 {
		return nil, fmt.Errorf("halftone: no header terminator")
	}
	f := strings.Fields(string(data[:nul]))
	if len(f) != 5 {
		return nil, fmt.Errorf("halftone: bad header %q", data[:nul])
	}
	var n [5]int
	for i, s := range f {
		v, err := strconv.Atoi(s)
		if err != nil {
			return nil, fmt.Errorf("halftone: bad header field %q", s)
		}
		n[i] = v
	}
	ver, w, h, in, depth := n[0], n[1], n[2], n[3], n[4]
	if ver != 1 {
		return nil, fmt.Errorf("halftone: unsupported version %d", ver)
	}
	if in != 8 {
		return nil, fmt.Errorf("halftone: unsupported input depth %d", in)
	}
	if depth != 1 && depth != 2 && depth != 4 {
		return nil, fmt.Errorf("halftone: unsupported output depth %d", depth)
	}
	if w <= 0 || h <= 0 || w > 4096 || h > 4096 {
		return nil, fmt.Errorf("halftone: bad size %dx%d", w, h)
	}
	entries := w * h * 256
	packed := data[nul+1:]
	if want := (entries*depth + 7) / 8; len(packed) != want {
		return nil, fmt.Errorf("halftone: data is %d bytes, want %d", len(packed), want)
	}
	tbl := make([]byte, entries)
	mask := byte(1<<depth - 1)
	per := 8 / depth
	for i := range tbl {
		shift := uint(8 - depth*(i%per+1))
		tbl[i] = packed[i/per] >> shift & mask
	}
	return &Screen{W: w, H: h, Depth: depth, table: tbl}, nil
}

// Row halftones one plane row (0 = no ink) at page row y and writes
// MSB-first Depth-bit pixels into out. Exactly (len(in)*Depth+7)/8 bytes of
// out are written; trailing pad bits are zero.
func (s *Screen) Row(in []byte, y int, out []byte) {
	n := (len(in)*s.Depth + 7) / 8
	out = out[:n]
	clear(out)
	rowBase := (y % s.H) * s.W * 256
	cell := s.table[rowBase : rowBase+s.W*256]
	d := uint(s.Depth)
	x, off := 0, 0
	var bit uint
	for _, v := range in {
		val := cell[off+int(v)]
		out[bit>>3] |= val << (8 - d - bit&7)
		bit += d
		off += 256
		if x++; x == s.W {
			x, off = 0, 0
		}
	}
}

// LoadScreenSet loads the C, M, Y, K screens from dir. Mono uses the K file
// for every slot.
func LoadScreenSet(dir string, quality Quality, color bool) ([4]*Screen, error) {
	var set [4]*Screen
	if quality != Fast && quality != Standard && quality != Fine {
		return set, fmt.Errorf("halftone: invalid quality %d", quality)
	}
	load := func(c string) (*Screen, error) {
		name := fmt.Sprintf("RPSC4S%s%d_Mac.bin", c, quality.BPP())
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return nil, err
		}
		s, err := ParseScreen(data)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		return s, nil
	}
	if !color {
		k, err := load("K")
		if err != nil {
			return set, err
		}
		return [4]*Screen{k, k, k, k}, nil
	}
	for i, c := range []string{"C", "M", "Y", "K"} {
		s, err := load(c)
		if err != nil {
			return [4]*Screen{}, err
		}
		set[i] = s
	}
	return set, nil
}
