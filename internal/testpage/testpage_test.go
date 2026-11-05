package testpage

import (
	"testing"

	"github.com/kipavy/ricoh-spc240/internal/ddst"
	"github.com/kipavy/ricoh-spc240/internal/raster"
)

func TestPagesGeometry(t *testing.T) {
	a4p, _ := ddst.LookupPaper("A4")
	w, h := ddst.PageRaster(a4p)
	pp := Pages()
	if pp[0].Channels != 4 || pp[1].Channels != 1 {
		t.Fatalf("channels front=%d back=%d", pp[0].Channels, pp[1].Channels)
	}
	for i, p := range pp {
		if p.W != w || p.H != h || len(p.Pix) != w*h*p.Channels {
			t.Errorf("page %d is %dx%d (%d bytes)", i, p.W, p.H, len(p.Pix))
		}
	}
}

func TestFreshPages(t *testing.T) {
	a := Pages()
	a[0].Pix[0], a[1].Pix[0] = 99, 99
	b := Pages()
	if b[0].Pix[0] == 99 || b[1].Pix[0] == 99 {
		t.Error("Pages shares buffers between calls")
	}
}

func TestInkCoverageUnder3Percent(t *testing.T) {
	var sum, cap float64
	for _, p := range Pages() {
		for _, v := range p.Pix {
			sum += float64(v)
		}
		cap += 255 * float64(p.W*p.H*p.Channels)
	}
	if c := sum / cap; c >= 0.03 || c <= 0 {
		t.Errorf("ink coverage %.4f, want 0 < c < 0.03", c)
	}
}

func inkInRows(p *raster.Page, y0, y1 int) int {
	n := 0
	for _, v := range p.Pix[y0*p.W*p.Channels : y1*p.W*p.Channels] {
		if v != 0 {
			n++
		}
	}
	return n
}

func TestBottomTwoThirdsBlank(t *testing.T) {
	for i, p := range Pages() {
		if n := inkInRows(p, p.H/3, p.H); n != 0 {
			t.Errorf("page %d has %d inked samples in the bottom two thirds", i, n)
		}
	}
}

func TestLabelsAtTop(t *testing.T) {
	pp := Pages()
	for i, p := range pp {
		if n := inkInRows(p, 0, p.H*15/100); n == 0 {
			t.Errorf("page %d has no label ink in the top 15%% rows", i)
		}
	}
	for i, p := range pp {
		n := 0
		for y := 0; y < 45; y++ {
			for x := 0; x < p.W; x++ {
				if p.Pix[(y*p.W+x)*p.Channels+p.Channels-1] == 255 {
					n++
				}
			}
		}
		if n == 0 {
			t.Errorf("page %d: no K=255 label ink in rows 0-44", i)
		}
	}
	// Front label is black (K=255) in the top rows.
	f := pp[0]
	k := 0
	for y := 0; y < f.H*15/100; y++ {
		for x := 0; x < f.W; x++ {
			if f.Pix[(y*f.W+x)*4+3] == 255 {
				k++
			}
		}
	}
	if k == 0 {
		t.Error("front page has no K=255 label pixels")
	}
}

func TestFrontHasAllPrimaries(t *testing.T) {
	f := Pages()[0]
	var seen [4]bool
	for i := 0; i < len(f.Pix); i += 4 {
		for c := 0; c < 4; c++ {
			if f.Pix[i+c] == 255 {
				seen[c] = true
			}
		}
	}
	if seen != [4]bool{true, true, true, true} {
		t.Errorf("channels with full ink: %v", seen)
	}
}
