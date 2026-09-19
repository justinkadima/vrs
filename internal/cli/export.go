package cli

import (
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/justinkadima/vrs/internal/remote"
)

// runExport materializes a recorded snapshot onto a target directory —
// over SSH/SFTP ([user@]host:/abs/path) or locally. Never the untracked
// working tree: only recorded states ship. Incremental via the target
// manifest; --prune opts into exact-mirror semantics (extras trashed, never
// deleted).
func runExport(args []string, out, errW io.Writer) error {
	fs := flag.NewFlagSet("export", flag.ContinueOnError)
	fs.SetOutput(errW)
	prune := fs.Bool("prune", false, "move target files that are not in the snapshot to the target's trash")
	if err := fs.Parse(args); err != nil {
		return ErrUsage
	}
	if fs.NArg() > 2 {
		fmt.Fprintln(errW, "usage: vrs export <target> [@ref] [--prune]")
		return ErrUsage
	}
	path, ref, err := scanTargetArgs(fs.Args())
	if err != nil {
		return err
	}
	if path == "" {
		fmt.Fprintln(errW, "usage: vrs export <target> [@ref] [--prune]")
		return ErrUsage
	}

	rc, err := openRepo()
	if err != nil {
		return err
	}
	defer rc.st.Close()

	targetID, err := rc.baseOrRef(ref)
	if err != nil {
		return err
	}
	if targetID == 0 {
		return fmt.Errorf("no snapshots yet — run `vrs save` first")
	}
	entries, err := rc.st.SnapshotEntries(targetID)
	if err != nil {
		return err
	}
	for p := range entries {
		if remote.Reserved(p) {
			return fmt.Errorf("snapshot #%d contains the reserved name %q — vrs owns it at export targets", targetID, p)
		}
	}

	tgt, err := remote.ParseTarget(path)
	if err != nil {
		return err
	}

	var dst remote.Fs
	var dstRoot string
	switch tgt.Kind {
	case "local":
		if tgt.Path == rc.root || strings.HasPrefix(tgt.Path, rc.root+"/") {
			return fmt.Errorf("refusing to export inside the repository itself (%s) — the export manifest must not become tracked content", tgt.Path)
		}
		dst, dstRoot = remote.NewLocalFs(), tgt.Path
	case "ssh":
		sfs, err := remote.Dial(tgt)
		if err != nil {
			return fmt.Errorf("connect %s: %w", tgt.Host, err)
		}
		defer sfs.Close()
		dst, dstRoot = sfs, tgt.Path
	}

	res, err := remote.ExportTree(entries, rc.st, dst, dstRoot, *prune)
	if err != nil {
		return err
	}
	if err := rc.st.PushOp("export", map[string]any{
		"target": path, "snapshot": targetID,
		"written": len(res.Written), "pruned": len(res.Pruned),
	}); err != nil {
		return err
	}

	label := tgt.String()
	fmt.Fprintf(out, "exported #%d to %s — %d file(s) written", targetID, label, len(res.Written))
	if res.ModeOnly > 0 {
		fmt.Fprintf(out, ", %d mode-only", res.ModeOnly)
	}
	if res.Skipped > 0 {
		fmt.Fprintf(out, ", %d unchanged", res.Skipped)
	}
	fmt.Fprintln(out)
	if *prune && len(res.Pruned) > 0 {
		fmt.Fprintf(out, "  %d target file(s) moved to %s at the target (--prune)\n",
			len(res.Pruned), remote.TrashDirName)
	}
	return nil
}
