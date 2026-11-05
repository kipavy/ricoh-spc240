// Command ricoh-spc240 prints to a Ricoh Aficio SP C240DN by generating its
// DDST stream directly (see docs/protocol.md).
package main

import (
	"fmt"
	"io"
	"os"
)

const usage = `usage: ricoh-spc240 <command> [flags]

commands:
  print FILE         render FILE and send it to the printer (or --output FILE.ddst)
  encode PAGE...     encode PAM/PGM page rasters (already page-sized) into a job
  decode FILE.ddst   list the records of a stream (--png DIR writes page previews)

run "ricoh-spc240 <command> -h" for the flags of a command.
`

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run executes one command line and returns the process exit code:
// 0 success, 1 runtime failure, 2 usage error.
func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return 2
	}
	switch args[0] {
	case "print":
		return cmdPrint(args[1:], stdout, stderr)
	case "encode":
		return cmdEncode(args[1:], stdout, stderr)
	case "decode":
		return cmdDecode(args[1:], stdout, stderr)
	case "-h", "--help", "help":
		fmt.Fprint(stdout, usage)
		return 0
	}
	fmt.Fprintf(stderr, "ricoh-spc240: unknown command %q\n\n%s", args[0], usage)
	return 2
}

// usageError marks an error that should exit with code 2.
type usageError struct{ error }

func usagef(format string, a ...any) error {
	return usageError{fmt.Errorf(format, a...)}
}

// exitCode reports err on stderr and maps it to an exit code.
func exitCode(err error, stderr io.Writer) int {
	if err == nil {
		return 0
	}
	fmt.Fprintf(stderr, "ricoh-spc240: %v\n", err)
	if _, ok := err.(usageError); ok {
		return 2
	}
	return 1
}
