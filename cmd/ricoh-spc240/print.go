package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/kipavy/ricoh-spc240/internal/raster"
	"github.com/kipavy/ricoh-spc240/internal/render"
)

func cmdPrint(args []string, stdout, stderr io.Writer) int {
	c := newCommon("print", stderr)
	pos, err := c.parseArgs(args)
	if errors.Is(err, flag.ErrHelp) {
		return 0
	}
	if err == nil && len(pos) != 1 {
		err = usagef("print needs exactly one FILE")
	}
	if err != nil {
		return exitCode(err, stderr)
	}
	return exitCode(runPrint(c, pos[0], stdout), stderr)
}

func runPrint(c *common, input string, stdout io.Writer) error {
	o, err := c.jobOptions(filepath.Base(input))
	if err != nil {
		return err
	}
	icc := filepath.Join(c.assets, "ricoh-c240dn.icc")
	if o.Color {
		if _, err := os.Stat(icc); err != nil {
			return fmt.Errorf("ICC profile: %w", err)
		}
	}
	f, err := os.Open(input)
	if err != nil {
		return err
	}
	f.Close()
	kind, err := render.Detect(input)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	tmp, err := os.MkdirTemp("", "ricoh-spc240-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	rctx, cancel := context.WithCancel(ctx)
	files, errc := render.Render(rctx, render.Request{Input: input, Paper: o.Paper,
		Color: o.Color, Pages: c.pages, ICC: icc, TmpDir: tmp, Landscape: c.landscape})
	defer func() { // stop the renderer and wait for it before removing tmp
		cancel()
		for pf := range files {
			os.Remove(pf.Path)
		}
	}()
	next := func() (*raster.Page, error) {
		pf, ok := <-files
		if !ok {
			if err := <-errc; err != nil {
				return nil, err
			}
			return nil, io.EOF
		}
		p, err := readPage(pf.Path, true)
		if err == nil {
			p = landscapeRotate(p, kind, c.landscape)
		}
		return p, err
	}
	return c.writeJob(ctx, stdout, o, next)
}

// readPage loads a PAM/PGM file, optionally deleting it afterwards.
func readPage(path string, remove bool) (*raster.Page, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if remove {
		defer os.Remove(path)
	}
	p, err := raster.ReadPAM(f)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return p, nil
}

// landscapeRotate turns a page rendered on landscape media onto the portrait
// sheet, with the content's top on the right-hand long edge. Text (enscript
// output) comes out of Ghostscript turned the other way, so it needs an extra
// half turn to follow the same convention as images and PDFs.
func landscapeRotate(p *raster.Page, kind render.Kind, landscape bool) *raster.Page {
	if !landscape {
		return p
	}
	p = p.Rotate90CW()
	if kind == render.KindText {
		p.Rotate180()
	}
	return p
}
