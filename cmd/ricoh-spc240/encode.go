package main

import (
	"context"
	"errors"
	"flag"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/kipavy/ricoh-spc240/internal/raster"
)

// cmdEncode builds a job from ready-made PAM/PGM page rasters (no Ghostscript).
func cmdEncode(args []string, stdout, stderr io.Writer) int {
	c := newCommon("encode", stderr)
	pos, err := c.parseArgs(args)
	if errors.Is(err, flag.ErrHelp) {
		return 0
	}
	if err == nil && len(pos) == 0 {
		err = usagef("encode needs at least one PAGE file")
	}
	if err != nil {
		return exitCode(err, stderr)
	}
	return exitCode(runEncode(c, pos, stdout), stderr)
}

func runEncode(c *common, files []string, stdout io.Writer) error {
	o, err := c.jobOptions(filepath.Base(files[0]))
	if err != nil {
		return err
	}
	for _, f := range files {
		if _, err := os.Stat(f); err != nil {
			return err
		}
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	i := 0
	next := func() (*raster.Page, error) {
		if i == len(files) {
			return nil, io.EOF
		}
		i++
		return readPage(files[i-1], false)
	}
	return c.writeJob(ctx, stdout, o, next)
}
