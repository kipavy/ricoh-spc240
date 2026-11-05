package main

import (
	"context"
	"errors"
	"flag"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/kipavy/ricoh-spc240/internal/raster"
	"github.com/kipavy/ricoh-spc240/internal/testpage"
)

// cmdTestpage prints (or writes) the one-sheet duplex test page. It never
// falls back to the default printer: paper is only used on explicit request.
func cmdTestpage(args []string, stdout, stderr io.Writer) int {
	c := newCommon("testpage", stderr)
	pos, err := c.parseArgs(args)
	if errors.Is(err, flag.ErrHelp) {
		return 0
	}
	if err == nil && len(pos) > 0 {
		err = usagef("testpage takes no PAGE arguments")
	}
	if err == nil {
		printerSet := false
		c.fs.Visit(func(f *flag.Flag) { printerSet = printerSet || f.Name == "printer" })
		if c.output == "" && !printerSet {
			err = usagef("testpage needs --output FILE or --printer ADDR")
		}
	}
	if err != nil {
		return exitCode(err, stderr)
	}
	// The job is fixed: colour, duplex long edge, A4, plain recycled paper.
	c.mono, c.grayK, c.copies, c.noCollate = false, false, 1, false
	c.paper, c.ptype, c.tray, c.duplex, c.pages = "A4", "plainrecycled", "auto", "long", ""
	return exitCode(runTestpage(c, stdout), stderr)
}

func runTestpage(c *common, stdout io.Writer) error {
	o, err := c.jobOptions("ricoh-spc240 test page")
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	pp := testpage.Pages()
	i := 0
	next := nextPage(func() (*raster.Page, error) {
		if i == len(pp) {
			return nil, io.EOF
		}
		i++
		return pp[i-1], nil
	})
	return c.writeJob(ctx, stdout, o, next)
}
