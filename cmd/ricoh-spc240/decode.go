package main

import (
	"encoding/binary"
	"errors"
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"os"
	"path/filepath"

	"github.com/kipavy/ricoh-spc240/internal/ddst"
	"github.com/kipavy/ricoh-spc240/internal/jbig"
)

func cmdDecode(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("decode", flag.ContinueOnError)
	fs.SetOutput(stderr)
	pngDir := fs.String("png", "", "write page-NN.png previews into this directory")
	pos, err := parse(fs, args)
	if errors.Is(err, flag.ErrHelp) {
		return 0
	}
	if err != nil {
		return exitCode(usageError{err}, stderr)
	}
	if len(pos) != 1 {
		return exitCode(usagef("decode needs exactly one FILE"), stderr)
	}
	return exitCode(runDecode(pos[0], *pngDir, stdout), stderr)
}

func runDecode(path, pngDir string, stdout io.Writer) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	recs, perr := ddst.Parse(f)
	printRecords(stdout, recs)
	if perr != nil {
		return perr
	}
	if pngDir != "" {
		return writePNGs(stdout, recs, pngDir)
	}
	return nil
}

func be16(b []byte) int { return int(binary.BigEndian.Uint16(b)) }

func jobBPP(h []byte) int {
	if h[33] == 0 {
		return 1
	}
	return int(h[33]) + 1
}

// printRecords prints one line per record; GDIB records are summarised per page.
func printRecords(w io.Writer, recs []ddst.Record) {
	bands, total := 0, 0
	flush := func() {
		if bands > 0 {
			fmt.Fprintf(w, "  GDIB x%d, %d bytes\n", bands, total)
			bands, total = 0, 0
		}
	}
	for _, r := range recs {
		h := r.Header
		if r.Kind != "GDIB" {
			flush()
		}
		switch r.Kind {
		case "GDIJ":
			title := h[56:120]
			for i, c := range title {
				if c == 0 {
					title = title[:i]
					break
				}
			}
			fmt.Fprintf(w, "GDIJ @%d copies=%d flags=%#04x color=%t bpp=%d title=%q\n",
				r.Offset, be16(h[10:]), be16(h[16:]), h[24] != 0, jobBPP(h), title)
		case "GDIP":
			fmt.Fprintf(w, "GDIP @%d paper=%#02x tray=%d type=%#02x size=%dx%d planes=%d side=%#02x pageno=%d bands=%d\n",
				r.Offset, h[8], h[9], h[10], be16(h[12:]), be16(h[14:]), h[32], h[33], be16(h[34:]), be16(h[54:]))
		case "GDIB":
			bands++
			for _, p := range r.Planes {
				total += len(p)
			}
		default:
			fmt.Fprintf(w, "%s @%d\n", r.Kind, r.Offset)
		}
	}
	flush()
}

// writePNGs writes a preview per page. Planes are composited as ink/no ink
// (any non-zero pixel value counts as ink); this is not colour accurate.
func writePNGs(stdout io.Writer, recs []ddst.Record, dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	bpp := 1
	var pageHdr []byte
	var bands []ddst.Record
	n := 0
	emit := func() error {
		if pageHdr == nil {
			return nil
		}
		n++
		name := filepath.Join(dir, fmt.Sprintf("page-%02d.png", n))
		if err := writePagePNG(name, pageHdr, bands, bpp); err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
		fmt.Fprintf(stdout, "wrote %s\n", name)
		return nil
	}
	for _, r := range recs {
		switch r.Kind {
		case "GDIJ":
			bpp = jobBPP(r.Header)
		case "GDIP", "JIDG", "GDIC":
			if err := emit(); err != nil {
				return err
			}
			pageHdr, bands = nil, nil
			if r.Kind == "GDIP" {
				pageHdr = r.Header
			}
		case "GDIB":
			bands = append(bands, r)
		}
	}
	return emit()
}

func writePagePNG(name string, hdr []byte, bands []ddst.Record, bpp int) error {
	width, height := be16(hdr[12:])/bpp, be16(hdr[14:])
	// ink[plane][y*width+x], plane order K, Y, M, C as in the stream
	var ink [4][]bool
	y0 := 0
	for _, b := range bands {
		lines := be16(b.Header[26:])
		for k, data := range b.Planes {
			if len(data) == 0 {
				continue
			}
			w, h, rows, err := jbig.Decode(data)
			if err != nil {
				return fmt.Errorf("plane %c: %w", "KYMC"[k], err)
			}
			if h != lines || w != width*bpp || y0+lines > height {
				return fmt.Errorf("plane %c is %dx%d, want %dx%d", "KYMC"[k], w, h, width*bpp, lines)
			}
			if ink[k] == nil {
				ink[k] = make([]bool, width*height)
			}
			stride := (w + 7) / 8
			for y := 0; y < h; y++ {
				for x := 0; x < width; x++ {
					for bit := x * bpp; bit < (x+1)*bpp; bit++ {
						if rows[y*stride+bit/8]>>(7-uint(bit%8))&1 != 0 {
							ink[k][(y0+y)*width+x] = true
							break
						}
					}
				}
			}
		}
		y0 += lines
	}
	at := func(k, i int) float64 {
		if ink[k] != nil && ink[k][i] {
			return 1
		}
		return 0
	}
	chan8 := func(c, k float64) uint8 { return uint8(255*(1-c)*(1-k) + 0.5) }
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for i := 0; i < width*height; i++ {
		k := at(0, i)
		img.SetRGBA(i%width, i/width, color.RGBA{
			chan8(at(3, i), k), chan8(at(2, i), k), chan8(at(1, i), k), 255})
	}
	out, err := os.Create(name)
	if err != nil {
		return err
	}
	if err := png.Encode(out, img); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
