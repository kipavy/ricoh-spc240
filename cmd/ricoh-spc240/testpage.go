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
		err = c.checkTestpageFlags()
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

// checkTestpageFlags requires an explicit destination and rejects every flag
// that would change the fixed A4 duplex colour layout.
func (c *common) checkTestpageFlags() error {
	set := map[string]bool{}
	c.fs.Visit(func(f *flag.Flag) { set[f.Name] = true })
	for _, name := range []string{"paper", "duplex", "type", "tray", "mono", "pages", "copies", "no-collate", "gray-k"} {
		if set[name] {
			return usagef("testpage: --%s is not supported (the test page is always A4 duplex colour)", name)
		}
	}
	if c.output == "" && !set["printer"] {
		return usagef("testpage needs --output FILE or --printer ADDR")
	}
	return nil
}
