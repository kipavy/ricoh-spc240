package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/kipavy/ricoh-spc240/internal/ddst"
	"github.com/kipavy/ricoh-spc240/internal/halftone"
	"github.com/kipavy/ricoh-spc240/internal/raster"
	"github.com/kipavy/ricoh-spc240/internal/render"
	"github.com/kipavy/ricoh-spc240/internal/send"
)

const (
	defaultAssets = "/usr/share/ricoh-spc240"
)

// common holds the flags shared by print and encode.
type common struct {
	fs                                *flag.FlagSet
	mono, noCollate, grayK, landscape bool
	duplex, pages, paper, ptype, tray string
	quality, assets, printer, output  string
	copies                            int
	addr                              string // normalised printer address
}

func newCommon(name string, stderr io.Writer) *common {
	c := &common{fs: flag.NewFlagSet(name, flag.ContinueOnError)}
	c.fs.SetOutput(stderr)
	c.fs.BoolVar(&c.mono, "mono", false, "print in black and white")
	c.fs.StringVar(&c.duplex, "duplex", "none", "none, long or short (binding edge)")
	c.fs.IntVar(&c.copies, "copies", 1, "number of copies")
	c.fs.BoolVar(&c.noCollate, "no-collate", false, "do not collate copies")
	c.fs.StringVar(&c.pages, "pages", "", "page selection, e.g. 1-3,5")
	c.fs.StringVar(&c.paper, "paper", "A4", "paper size")
	c.fs.StringVar(&c.ptype, "type", "plainrecycled", "paper type")
	c.fs.StringVar(&c.tray, "tray", "auto", "input tray")
	c.fs.StringVar(&c.quality, "quality", "fast", "fast, standard or fine")
	c.fs.BoolVar(&c.grayK, "gray-k", false, "print neutral colours with black toner only")
	c.fs.BoolVar(&c.landscape, "landscape", false, "print landscape documents full page (rotated onto portrait paper)")
	assets := defaultAssets
	if v := os.Getenv("RICOH_ASSETS"); v != "" {
		assets = v
	}
	c.fs.StringVar(&c.assets, "assets", assets, "directory with screens/ and ricoh-c240dn.icc")
	printer := ""
	if v := os.Getenv("RICOH_PRINTER"); v != "" {
		printer = v
	}
	c.fs.StringVar(&c.printer, "printer", printer, "printer HOST[:PORT]")
	c.fs.StringVar(&c.output, "output", "", "write the job to this file instead of printing")
	return c
}

// parse parses args with flags allowed before and after positional arguments.
func parse(fs *flag.FlagSet, args []string) ([]string, error) {
	var pos []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		if fs.NArg() == 0 {
			return pos, nil
		}
		pos = append(pos, fs.Arg(0))
		args = fs.Args()[1:]
	}
}

// parseArgs parses args and returns the positional arguments.
func (c *common) parseArgs(args []string) ([]string, error) {
	pos, err := parse(c.fs, args)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil, err
		}
		return nil, usageError{err}
	}
	set := map[string]bool{}
	c.fs.Visit(func(f *flag.Flag) { set[f.Name] = true })
	if c.output != "" && set["printer"] {
		return nil, usagef("--output and --printer are mutually exclusive")
	}
	return pos, nil
}

// jobOptions builds and validates the ddst options and loads the screens.
func (c *common) jobOptions(title string) (ddst.Options, error) {
	var o ddst.Options
	var err error
	if o.Paper, err = ddst.LookupPaper(c.paper); err != nil {
		return o, usageError{err}
	}
	if o.Type, err = ddst.LookupPaperType(c.ptype); err != nil {
		return o, usageError{err}
	}
	if o.Tray, err = ddst.LookupTray(c.tray); err != nil {
		return o, usageError{err}
	}
	if o.Quality, err = halftone.ParseQuality(c.quality); err != nil {
		return o, usageError{err}
	}
	switch strings.ToLower(c.duplex) {
	case "none", "":
		o.Duplex = ddst.DuplexNone
	case "long":
		o.Duplex = ddst.DuplexLongEdge
	case "short":
		o.Duplex = ddst.DuplexShortEdge
	default:
		return o, usagef("unknown duplex mode %q (want none, long or short)", c.duplex)
	}
	if c.copies < 1 || c.copies > 999 {
		return o, usagef("copies must be between 1 and 999")
	}
	if c.noCollate && o.Duplex != ddst.DuplexNone {
		fmt.Fprintln(c.fs.Output(), "ricoh-spc240: warning: --no-collate is ignored with --duplex: copies are collated")
	}
	if c.pages != "" {
		if _, err := render.ParsePages(c.pages, 1<<16); err != nil {
			return o, usageError{err}
		}
	}
	o.Title, o.Color, o.Copies = title, !c.mono, c.copies
	o.Collate, o.GrayToK = !c.noCollate, c.grayK
	o.Screens, err = halftone.LoadScreenSet(filepath.Join(c.assets, "screens"), o.Quality, o.Color)
	if err != nil {
		return o, fmt.Errorf("loading screens (is --assets %q right?): %w", c.assets, err)
	}
	// Dry run: reject incompatible options before anything is rendered or opened.
	if _, err := ddst.NewJob(io.Discard, o); err != nil {
		return o, usageError{err}
	}
	if c.output == "" {
		if c.printer == "" {
			return o, usagef("no printer: pass --printer HOST[:PORT], set RICOH_PRINTER, or use --output FILE")
		}
		if c.addr, err = send.NormalizeAddr(c.printer); err != nil {
			return o, usageError{err}
		}
	}
	return o, nil
}

// sink is the destination of a job: a file, or a printer connection that is
// opened only once the first page has been encoded. Until then the stream is
// buffered so that a rendering error never opens a connection.
type sink struct {
	f     *os.File
	conn  send.Conn
	buf   bytes.Buffer
	bytes int
	addr  string
}

func (s *sink) Write(p []byte) (int, error) {
	s.bytes += len(p)
	switch {
	case s.f != nil:
		return s.f.Write(p)
	case s.conn != nil:
		return s.conn.Write(p)
	}
	return s.buf.Write(p)
}

func (s *sink) connect(ctx context.Context) error {
	if s.f != nil || s.conn != nil {
		return nil
	}
	conn, err := send.Dial(ctx, s.addr, 10*time.Second)
	if err != nil {
		return err
	}
	s.conn = conn
	_, err = s.conn.Write(s.buf.Bytes())
	s.buf = bytes.Buffer{}
	return err
}

// abort closes the destination without finishing the job. On a printer
// connection no JIDG is sent, so the printer discards the partial job.
func (s *sink) abort() {
	if s.conn != nil {
		s.conn.Abort()
	}
	if s.f != nil {
		name := s.f.Name()
		s.f.Close()
		os.Remove(name)
	}
}

// nextPage returns the next page raster or io.EOF.
type nextPage func() (*raster.Page, error)

// writeJob feeds pages from next into a job built from c and reports the
// result on stdout.
func (c *common) writeJob(ctx context.Context, stdout io.Writer, o ddst.Options, next nextPage) error {
	s := &sink{}
	if c.output != "" {
		f, err := os.Create(c.output)
		if err != nil {
			return err
		}
		s.f = f
	} else {
		s.addr = c.addr
	}
	job, err := ddst.NewJob(s, o)
	if err != nil {
		s.abort()
		return err
	}
	w, h := ddst.PageRaster(o.Paper)
	pages := 0
	for {
		if ctx.Err() != nil {
			s.abort()
			return ctx.Err()
		}
		p, err := next()
		if err == io.EOF {
			break
		}
		if err == nil {
			err = job.AddPage(p.Crop(w, h))
		}
		if err == nil && pages == 0 {
			err = s.connect(ctx)
		}
		if err != nil {
			s.abort()
			return err
		}
		pages++
	}
	if pages == 0 {
		s.abort()
		return errors.New("no pages to print")
	}
	if err := job.Close(); err != nil {
		s.abort()
		return err
	}
	if s.f != nil {
		if err := s.f.Close(); err != nil {
			return err
		}
		fmt.Fprintf(stdout, "wrote %d pages (%d bytes) to %s\n", pages*o.Copies, s.bytes, c.output)
		return nil
	}
	if err := s.connect(ctx); err != nil {
		return err
	}
	if err := s.conn.Close(); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "sent %d pages (%d bytes) to %s\n", pages*o.Copies, s.bytes, s.addr)
	return nil
}
