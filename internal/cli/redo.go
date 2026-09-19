package cli

import (
	"flag"
	"fmt"
	"io"
	"path/filepath"

	"github.com/justinkadima/vrs/internal/snap"
)

func runRedo(args []string, out, errW io.Writer) error {
	fs := flag.NewFlagSet("redo", flag.ContinueOnError)
	fs.SetOutput(errW)
	force := fs.Bool("force", false, "redo even if files changed since the undo")
	if err := fs.Parse(args); err != nil {
		return ErrUsage
	}
	if fs.NArg() > 0 {
		fmt.Fprintln(errW, "usage: vrs redo [--force]")
		return ErrUsage
	}
	rc, err := openRepo()
	if err != nil {
		return err
	}
	defer rc.st.Close()

	entry, err := rc.st.RedoPeek()
	if err != nil {
		return err
	}
	if entry == nil {
		fmt.Fprintln(out, "nothing to redo")
		return nil
	}

	// Refuse to clobber edits made after the undo (editor convention):
	// the scope must still match the undo's target snapshot.
	target, err := rc.st.SnapshotEntries(entry.Target)
	if err != nil {
		return err
	}
	stats, err := snap.Status(rc.root, entry.Scope, target, rc.cache, rc.pol, rc.ig)
	if err != nil {
		return err
	}
	var changed []string
	for _, st := range stats {
		if st.Status != snap.StatusSame {
			changed = append(changed, st.Path)
		}
	}
	if len(changed) > 0 && !*force {
		fmt.Fprintf(out, "refusing to redo — %d file(s) changed since the undo:\n", len(changed))
		for i, p := range changed {
			if i == 5 {
				fmt.Fprintf(out, "  …\n")
				break
			}
			fmt.Fprintf(out, "  %s\n", p)
		}
		fmt.Fprintln(out, "use `vrs redo --force` (your current state is captured first, nothing is lost)")
		return nil
	}

	// Forced redo over newer edits: capture-before-mutate, then overwrite.
	var forcedCapture int64
	if len(changed) > 0 {
		capRes, err := snap.Capture(rc.root, rc.pol, rc.cache, rc.ig)
		if err != nil {
			return err
		}
		base, err := rc.st.Base()
		if err != nil {
			return err
		}
		ci, err := rc.st.Save(capRes, "", "capture", base)
		if err != nil {
			return err
		}
		forcedCapture = ci.ID
		fmt.Fprintf(out, "captured current state as #%d before overwriting (--force)\n", forcedCapture)
	}

	// Reapply the pre-undo state within the recorded scope.
	capEntries, err := rc.st.SnapshotEntries(entry.Capture)
	if err != nil {
		return err
	}
	trashBase := rc.trashDir()
	mat, err := snap.Materialize(rc.root, entry.Scope, capEntries, rc.cache, rc.st, trashBase, rc.pol, rc.ig)
	if err != nil {
		return err
	}
	if err := rc.st.SyncCache(mat.Written, mat.Trashed); err != nil {
		return err
	}
	if err := rc.st.RedoDelete(entry.ID); err != nil {
		return err
	}
	detail := map[string]any{
		"capture": entry.Capture, "target": entry.Target, "scope": entry.Scope,
		"written": len(mat.Written), "trashed": len(mat.Trashed),
	}
	if forcedCapture > 0 {
		detail["forced_capture"] = forcedCapture
	}
	if err := rc.st.PushOp("redo", detail); err != nil {
		return err
	}

	fmt.Fprintf(out, "redone — %d file(s) written, %d trashed (state from capture #%d)\n",
		len(mat.Written), len(mat.Trashed), entry.Capture)
	if len(mat.Trashed) > 0 {
		fmt.Fprintf(out, "  trashed under %s\n",
			filepath.ToSlash(filepath.Join(".vrs", "trash", filepath.Base(trashBase))))
	}
	return nil
}
