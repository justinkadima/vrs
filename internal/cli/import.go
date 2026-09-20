package cli

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/justinkadima/vrs/internal/ignore"
	"github.com/justinkadima/vrs/internal/paths"
	"github.com/justinkadima/vrs/internal/remote"
	"github.com/justinkadima/vrs/internal/snap"
	"github.com/justinkadima/vrs/internal/store"
)

// runImport overlays a source directory (SSH/SFTP or local) onto the
// working tree and snapshots the result. Downloads are filtered by the
// repo's ignore rules; .vrs is never touched on either side. Anything that
// changes is preceded by a capture (nothing is ever lost) and followed by an
// automatic save — imported state gets an anchor in history.
func runImport(args []string, out, errW io.Writer) error {
	fs := flag.NewFlagSet("import", flag.ContinueOnError)
	fs.SetOutput(errW)
	prune := fs.Bool("prune", false, "move local tracked files that are absent from the source to the repo trash")
	if err := fs.Parse(args); err != nil {
		return ErrUsage
	}
	if fs.NArg() > 2 {
		fmt.Fprintln(errW, "usage: vrs import <target> [--prune]")
		return ErrUsage
	}
	path, ref, err := scanTargetArgs(fs.Args())
	if err != nil {
		return err
	}
	if ref != nil {
		return fmt.Errorf("import takes no @ref — it always reads the source as it is now")
	}
	if path == "" {
		fmt.Fprintln(errW, "usage: vrs import <target> [--prune]")
		return ErrUsage
	}
	tgt, err := remote.ParseTarget(path)
	if err != nil {
		return err
	}

	// Find or auto-initialize the repository (import is an on-ramp).
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	root, ok := paths.FindRoot(cwd)
	var st *store.Store
	if !ok {
		st, root, err = initRepoHere(cwd)
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "initialized vrs repository in %s\n", root)
	} else {
		if st, err = store.Open(root); err != nil {
			return err
		}
	}
	defer st.Close()

	polyHex, err := st.Meta("chunker_poly")
	if err != nil {
		return fmt.Errorf("read chunker polynomial: %w", err)
	}
	pol, err := snap.ParsePolynomial(polyHex)
	if err != nil {
		return err
	}
	ig, err := ignore.Load(root)
	if err != nil {
		return err
	}
	cache, err := st.LoadCache()
	if err != nil {
		return err
	}

	var src remote.Fs
	var srcRoot string
	switch tgt.Kind {
	case "local":
		src, srcRoot = remote.NewLocalFs(), tgt.Path
	case "ssh":
		sfs, err := remote.Dial(tgt)
		if err != nil {
			return fmt.Errorf("connect %s: %w", tgt.Host, err)
		}
		defer sfs.Close()
		src, srcRoot = sfs, tgt.Path
	}
	local := remote.NewLocalFs()

	fmt.Fprintf(errW, "scanning %s ...\n", tgt.String())
	plan, err := remote.PlanImport(src, srcRoot, local, root, ig, *prune, scanProgress(errW))
	if err != nil {
		return fmt.Errorf("walk %s: %w", tgt.String(), err)
	}
	if len(plan.Files) == 0 && len(plan.Pruned) == 0 {
		fmt.Fprintf(out, "nothing to import — working tree already matches %s\n", tgt.String())
		return nil
	}

	// Capture-before-mutate: the pre-import state is preserved as a hidden
	// snapshot, so an import can never lose work. On a brand-new repository
	// there is nothing to preserve — the tree was empty.
	base, err := st.Base()
	if err != nil {
		return err
	}
	var capInfo *store.SaveInfo
	if base > 0 {
		capRes, err := snap.Capture(root, pol, cache, ig)
		if err != nil {
			return err
		}
		if capInfo, err = st.Save(capRes, "", "capture", base); err != nil {
			return err
		}
	}

	if len(plan.Files) > 0 {
		fmt.Fprintf(errW, "transferring %d file(s) from %s\n", len(plan.Files), tgt.String())
	}
	trashBase := filepath.Join(root, ".vrs", "trash", fmt.Sprintf("%d", time.Now().UnixNano()))
	if err := remote.ApplyImport(plan, src, srcRoot, local, root, trashBase, fileProgress(errW)); err != nil {
		return err
	}

	// The imported state becomes a real snapshot: provenance belongs in
	// the timeline. The source may have delivered its own .vrsignore (a
	// project's rules are content) — reload before saving so the adopted
	// rules govern this snapshot too, not just later saves.
	message := fmt.Sprintf("import from %s — %d file(s)", tgt.String(), len(plan.Files)+len(plan.Pruned))
	if ig, err = ignore.Load(root); err != nil {
		return err
	}
	res, err := snap.Capture(root, pol, cache, ig)
	if err != nil {
		return err
	}
	info, err := st.Save(res, message, "save", base)
	if err != nil {
		return err
	}
	detail := map[string]any{
		"source": tgt.String(), "files": len(plan.Files), "pruned": len(plan.Pruned),
		"snapshot": info.ID,
	}
	if capInfo != nil {
		detail["capture"] = capInfo.ID
	}
	if err := st.PushOp("import", detail); err != nil {
		return err
	}

	fmt.Fprintf(out, "imported %d file(s) from %s — snapshot #%d\n",
		len(plan.Files)+len(plan.Pruned), tgt.String(), info.ID)
	if len(plan.Pruned) > 0 {
		fmt.Fprintf(out, "  %d local file(s) moved to .vrs/trash (--prune)\n", len(plan.Pruned))
	}
	if capInfo != nil {
		fmt.Fprintf(out, "previous state captured as #%d — `vrs goto @%d` to recover\n", capInfo.ID, capInfo.ID)
	}
	return nil
}
