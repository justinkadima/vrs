// Package cli implements the vrs command line interface.
package cli

import (
	"errors"
	"fmt"
	"io"
)

// Version is the vrs build version.
const Version = "0.1.0-m2"

const usage = `vrs — snapshots for your code

Usage:
  vrs save [message] [-m msg]   snapshot the working tree (auto-initializes)
  vrs diff [path] [@ref]        changes vs the snapshot you are on
                                (with a path: unified content diff)
  vrs undo [path] [@ref]        restore the working copy from a snapshot
  vrs redo [--force]            reapply the last undone change
  vrs goto [@ref]               jump the working copy to any snapshot
                                (no argument = newest save)
  vrs log [-n N] [--all]        show the timeline (captures only with --all)
  vrs version                   print version
  vrs help                      show this help

Refs: @N = snapshot #N · @-N = N saves back from the tip · @2h = newest
snapshot at least 2h old (@30m, @3d, @1w also work).
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
	case "diff":
		return runDiff(args[1:], out, errW)
	case "undo":
		return runUndo(args[1:], out, errW)
	case "redo":
		return runRedo(args[1:], out, errW)
	case "goto":
		return runGoto(args[1:], out, errW)
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
