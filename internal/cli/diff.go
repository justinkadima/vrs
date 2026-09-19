package cli

import (
	"bytes"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/justinkadima/vrs/internal/diffx"
	"github.com/justinkadima/vrs/internal/snap"
)

func runDiff(args []string, out, errW io.Writer) error {
	fs := flag.NewFlagSet("diff", flag.ContinueOnError)
	fs.SetOutput(errW)
	if err := fs.Parse(args); err != nil {
		return ErrUsage
	}
	if fs.NArg() > 1 {
		fmt.Fprintln(errW, "usage: vrs diff [path]")
		return ErrUsage
	}
	rc, err := openRepo()
	if err != nil {
		return err
	}
	defer rc.st.Close()

	targetID, ok, err := rc.st.LatestSave()
	if err != nil {
		return err
	}
	if !ok {
		fmt.Fprintln(out, "no snapshots yet — run `vrs save` first")
		return nil
	}
	target, err := rc.st.SnapshotEntries(targetID)
	if err != nil {
		return err
	}

	// With a path argument: unified content diff for that file.
	if fs.NArg() == 1 {
		scope, err := rc.resolveScope(fs.Arg(0))
		if err != nil {
			return err
		}
		return fileDiff(rc, scope, target, targetID, out)
	}

	scope, err := rc.resolveScope("")
	if err != nil {
		return err
	}
	stats, err := snap.Status(rc.root, scope, target, rc.cache, rc.pol, rc.ig)
	if err != nil {
		return err
	}
	byStatus := map[string][]string{}
	for _, st := range stats {
		if st.Status != snap.StatusSame {
			byStatus[st.Status] = append(byStatus[st.Status], st.Path)
		}
	}
	if len(byStatus) == 0 {
		fmt.Fprintf(out, "no changes — matches #%d\n", targetID)
		return nil
	}
	order := []string{snap.StatusAdded, snap.StatusModified, snap.StatusDeleted}
	for _, st := range order {
		for _, p := range byStatus[st] {
			fmt.Fprintf(out, "%-9s %s\n", st, p)
		}
	}
	return nil
}

func fileDiff(rc *repoCtx, scope string, target map[string]snap.Entry, targetID int64, out io.Writer) error {
	stats, err := snap.Status(rc.root, scope, target, rc.cache, rc.pol, rc.ig)
	if err != nil {
		return err
	}
	st := stats[0]
	if st.Status == snap.StatusSame {
		fmt.Fprintf(out, "no changes in %s (matches #%d)\n", scope, targetID)
		return nil
	}

	var old, new []byte
	if st.Status != snap.StatusAdded {
		if old, err = rc.st.ReadVersion(target[scope].Hash); err != nil {
			return err
		}
	}
	if st.Status != snap.StatusDeleted {
		if new, err = os.ReadFile(filepath.Join(rc.root, filepath.FromSlash(scope))); err != nil {
			return err
		}
	}
	if isBinary(old) || isBinary(new) {
		fmt.Fprintf(out, "%s differs from #%d (binary file)\n", scope, targetID)
		return nil
	}

	switch st.Status {
	case snap.StatusModified:
		fmt.Fprintf(out, "--- #%d:%s\n", targetID, scope)
		fmt.Fprintf(out, "+++ %s\n", scope)
	case snap.StatusAdded:
		fmt.Fprintf(out, "+++ %s (not in #%d)\n", scope, targetID)
	case snap.StatusDeleted:
		fmt.Fprintf(out, "--- #%d:%s\n", targetID, scope)
		fmt.Fprintf(out, "+++ %s (deleted from working copy)\n", scope)
	}
	fmt.Fprint(out, diffx.Unified(diffx.LineDiff(string(old), string(new)), 3))
	return nil
}

func isBinary(b []byte) bool {
	n := len(b)
	if n > 8000 {
		n = 8000
	}
	return bytes.IndexByte(b[:n], 0) >= 0
}
