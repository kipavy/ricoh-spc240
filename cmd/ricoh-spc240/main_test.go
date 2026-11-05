package main

import (
	"bytes"
	"fmt"
	"image/png"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A6 printable area at 600 dpi (keeps test pages light).
const a6W, a6H = 2304, 3296

// writeScreens builds synthetic 1x1 screens for all 12 names: level 0 gives
// no ink, any other level gives full ink.
func writeScreens(t testing.TB) string {
	t.Helper()
	assets := t.TempDir()
	dir := filepath.Join(assets, "screens")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, depth := range []int{1, 2, 4} {
		n := 256 * depth / 8
		data := []byte(fmt.Sprintf("1 1 1 8 %d\x00", depth))
		body := bytes.Repeat([]byte{0xFF}, n)
		body[0] = 0xFF >> uint(depth) // first entry (level 0) is zero
		data = append(data, body...)
		for _, c := range "CMYK" {
			name := fmt.Sprintf("RPSC4S%c%d_Mac.bin", c, depth)
			if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	return assets
}

// writePGM writes an A6-printable-sized gray page with a black square.
func writePGM(t testing.TB, path string, square bool) {
	t.Helper()
	pix := bytes.Repeat([]byte{255}, a6W*a6H)
	if square {
		for y := 400; y < 600; y++ {
			for x := 300; x < 500; x++ {
				pix[y*a6W+x] = 0
			}
		}
	}
	data := append([]byte(fmt.Sprintf("P5\n%d %d\n255\n", a6W, a6H)), pix...)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func runCmd(args ...string) (int, string, string) {
	var out, errb bytes.Buffer
	code := run(args, &out, &errb)
	return code, out.String(), errb.String()
}

// encodeFixture encodes two A6 pages into a .ddst file and returns its path.
func encodeFixture(t *testing.T, extra ...string) (assets, ddstPath string) {
	t.Helper()
	assets = writeScreens(t)
	tmp := t.TempDir()
	p1, p2 := filepath.Join(tmp, "p1.pgm"), filepath.Join(tmp, "p2.pgm")
	writePGM(t, p1, true)
	writePGM(t, p2, false)
	ddstPath = filepath.Join(tmp, "job.ddst")
	args := append([]string{"encode", p1, p2, "--mono", "--paper", "A6", "--assets", assets,
		"--quality", "fast", "--output", ddstPath}, extra...)
	if code, out, errs := runCmd(args...); code != 0 {
		t.Fatalf("encode exit %d: %s%s", code, out, errs)
	}
	return assets, ddstPath
}

func TestEncodeToOutputFile(t *testing.T) {
	_, path := encodeFixture(t, "--copies", "2")
	code, out, errs := runCmd("decode", path)
	if code != 0 {
		t.Fatalf("decode exit %d: %s", code, errs)
	}
	if n := strings.Count(out, "GDIP @"); n != 4 {
		t.Errorf("want 4 GDIP (2 pages x 2 copies), got %d:\n%s", n, out)
	}
	for _, want := range []string{"GDIJ @0", `title="p1.pgm"`, "color=false", "bpp=1",
		"size=2304x3296", "planes=1", "bands=13", "GDIB x13", "JIDG @"} {
		if !strings.Contains(out, want) {
			t.Errorf("decode output lacks %q:\n%s", want, out)
		}
	}
}

func TestFlagsAfterFile(t *testing.T) {
	assets := writeScreens(t)
	tmp := t.TempDir()
	p := filepath.Join(tmp, "p.pgm")
	writePGM(t, p, true)
	o := filepath.Join(tmp, "o.ddst")
	// flags both before and after the positional argument
	code, out, errs := runCmd("encode", "--mono", p, "--paper", "A6", "--assets", assets, "--output", o)
	if code != 0 || !strings.HasPrefix(out, "wrote 1 pages (") || !strings.HasSuffix(out, "to "+o+"\n") {
		t.Fatalf("exit %d out %q err %q", code, out, errs)
	}
}

func TestPrintFlagsConflict(t *testing.T) {
	code, _, errs := runCmd("print", "x.pdf", "--output", "o.ddst", "--printer", "127.0.0.1")
	if code != 2 || !strings.Contains(errs, "mutually exclusive") {
		t.Errorf("exit %d, stderr %q", code, errs)
	}
}

func TestBadQuality(t *testing.T) {
	assets := writeScreens(t)
	code, _, errs := runCmd("encode", "x.pam", "--quality", "ultra", "--assets", assets, "--output", "o.ddst")
	if code != 2 || !strings.Contains(errs, "quality") {
		t.Errorf("exit %d, stderr %q", code, errs)
	}
}

func TestPrintValidatesBeforeRendering(t *testing.T) {
	// missing assets must fail before touching the input or the network
	code, _, errs := runCmd("print", "nonexistent.pdf", "--assets", t.TempDir(), "--output", filepath.Join(t.TempDir(), "o"))
	if code != 1 || !strings.Contains(errs, "screens") {
		t.Errorf("exit %d, stderr %q", code, errs)
	}
	code, _, errs = runCmd("print", "nonexistent.pdf", "--assets", writeScreens(t), "--output", filepath.Join(t.TempDir(), "o"))
	if code != 1 || !strings.Contains(errs, "ICC profile") {
		t.Errorf("ICC case: exit %d, stderr %q", code, errs)
	}
	assets := writeScreens(t)
	code, _, errs = runCmd("print", filepath.Join(t.TempDir(), "nonexistent.pdf"), "--mono", "--assets", assets, "--output", filepath.Join(t.TempDir(), "o"))
	if code != 1 || !strings.Contains(errs, "nonexistent.pdf") {
		t.Errorf("input case: exit %d, stderr %q", code, errs)
	}
}

func TestDecodePrintsHeaders(t *testing.T) {
	_, path := encodeFixture(t, "--paper", "A6", "--tray", "tray1", "--type", "plain")
	code, out, _ := runCmd("decode", path)
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	for _, want := range []string{"copies=1", "paper=0x46", "tray=2", "type=0x01", "side=0x01", "pageno=2"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if code, _, errs := runCmd("decode", filepath.Join(t.TempDir(), "none")); code != 1 || errs == "" {
		t.Errorf("missing file: exit %d", code)
	}
}

func TestDecodePNG(t *testing.T) {
	_, path := encodeFixture(t)
	dir := filepath.Join(t.TempDir(), "png")
	code, _, errs := runCmd("decode", path, "--png", dir)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	for _, n := range []string{"page-01.png", "page-02.png"} {
		if _, err := os.Stat(filepath.Join(dir, n)); err != nil {
			t.Errorf("%v", err)
		}
	}
	f, err := os.Open(filepath.Join(dir, "page-01.png"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	img, err := png.Decode(f)
	if err != nil {
		t.Fatal(err)
	}
	if b := img.Bounds(); b.Dx() != a6W || b.Dy() != a6H {
		t.Fatalf("size %v", b)
	}
	if r, _, _, _ := img.At(400, 500).RGBA(); r != 0 {
		t.Errorf("square should be black, red=%d", r)
	}
	if r, _, _, _ := img.At(1000, 1000).RGBA(); r != 0xFFFF {
		t.Errorf("background should be white, red=%d", r)
	}
}

func TestEncodeSendsToLoopbackPrinter(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skip(err)
	}
	defer ln.Close()
	got := make(chan []byte, 1)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			got <- nil
			return
		}
		defer c.Close()
		c.SetReadDeadline(time.Now().Add(20 * time.Second))
		b, _ := io.ReadAll(c)
		got <- b
	}()
	assets := writeScreens(t)
	p := filepath.Join(t.TempDir(), "p.pgm")
	writePGM(t, p, true)
	code, out, errs := runCmd("encode", p, "--mono", "--paper", "A6", "--assets", assets,
		"--quality", "fast", "--printer", ln.Addr().String())
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	if !strings.HasPrefix(out, "sent 1 pages (") || !strings.Contains(out, "to "+ln.Addr().String()) {
		t.Errorf("output %q", out)
	}
	b := <-got
	if !bytes.HasPrefix(b, []byte("GDIJ")) || !bytes.HasSuffix(b, []byte("JIDG")) {
		t.Errorf("stream starts %q ends %q (%d bytes)", b[:min(4, len(b))], b[max(0, len(b)-4):], len(b))
	}
}

func TestNoConnectionOnBadInput(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skip(err)
	}
	defer ln.Close()
	accepted := make(chan struct{}, 1)
	go func() {
		if c, err := ln.Accept(); err == nil {
			accepted <- struct{}{}
			c.Close()
		}
	}()
	assets := writeScreens(t)
	bad := filepath.Join(t.TempDir(), "bad.pgm")
	os.WriteFile(bad, []byte("not a page"), 0o644)
	code, _, _ := runCmd("encode", bad, "--mono", "--paper", "A6", "--assets", assets, "--printer", ln.Addr().String())
	if code != 1 {
		t.Errorf("exit %d", code)
	}
	select {
	case <-accepted:
		t.Error("printer connection opened despite a bad page")
	case <-time.After(200 * time.Millisecond):
	}
}

func TestValidationBeforeOutputAndRender(t *testing.T) {
	assets := writeScreens(t)
	tmp := t.TempDir()
	in := filepath.Join(tmp, "in.pdf")
	os.WriteFile(in, []byte("%PDF-dummy"), 0o644)
	page := filepath.Join(tmp, "p.pgm")
	writePGM(t, page, false)
	out := filepath.Join(tmp, "existing.ddst")
	for name, args := range map[string][]string{
		"print":  {"print", in, "--mono", "--paper", "A6", "--duplex", "long", "--assets", assets, "--output", out},
		"encode": {"encode", page, "--mono", "--paper", "A6", "--duplex", "long", "--assets", assets, "--output", out},
		"addr":   {"encode", page, "--mono", "--paper", "A6", "--assets", assets, "--printer", " "},
	} {
		os.WriteFile(out, []byte("keep me"), 0o644)
		if code, _, errs := runCmd(args...); code != 2 {
			t.Errorf("%s: exit %d, stderr %q", name, code, errs)
		}
		if b, _ := os.ReadFile(out); string(b) != "keep me" {
			t.Errorf("%s: output file was modified: %q", name, b)
		}
	}
}

func TestAbortAfterConnectSendsNoJIDG(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skip(err)
	}
	defer ln.Close()
	got := make(chan []byte, 1)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			got <- nil
			return
		}
		defer c.Close()
		c.SetReadDeadline(time.Now().Add(10 * time.Second))
		b, _ := io.ReadAll(c) // a reset ends the read early; keep what arrived
		got <- b
	}()
	assets := writeScreens(t)
	tmp := t.TempDir()
	good, bad := filepath.Join(tmp, "good.pgm"), filepath.Join(tmp, "bad.pgm")
	writePGM(t, good, true)
	os.WriteFile(bad, []byte("junk"), 0o644)
	code, out, _ := runCmd("encode", good, bad, "--mono", "--paper", "A6", "--assets", assets,
		"--quality", "fast", "--printer", ln.Addr().String())
	if code != 1 || out != "" {
		t.Errorf("exit %d, stdout %q", code, out)
	}
	b := <-got
	if len(b) == 0 {
		t.Error("no connection/bytes received: the abort path was not exercised")
	}
	if bytes.Contains(b, []byte("JIDG")) {
		t.Error("aborted job contains JIDG")
	}

	o := filepath.Join(tmp, "o.ddst")
	code, _, _ = runCmd("encode", good, bad, "--mono", "--paper", "A6", "--assets", assets, "--output", o)
	if code != 1 {
		t.Errorf("output variant exit %d", code)
	}
	if _, err := os.Stat(o); !os.IsNotExist(err) {
		t.Errorf("partial output file not removed: %v", err)
	}
}

func TestTestpageOutputThenDecode(t *testing.T) {
	assets := writeScreens(t)
	path := filepath.Join(t.TempDir(), "tp.ddst")
	if code, out, errs := runCmd("testpage", "--output", path, "--assets", assets, "--quality", "fast"); code != 0 {
		t.Fatalf("testpage exit %d: %s%s", code, out, errs)
	}
	code, out, errs := runCmd("decode", path)
	if code != 0 {
		t.Fatalf("decode exit %d: %s", code, errs)
	}
	var gdip []string
	for _, l := range strings.Split(out, "\n") {
		if strings.HasPrefix(l, "GDIP @") {
			gdip = append(gdip, l)
		}
	}
	if len(gdip) != 2 {
		t.Fatalf("want 2 GDIP, got %d:\n%s", len(gdip), out)
	}
	if !strings.Contains(gdip[0], "side=0x0d") || !strings.Contains(gdip[0], "pageno=2") || !strings.Contains(gdip[0], "planes=1") {
		t.Errorf("first record should be back/pageno 2/planes=1: %s", gdip[0])
	}
	if !strings.Contains(gdip[1], "side=0x05") || !strings.Contains(gdip[1], "pageno=1") || !strings.Contains(gdip[1], "planes=4") {
		t.Errorf("second record should be front/pageno 1/planes=4: %s", gdip[1])
	}
}

func TestTestpageNeedsExplicitDestination(t *testing.T) {
	code, _, errs := runCmd("testpage", "--assets", writeScreens(t))
	if code != 2 || !strings.Contains(errs, "testpage needs --output FILE or --printer ADDR") {
		t.Errorf("exit %d, stderr %q", code, errs)
	}
}
