package raster

import (
	"bytes"
	"strings"
	"testing"
)

func TestReadPAMCMYK(t *testing.T) {
	hdr := "P7\n# c\nHEIGHT 1\nWIDTH 2\nDEPTH 4\nMAXVAL 255\nTUPLTYPE CMYK\nENDHDR\n"
	data := []byte{1, 2, 3, 4, 5, 6, 7, 8}
	p, err := ReadPAM(bytes.NewReader(append([]byte(hdr), data...)))
	if err != nil {
		t.Fatal(err)
	}
	if p.W != 2 || p.H != 1 || p.Channels != 4 || !bytes.Equal(p.Pix, data) {
		t.Fatalf("got %+v", p)
	}
}

func TestReadPAMErrors(t *testing.T) {
	cases := map[string]string{
		"maxval":   "P7\nWIDTH 1\nHEIGHT 1\nDEPTH 4\nMAXVAL 65535\nTUPLTYPE CMYK\nENDHDR\n12345678",
		"depth":    "P7\nWIDTH 1\nHEIGHT 1\nDEPTH 3\nMAXVAL 255\nTUPLTYPE RGB\nENDHDR\n123",
		"magic":    "P6\n1 1\n255\n123",
		"short":    "P7\nWIDTH 2\nHEIGHT 1\nDEPTH 4\nMAXVAL 255\nTUPLTYPE CMYK\nENDHDR\n1234",
		"pgmshort": "P5\n2 2\n255\n123",
		"pgmmax":   "P5\n1 1\n65535\n12",
	}
	for name, in := range cases {
		if _, err := ReadPAM(strings.NewReader(in)); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}

func TestReadPGMInvertsToK(t *testing.T) {
	in := "P5\n# x\n2\n1\n255\n" + string([]byte{0, 200})
	p, err := ReadPAM(strings.NewReader(in))
	if err != nil {
		t.Fatal(err)
	}
	if p.Channels != 1 || p.W != 2 || p.H != 1 || !bytes.Equal(p.Pix, []byte{255, 55}) {
		t.Fatalf("got %+v", p)
	}
}

func TestPrintableAreaA4(t *testing.T) {
	cases := []struct {
		wp, hp float64
		w, h   int
	}{
		{595.2756, 841.8898, 4768, 6816},
		{612, 792, 4928, 6400},
		{419.5276, 595.2756, 3296, 4761},
		{595, 842, 4768, 6817}, // ddst-style rounded points: height rounds to 7017 px
	}
	for _, c := range cases {
		w, h := PrintableArea(c.wp, c.hp, 600)
		if w != c.w || h != c.h {
			t.Errorf("%v x %v: got %dx%d want %dx%d", c.wp, c.hp, w, h, c.w, c.h)
		}
	}
}

func TestCropCentres(t *testing.T) {
	p := &Page{W: 4, H: 3, Channels: 1, Pix: []byte{
		1, 2, 3, 4,
		5, 6, 7, 8,
		9, 10, 11, 12}}
	c := p.Crop(2, 1)
	if c.W != 2 || c.H != 1 || !bytes.Equal(c.Pix, []byte{6, 7}) {
		t.Fatalf("crop: %+v", c)
	}
	c = p.Crop(3, 3) // drop floor(1/2)=0 left
	if !bytes.Equal(c.Pix, []byte{1, 2, 3, 5, 6, 7, 9, 10, 11}) {
		t.Fatalf("crop3: %+v", c)
	}
	s := &Page{W: 2, H: 1, Channels: 1, Pix: []byte{7, 8}}
	c = s.Crop(4, 3) // pad: x offset 1, y offset 1
	want := []byte{0, 0, 0, 0, 0, 7, 8, 0, 0, 0, 0, 0}
	if c.W != 4 || c.H != 3 || !bytes.Equal(c.Pix, want) {
		t.Fatalf("pad: %+v", c)
	}
	q := &Page{W: 1, H: 1, Channels: 4, Pix: []byte{1, 2, 3, 4}}
	c = q.Crop(3, 1)
	if !bytes.Equal(c.Pix, []byte{0, 0, 0, 0, 1, 2, 3, 4, 0, 0, 0, 0}) {
		t.Fatalf("cmyk pad: %+v", c)
	}
}

func TestGrayToK(t *testing.T) {
	p := &Page{W: 3, H: 1, Channels: 4, Pix: []byte{50, 50, 50, 10, 50, 50, 49, 0, 200, 200, 200, 100}}
	p.GrayToK()
	want := []byte{0, 0, 0, 60, 50, 50, 49, 0, 0, 0, 0, 255}
	if !bytes.Equal(p.Pix, want) {
		t.Fatalf("got %v", p.Pix)
	}
	g := &Page{W: 1, H: 1, Channels: 1, Pix: []byte{9}}
	g.GrayToK()
	if g.Pix[0] != 9 {
		t.Fatal("1ch changed")
	}
}

func TestRotate180(t *testing.T) {
	p := &Page{W: 2, H: 2, Channels: 4, Pix: []byte{
		1, 2, 3, 4, 5, 6, 7, 8,
		9, 10, 11, 12, 13, 14, 15, 16}}
	p.Rotate180()
	want := []byte{13, 14, 15, 16, 9, 10, 11, 12, 5, 6, 7, 8, 1, 2, 3, 4}
	if !bytes.Equal(p.Pix, want) {
		t.Fatalf("got %v", p.Pix)
	}
}

func TestBlank(t *testing.T) {
	p := &Page{W: 2, H: 1, Channels: 1, Pix: []byte{0, 0}}
	if !p.Blank() {
		t.Fatal("should be blank")
	}
	p.Pix[1] = 1
	if p.Blank() {
		t.Fatal("should not be blank")
	}
}
