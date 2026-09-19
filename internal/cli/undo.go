package cli

import (
	"flag"
	"fmt"
	"io"
	"path/filepath"

	"github.com/justinkadima/vrs/internal/snap"
)

func runUndo(args []string, out, errW io.Writer) error {
	fs := flag.NewFlagSet("undo", flag.ContinueOnError)
	fs.SetOutput(errW)
	if err := fs.Parse(args); err != nil {
		return ErrUsage
	}
	if fs.NArg() > 2 {
		fmt.Fprintln(errW, "usage: vrs undo [path] [@ref]")
		return ErrUsage
	}
	path, ref, err := scanTargetArgs(fs.Args())
	if err != nil {
		return err
	}
	rc, err := openRepo()
	if err != nil {
		return err
	}
	defer rc.st.Close()

	// Restore from the snapshot the working copy sits on (position),
	// or an explicit @ref.
	targetID, err := rc.st.Base()
	if err != nil {
		return err
	}
	if ref != nil {
		if targetID, err = resolveRef(rc.st, *ref); err != nil {
			return err
		}
	}
	if targetID == 0 {
		fmt.Fprintln(out, "no snapshots yet — run `vrs save` first")
		return nil
	}
	target, err := rc.st.SnapshotEntries(targetID)
	if err != nil {
		return err
	}
	scope, err := rc.resolveScope(path)
	if err != nil {
		return err
	}

	stats, err := snap.Status(rc.root, scope, target, rc.cache, rc.pol, rc.ig)
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
		fmt.Fprintf(out, "nothing to undo — %s matches #%d\n", scopeLabel(scope), targetID)
		return nil
	}

	// Capture-before-mutate: the divergent state is preserved as a hidden
	// snapshot, so undo can never lose work.
	capRes, err := snap.Capture(rc.root, rc.pol, rc.cache, rc.ig)
	if err != nil {
		return err
	}
	base, err := rc.st.Base()
	if err != nil {
		return err
	}
	capInfo, err := rc.st.Save(capRes, "", "capture", base)
	if err != nil {
		return err
	}

	// Materialize the target onto the scope: modified files are restored,
	// deleted files recreated, extra files moved to trash.
	trashBase := rc.trashDir()
	mat, err := snap.Materialize(rc.root, scope, target, rc.cache, rc.st, trashBase, rc.pol, rc.ig)
	if err != nil {
		return err
	}
	if err := rc.st.SyncCache(mat.Written, mat.Trashed); err != nil {
		return err
	}
	if err := rc.st.RedoPush(capInfo.ID, targetID, scope); err != nil {
		return err
	}
	if err := rc.st.PushOp("undo", map[string]any{
		"capture": capInfo.ID, "target": targetID, "scope": scope,
		"written": len(mat.Written), "trashed": len(mat.Trashed),
	}); err != nil {
		return err
	}

	fmt.Fprintf(out, "undid %d change(s) — %s restored to #%d\n",
		len(mat.Written)+len(mat.Trashed), scopeLabel(scope), targetID)
	if len(mat.Written) > 0 {
		fmt.Fprintf(out, "  %d file(s) written\n", len(mat.Written))
	}
	if len(mat.Trashed) > 0 {
		fmt.Fprintf(out, "  %d file(s) moved to %s\n", len(mat.Trashed),
			filepath.ToSlash(filepath.Join(".vrs", "trash", filepath.Base(trashBase))))
	}
	fmt.Fprintf(out, "previous state captured as #%d — `vrs redo` to reapply, `vrs log --all` to see\n", capInfo.ID)
	return nil
}
