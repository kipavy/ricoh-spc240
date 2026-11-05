package render

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/kipavy/ricoh-spc240/internal/ddst"
	"github.com/kipavy/ricoh-spc240/internal/raster"
)

var a4 = ddst.PaperSize{Name: "A4", WidthPt: 595.276, HeightPt: 841.89}

func req() Request {
	return Request{Input: "/in/doc", Paper: a4, Color: true, ICC: "/icc/ricoh.icc", TmpDir: "/tmp/w"}
}

func TestPageCommandColor(t *testing.T) {
	argv, out := PageCommand(req(), "/tmp/w/input.pdf", 3)
	want := []string{"gs", "-q", "-dSAFER", "-dBATCH", "-dNOPAUSE", "-r600",
		"-dDEVICEWIDTHPOINTS=595.276", "-dDEVICEHEIGHTPOINTS=841.89", "-dFIXEDMEDIA",
		"-dPDFFitPage", "-dFitPage", "-dAutoRotatePages=/PageByPage",
		"-dFirstPage=3", "-dLastPage=3", "-sDEVICE=pamcmyk32",
		"-sOutputICCProfile=/icc/ricoh.icc", "-o", "/tmp/w/p0003.pam", "/tmp/w/input.pdf"}
	if !reflect.DeepEqual(argv, want) {
		t.Fatalf("argv\n got %q\nwant %q", argv, want)
	}
	if out != "/tmp/w/p0003.pam" {
		t.Fatalf("out %q", out)
	}
}

func TestPageCommandMonoUsesPGM(t *testing.T) {
	r := req()
	r.Color = false
	argv, out := PageCommand(r, "/tmp/w/input.pdf", 12)
	j := strings.Join(argv, " ")
	if !strings.Contains(j, "-sDEVICE=pgmraw") || strings.Contains(j, "ICC") || strings.Contains(j, "pamcmyk32") {
		t.Fatalf("argv %q", argv)
	}
	if out != "/tmp/w/p0012.pgm" || argv[len(argv)-3] != "-o" || argv[len(argv)-2] != out {
		t.Fatalf("out %q argv %q", out, argv)
	}
}

func TestGhostscriptArgsFitLandscape(t *testing.T) {
	argv, _ := PageCommand(req(), "x.pdf", 1)
	for _, w := range []string{"-dPDFFitPage", "-dFitPage", "-dAutoRotatePages=/PageByPage", "-dFIXEDMEDIA"} {
		found := false
		for _, a := range argv {
			found = found || a == w
		}
		if !found {
			t.Errorf("missing %s in %q", w, argv)
		}
	}
}

func TestPageCountCommand(t *testing.T) {
	want := []string{"gs", "-q", "-dNODISPLAY", "-dSAFER", "--permit-file-read=/a/b (1).pdf",
		"-c", `(/a/b \(1\).pdf) (r) file runpdfbegin pdfpagecount = quit`}
	if got := PageCountCommand("/a/b (1).pdf"); !reflect.DeepEqual(got, want) {
		t.Fatalf("got %q", got)
	}
}

func TestNormalizeCommands(t *testing.T) {
	r := req()
	if got := NormalizeCommands(r, KindPDF); len(got) != 0 {
		t.Fatalf("pdf: %q", got)
	}
	ps := []string{"gs", "-q", "-dSAFER", "-dBATCH", "-dNOPAUSE", "-sDEVICE=pdfwrite", "-o", "/tmp/w/input.pdf", "/in/doc"}
	if got := NormalizeCommands(r, KindPS); !reflect.DeepEqual(got, [][]string{ps}) {
		t.Fatalf("ps: %q", got)
	}
	img := []string{"img2pdf", "--pagesize", "595.276ptx841.89pt", "--fit", "into", "-o", "/tmp/w/input.pdf", "/in/doc"}
	if got := NormalizeCommands(r, KindImage); !reflect.DeepEqual(got, [][]string{img}) {
		t.Fatalf("image: %q", got)
	}
	txt := NormalizeCommands(r, KindText)
	wantTxt := [][]string{
		{"enscript", "-B", "-q", "--encoding=latin1", "--media=A4", "-o", "/tmp/w/input.ps", "/tmp/w/input.txt"},
		{"gs", "-q", "-dSAFER", "-dBATCH", "-dNOPAUSE", "-sDEVICE=pdfwrite", "-o", "/tmp/w/input.pdf", "/tmp/w/input.ps"},
	}
	if !reflect.DeepEqual(txt, wantTxt) {
		t.Fatalf("text: %q", txt)
	}
	r.Paper.Name = ""
	if NormalizeCommands(r, KindText)[0][4] != "--media=A4" {
		t.Fatal("default media")
	}
}

func TestParsePages(t *testing.T) {
	ok := []struct {
		spec  string
		count int
		want  []int
	}{
		{"", 3, []int{1, 2, 3}},
		{"  ", 2, []int{1, 2}},
		{"1-3,5", 5, []int{1, 2, 3, 5}},
		{" 2 , 4 - 5 ", 5, []int{2, 4, 5}},
		{"4-", 6, []int{4, 5, 6}},
		{"2-2", 3, []int{2}},
	}
	for _, c := range ok {
		got, err := ParsePages(c.spec, c.count)
		if err != nil || !reflect.DeepEqual(got, c.want) {
			t.Errorf("%q: got %v, %v want %v", c.spec, got, err, c.want)
		}
	}
	for _, c := range []struct {
		spec  string
		count int
	}{{"0", 3}, {"3-1", 5}, {"4", 3}, {"1-9", 3}, {"a", 3}, {"1,,2", 3}, {"-3", 5}, {"5-", 3}} {
		if got, err := ParsePages(c.spec, c.count); err == nil {
			t.Errorf("%q/%d: want error, got %v", c.spec, c.count, got)
		}
	}
}

func TestDetect(t *testing.T) {
	dir := t.TempDir()
	cases := []struct {
		name string
		data []byte
		want Kind
		fail bool
	}{
		{"a.pdf", []byte("%PDF-1.4\n..."), KindPDF, false},
		{"b.bin", []byte("%!PS-Adobe-3.0\n"), KindPS, false},
		{"c.bin", []byte("\x89PNG\r\n\x1a\n...."), KindImage, false},
		{"d.bin", []byte{0xFF, 0xD8, 0xFF, 0xE0, 0, 0}, KindImage, false},
		{"e.bin", []byte("GIF89a...."), KindImage, false},
		{"f.bin", []byte("II*\x00...."), KindImage, false},
		{"g.txt", []byte("héllo wörld\nline 2\n"), KindText, false},
		{"h", []byte("plain text no extension"), KindText, false},
		{"i.bin", []byte{0, 1, 2, 0xFE, 0xFF, 0, 0, 7}, 0, true},
		{"j.ps", []byte("garbage"), KindPS, false},
		{"k.jpg", []byte("garbage"), KindImage, false},
	}
	for _, c := range cases {
		p := filepath.Join(dir, c.name)
		if err := os.WriteFile(p, c.data, 0o644); err != nil {
			t.Fatal(err)
		}
		got, err := Detect(p)
		if c.fail {
			if err == nil || !strings.Contains(err.Error(), "unsupported input") {
				t.Errorf("%s: want unsupported input, got %v, %v", c.name, got, err)
			}
			continue
		}
		if err != nil || got != c.want {
			t.Errorf("%s: got %v, %v want %v", c.name, got, err, c.want)
		}
	}
	if _, err := Detect(filepath.Join(dir, "missing")); err == nil {
		t.Error("missing file: want error")
	}
}

func TestRenderMissingTool(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	dir := t.TempDir()
	in := filepath.Join(dir, "x.pdf")
	os.WriteFile(in, []byte("%PDF-1.4"), 0o644)
	r := req()
	r.Input, r.TmpDir = in, dir
	pf, errc := Render(context.Background(), r)
	for range pf {
	}
	err := <-errc
	if err == nil || !strings.Contains(err.Error(), "gs") {
		t.Fatalf("err = %v", err)
	}
}

func TestRenderIntegration(t *testing.T) {
	if _, err := exec.LookPath("gs"); err != nil {
		t.Skip("gs not on PATH")
	}
	dir := t.TempDir()
	psPath := filepath.Join(dir, "in.ps")
	ps := "%!PS\n/Helvetica findfont 40 scalefont setfont\n" +
		"100 700 moveto (one) show showpage\n100 700 moveto (two) show showpage\n"
	os.WriteFile(psPath, []byte(ps), 0o644)
	pdf := filepath.Join(dir, "in.pdf")
	if out, err := exec.Command("gs", "-q", "-dSAFER", "-dBATCH", "-dNOPAUSE", "-sDEVICE=pdfwrite", "-o", pdf, psPath).CombinedOutput(); err != nil {
		t.Fatalf("make pdf: %v\n%s", err, out)
	}
	icc := os.Getenv("RICOH_ICC") // optional; gs default CMYK profile otherwise
	if icc == "" {
		icc = findDefaultICC()
	}
	tmp := t.TempDir()
	r := Request{Input: pdf, Paper: a4, Color: icc != "", ICC: icc, TmpDir: tmp}
	pf, errc := Render(context.Background(), r)
	n := 0
	for f := range pf {
		n++
		if f.Index != n-1 || f.Page != n {
			t.Errorf("file %d: %+v", n, f)
		}
		fh, err := os.Open(f.Path)
		if err != nil {
			t.Fatal(err)
		}
		pg, err := raster.ReadPAM(fh)
		fh.Close()
		if err != nil {
			t.Fatal(err)
		}
		if pg.W != 4961 || pg.H != 7016 {
			t.Errorf("page %d is %dx%d", n, pg.W, pg.H)
		}
		os.Remove(f.Path)
	}
	if err := <-errc; err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("got %d pages", n)
	}
}

func findDefaultICC() string {
	for _, p := range []string{"/usr/share/color/icc/ghostscript/default_cmyk.icc", "/usr/share/ghostscript/iccprofiles/default_cmyk.icc"} {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

func TestToLatin1(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"Été", "\xC9t\xE9"},
		{"œuf", "oeuf"},
		{"Œuvre", "OEuvre"},
		{"l’été", "l'\xE9t\xE9"},
		{"“a” – b — c…", "\"a\" - b - c..."},
		{"€5", "EUR5"},
		{"a b", "a b"},
		{"hi 😀!", "hi ?!"},
		{"\xEF\xBB\xBFbom", "bom"},
	} {
		if got := string(toLatin1([]byte(c.in))); got != c.want {
			t.Errorf("%q: got %q want %q", c.in, got, c.want)
		}
	}
}

func TestEnscriptMedia(t *testing.T) {
	for in, want := range map[string]string{
		"A4": "A4", "a3": "A3", "A5": "A5", "Letter": "Letter", "LEGAL": "Legal",
		"Executive": "Executive", "B5": "A4", "": "A4", "A6": "A4",
	} {
		if got := enscriptMedia(in); got != want {
			t.Errorf("%q: got %q want %q", in, got, want)
		}
	}
}

func TestDetectEmpty(t *testing.T) {
	p := filepath.Join(t.TempDir(), "e.txt")
	os.WriteFile(p, nil, 0o644)
	if _, err := Detect(p); err == nil || !strings.Contains(err.Error(), "empty input file") {
		t.Fatalf("err = %v", err)
	}
}
