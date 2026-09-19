package cli

import (
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/justinkadima/vrs/internal/paths"
	"github.com/justinkadima/vrs/internal/store"
)

func runLog(args []string, out, errW io.Writer) error {
	fs := flag.NewFlagSet("log", flag.ContinueOnError)
	fs.SetOutput(errW)
	n := fs.Int("n", 0, "show at most N snapshots (0 = all)")
	all := fs.Bool("all", false, "include hidden captures")
	if err := fs.Parse(args); err != nil {
		return ErrUsage
	}

	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	root, ok := paths.FindRoot(cwd)
	if !ok {
		return fmt.Errorf("not a vrs repository — run `vrs save` to start one here")
	}
	st, err := store.Open(root)
	if err != nil {
		return err
	}
	defer st.Close()

	rows, err := st.ListSnapshots(*all, *n)
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		fmt.Fprintln(out, "no snapshots yet — run `vrs save`")
		return nil
	}
	for _, r := range rows {
		msg := r.Message
		if msg == "" {
			msg = "(no message)"
		}
		tag := ""
		if r.Kind != "save" {
			tag = " [" + r.Kind + "]"
		}
		fmt.Fprintf(out, "#%-4d %s  %s%s\n",
			r.ID, time.Unix(0, r.TS).Format("2006-01-02 15:04"), msg, tag)
	}
	// If the working copy is positioned on an older snapshot (after goto),
	// say so — the timeline alone doesn't show it.
	if base, err := st.Base(); err == nil && base > 0 {
		if tip, ok, err := st.LatestSave(); err == nil && ok && tip != base {
			fmt.Fprintf(out, "\nyou are at #%d — the tip is #%d (save here to start a new line, `vrs goto` to return)\n", base, tip)
		}
	}
	return nil
}
