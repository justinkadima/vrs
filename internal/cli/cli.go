// Package cli implements the vrs command line interface.
package cli

import (
	"errors"
	"fmt"
	"io"
)

// Version is the vrs build version.
const Version = "0.1.0-m0"

const usage = `vrs — snapshots for your code

Usage:
  vrs save [message] [-m msg]   snapshot the working tree (auto-initializes)
  vrs log [-n N] [--all]        show the timeline
  vrs version                   print version
  vrs help                      show this help
`

// ErrUsage signals a usage error (exit code 2).
var ErrUsage = errors.New("usage")

// Run executes the CLI with the given arguments, writing normal output to out
// and diagnostics to errW.
func Run(args []string, out, errW io.Writer) error {
	if len(args) == 0 {
		fmt.Fprint(errW, usage)
		return ErrUsage
	}
	switch args[0] {
	case "save":
		return runSave(args[1:], out, errW)
	case "log":
		return runLog(args[1:], out, errW)
	case "version":
		fmt.Fprintln(out, "vrs "+Version)
		return nil
	case "help", "--help", "-h":
		fmt.Fprint(out, usage)
		return nil
	default:
		fmt.Fprintf(errW, "vrs: unknown command %q\n\n%s", args[0], usage)
		return ErrUsage
	}
}
