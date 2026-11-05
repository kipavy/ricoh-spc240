// Package render turns an input document (PDF, PostScript, image, text) into
// one 600-dpi PAM (CMYK) or PGM (gray) file per page using external tools:
// Ghostscript, img2pdf and enscript.
package render

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/kipavy/ricoh-spc240/internal/ddst"
)

// Request describes one rendering job.
type Request struct {
	Input  string
	Paper  ddst.PaperSize
	Color  bool   // CMYK through ICC when true, gray otherwise
	Pages  string // "1-3,5", "4-", or "" for all pages
	ICC    string // output ICC profile (colour only)
	TmpDir string // work directory for intermediate and page files
}

// PageFile is one rendered page. The receiver must delete Path after use.
type PageFile struct {
	Index int // 0-based position in the selection
	Page  int // 1-based page number in the document
	Path  string
}

// Kind is the detected input type.
type Kind int

const (
	KindPDF Kind = iota
	KindPS
	KindImage
	KindText
)

var errUnsupported = errors.New("unsupported input")

// Detect classifies a file by magic bytes, falling back to its extension.
func Detect(path string) (Kind, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	buf := make([]byte, 8192)
	n, _ := f.Read(buf)
	buf = buf[:n]
	ext := strings.ToLower(filepath.Ext(path))

	switch {
	case bytes.HasPrefix(buf, []byte("%PDF-")):
		return KindPDF, nil
	case bytes.HasPrefix(buf, []byte("%!")):
		return KindPS, nil
	case bytes.HasPrefix(buf, []byte("\x89PNG")),
		bytes.HasPrefix(buf, []byte{0xFF, 0xD8, 0xFF}),
		bytes.HasPrefix(buf, []byte("GIF8")),
		bytes.HasPrefix(buf, []byte("II*\x00")),
		bytes.HasPrefix(buf, []byte("MM\x00*")):
		return KindImage, nil
	case bytes.HasPrefix(buf, []byte("BM")) && ext == ".bmp":
		return KindImage, nil
	}
	switch ext {
	case ".pdf":
		return KindPDF, nil
	case ".ps", ".eps":
		return KindPS, nil
	case ".jpg", ".jpeg", ".png", ".gif", ".tif", ".tiff", ".bmp":
		return KindImage, nil
	}
	if isText(buf, n == len(buf)) {
		return KindText, nil
	}
	return 0, fmt.Errorf("%w: %s", errUnsupported, path)
}

// isText reports whether b is NUL-free valid UTF-8; when the sample may have
// been cut mid-rune, up to 3 trailing bytes are forgiven.
func isText(b []byte, maybeCut bool) bool {
	if bytes.IndexByte(b, 0) >= 0 {
		return false
	}
	if maybeCut {
		for i := 0; i < utf8.UTFMax-1 && len(b) > 0 && !utf8.Valid(b); i++ {
			b = b[:len(b)-1]
		}
	}
	return utf8.Valid(b)
}

func pts(v float64) string {
	return strconv.FormatFloat(math.Round(v*1e4)/1e4, 'f', -1, 64)
}

func (r Request) pdfPath() string {
	return r.TmpDir + "/input.pdf"
}

// NormalizeCommands returns the argv lists that produce TmpDir/input.pdf from
// the input (none for a PDF).
func NormalizeCommands(r Request, kind Kind) [][]string {
	pdf := r.pdfPath()
	toPDF := func(in string) []string {
		return []string{"gs", "-q", "-dSAFER", "-dBATCH", "-dNOPAUSE", "-sDEVICE=pdfwrite", "-o", pdf, in}
	}
	switch kind {
	case KindPS:
		return [][]string{toPDF(r.Input)}
	case KindImage:
		return [][]string{{"img2pdf", "--pagesize", pts(r.Paper.WidthPt) + "ptx" + pts(r.Paper.HeightPt) + "pt",
			"--fit", "into", "-o", pdf, r.Input}}
	case KindText:
		media := r.Paper.Name
		if media == "" {
			media = "A4"
		}
		ps := r.TmpDir + "/input.ps"
		return [][]string{
			{"enscript", "-B", "-q", "--media=" + media, "-o", ps, r.Input},
			toPDF(ps),
		}
	}
	return nil
}

func psString(s string) string {
	return strings.NewReplacer("\\", "\\\\", "(", "\\(", ")", "\\)").Replace(s)
}

// PageCountCommand returns the gs argv that prints the page count of pdf.
func PageCountCommand(pdf string) []string {
	return []string{"gs", "-q", "-dNODISPLAY", "-dSAFER", "--permit-file-read=" + pdf,
		"-c", "(" + psString(pdf) + ") (r) file runpdfbegin pdfpagecount = quit"}
}

// PageCommand returns the gs argv rendering one page (1-based) of pdf and the
// file it writes (named after the real page number).
func PageCommand(r Request, pdf string, page int) (argv []string, outPath string) {
	ext, dev := "pgm", "pgmraw"
	if r.Color {
		ext, dev = "pam", "pamcmyk32"
	}
	outPath = fmt.Sprintf("%s/p%04d.%s", r.TmpDir, page, ext)
	argv = []string{"gs", "-q", "-dSAFER", "-dBATCH", "-dNOPAUSE", "-r600",
		"-dDEVICEWIDTHPOINTS=" + pts(r.Paper.WidthPt), "-dDEVICEHEIGHTPOINTS=" + pts(r.Paper.HeightPt),
		"-dFIXEDMEDIA", "-dPDFFitPage", "-dFitPage", "-dAutoRotatePages=/PageByPage",
		fmt.Sprintf("-dFirstPage=%d", page), fmt.Sprintf("-dLastPage=%d", page),
		"-sDEVICE=" + dev}
	if r.Color {
		argv = append(argv, "-sOutputICCProfile="+r.ICC)
	}
	argv = append(argv, "-o", outPath, pdf)
	return argv, outPath
}

// ParsePages expands a spec like "1-3,5" or "4-" against the page count.
// An empty spec selects every page.
func ParsePages(spec string, count int) ([]int, error) {
	if strings.TrimSpace(spec) == "" {
		all := make([]int, count)
		for i := range all {
			all[i] = i + 1
		}
		return all, nil
	}
	var out []int
	for _, part := range strings.Split(spec, ",") {
		part = strings.ReplaceAll(part, " ", "")
		lo, hi, isRange := strings.Cut(part, "-")
		a, err := strconv.Atoi(lo)
		if err != nil {
			return nil, fmt.Errorf("render: bad page spec %q", part)
		}
		b := a
		if isRange {
			if hi == "" {
				b = count
			} else if b, err = strconv.Atoi(hi); err != nil {
				return nil, fmt.Errorf("render: bad page spec %q", part)
			}
		}
		if a < 1 || b < a || b > count {
			return nil, fmt.Errorf("render: page spec %q out of range (document has %d pages)", part, count)
		}
		for p := a; p <= b; p++ {
			out = append(out, p)
		}
	}
	return out, nil
}

func run(ctx context.Context, argv []string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		tail := strings.TrimSpace(stderr.String())
		if len(tail) > 500 {
			tail = tail[len(tail)-500:]
		}
		return nil, fmt.Errorf("render: %s failed: %w: %s", argv[0], err, tail)
	}
	return stdout.Bytes(), nil
}

// Render normalises the input to PDF and renders the selected pages one at a
// time. A page is only started once the previous PageFile has been received,
// so at most one finished page file waits on disk. The PageFile channel is
// closed when done; at most one error is sent.
func Render(ctx context.Context, r Request) (<-chan PageFile, <-chan error) {
	out := make(chan PageFile)
	errc := make(chan error, 1)
	go func() {
		defer close(errc)
		defer close(out)
		if err := render(ctx, r, out); err != nil {
			errc <- err
		}
	}()
	return out, errc
}

func render(ctx context.Context, r Request, out chan<- PageFile) error {
	kind, err := Detect(r.Input)
	if err != nil {
		return err
	}
	norm := NormalizeCommands(r, kind)
	need := map[string]bool{"gs": true}
	for _, c := range norm {
		need[c[0]] = true
	}
	for tool := range need {
		if _, err := exec.LookPath(tool); err != nil {
			return fmt.Errorf("render: required tool %q not found on PATH", tool)
		}
	}
	pdf := r.Input
	if kind != KindPDF {
		pdf = r.pdfPath()
	}
	for _, c := range norm {
		if _, err := run(ctx, c); err != nil {
			return err
		}
	}
	res, err := run(ctx, PageCountCommand(pdf))
	if err != nil {
		return err
	}
	count, err := strconv.Atoi(strings.TrimSpace(string(res)))
	if err != nil {
		return fmt.Errorf("render: cannot parse page count %q", strings.TrimSpace(string(res)))
	}
	pages, err := ParsePages(r.Pages, count)
	if err != nil {
		return err
	}
	for i, p := range pages {
		argv, path := PageCommand(r, pdf, p)
		if _, err := run(ctx, argv); err != nil {
			return err
		}
		select {
		case out <- PageFile{Index: i, Page: p, Path: path}:
		case <-ctx.Done():
			os.Remove(path)
			return ctx.Err()
		}
	}
	return nil
}
