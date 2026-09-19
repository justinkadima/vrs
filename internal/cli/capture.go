package cli

import (
	"flag"
	"fmt"
	"io"
)

// runCapture writes a hidden checkpoint snapshot — a snapshot that doesn't
// advance the timeline's tip and doesn't clear pending redos. Intended for
// agents and scripts; humans get the same effect implicitly from
// capture-before-mutate. Idempotent: an unchanged tree is never stored
// twice, the id of the snapshot that already records the state is returned.
func runCapture(args []string, out, errW io.Writer) error {
	fs := flag.NewFlagSet("capture", flag.ContinueOnError)
	fs.SetOutput(errW)
	tag := fs.String("t", "", "checkpoint label")
	if err := fs.Parse(args); err != nil {
		return ErrUsage
	}
	if fs.NArg() > 0 {
		fmt.Fprintln(errW, "usage: vrs capture [-t tag]")
		return ErrUsage
	}
	rc, err := openRepo()
	if err != nil {
		return err
	}
	defer rc.st.Close()

	id, created, changed, err := checkpoint(rc, *tag)
	if err != nil {
		return err
	}
	if !created {
		fmt.Fprintf(out, "unchanged — state already recorded as #%d\n", id)
		return nil
	}
	if *tag != "" {
		fmt.Fprintf(out, "checkpoint #%d — %d file(s) changed (tag: %s) — restore with `vrs goto @%d`\n",
			id, changed, *tag, id)
		return nil
	}
	fmt.Fprintf(out, "checkpoint #%d — %d file(s) changed — restore with `vrs goto @%d`\n", id, changed, id)
	return nil
}
