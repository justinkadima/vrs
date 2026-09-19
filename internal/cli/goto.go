package cli

import (
	"flag"
	"fmt"
	"io"
	"path/filepath"

	"github.com/justinkadima/vrs/internal/snap"
)

// runGoto materializes any snapshot into the working copy. The full tree is
// always the scope (invariant for goto/save). The pre-jump state is captured
// first, so goto can never lose work. goto moves the working copy's position
// (meta.base): diff/undo now compare against the target, and a save from
// here forks a new line from it.
func runGoto(args []string, out, errW io.Writer) error {
	fs := flag.NewFlagSet("goto", flag.ContinueOnError)
	fs.SetOutput(errW)
	if err := fs.Parse(args); err != nil {
		return ErrUsage
	}
	if fs.NArg() > 1 {
		fmt.Fprintln(errW, "usage: vrs goto [@ref]")
		return ErrUsage
	}
	rc, err := openRepo()
	if err != nil {
		return err
	}
	defer rc.st.Close()

	var targetID int64
	if fs.NArg() == 0 {
		id, ok, err := rc.st.LatestSave()
		if err != nil {
			return err
		}
		if !ok {
			fmt.Fprintln(out, "no snapshots yet — run `vrs save` first")
			return nil
		}
		targetID = id
	} else {
		spec, err := parseRef(fs.Arg(0))
		if err != nil {
			return err
		}
		if targetID, err = resolveRef(rc.st, spec); err != nil {
			return err
		}
	}
	target, err := rc.st.SnapshotEntries(targetID)
	if err != nil {
		return err
	}

	base, err := rc.st.Base()
	if err != nil {
		return err
	}

	stats, err := snap.Status(rc.root, "", target, rc.cache, rc.pol, rc.ig)
	if err != nil {
		return err
	}
	divergent := 0
	for _, st := range stats {
		if st.Status != snap.StatusSame {
			divergent++
		}
	}
	if divergent == 0 {
		// Already there — but an explicit goto can still move the position
		// (e.g. the working copy happens to match the tip while positioned
		// on an old snapshot).
		if base != targetID {
			if err := rc.st.SetBase(targetID); err != nil {
				return err
			}
			fmt.Fprintf(out, "working copy already at #%d (position moved from #%d)\n", targetID, base)
		} else {
			fmt.Fprintf(out, "working copy already at #%d\n", targetID)
		}
		return nil
	}

	// Capture-before-mutate: the pre-jump state is preserved as a hidden
	// snapshot, so goto can never lose work.
	capRes, err := snap.Capture(rc.root, rc.pol, rc.cache, rc.ig)
	if err != nil {
		return err
	}
	capInfo, err := rc.st.Save(capRes, "", "capture", base)
	if err != nil {
		return err
	}

	trashBase := rc.trashDir()
	mat, err := snap.Materialize(rc.root, "", target, rc.cache, rc.st, trashBase, rc.pol, rc.ig)
	if err != nil {
		return err
	}
	if err := rc.st.SyncCache(mat.Written, mat.Trashed); err != nil {
		return err
	}
	// Any working-copy mutation invalidates pending redos (editor convention).
	if err := rc.st.RedoClear(); err != nil {
		return err
	}
	if err := rc.st.SetBase(targetID); err != nil {
		return err
	}
	if err := rc.st.PushOp("goto", map[string]any{
		"capture": capInfo.ID, "target": targetID,
		"written": len(mat.Written), "trashed": len(mat.Trashed),
	}); err != nil {
		return err
	}

	fmt.Fprintf(out, "working copy now at #%d — previous state captured as #%d (`vrs log --all`)\n",
		targetID, capInfo.ID)
	if len(mat.Written) > 0 {
		fmt.Fprintf(out, "  %d file(s) written\n", len(mat.Written))
	}
	if len(mat.Trashed) > 0 {
		fmt.Fprintf(out, "  %d file(s) moved to %s\n", len(mat.Trashed),
			filepath.ToSlash(filepath.Join(".vrs", "trash", filepath.Base(trashBase))))
	}
	if tip, ok, err := rc.st.LatestSave(); err == nil && ok && tip != targetID {
		fmt.Fprintf(out, "you are behind the tip (#%d) — save here to start a new line from #%d, or `vrs goto` (no argument) to return\n",
			tip, targetID)
	}
	return nil
}
