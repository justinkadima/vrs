package cli

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/justinkadima/vrs/internal/ignore"
	"github.com/justinkadima/vrs/internal/paths"
	"github.com/justinkadima/vrs/internal/snap"
	"github.com/justinkadima/vrs/internal/store"
)

func humanBytes(n int64) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MiB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KiB", float64(n)/(1<<10))
	default:
		return fmt.Sprintf("%d B", n)
	}
}

func runSave(args []string, out, errW io.Writer) error {
	fs := flag.NewFlagSet("save", flag.ContinueOnError)
	fs.SetOutput(errW)
	msgFlag := fs.String("m", "", "snapshot message")
	force := fs.Bool("force", false, "save even if nothing changed")
	if err := fs.Parse(args); err != nil {
		return ErrUsage
	}
	message := strings.TrimSpace(*msgFlag)
	if message == "" && fs.NArg() > 0 {
		message = strings.TrimSpace(strings.Join(fs.Args(), " "))
	}

	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	root, ok := paths.FindRoot(cwd)
	var st *store.Store
	if !ok {
		poly, err := snap.RandomPolynomialHex()
		if err != nil {
			return err
		}
		st, err = store.InitAt(cwd, poly)
		if err != nil {
			return err
		}
		root = cwd
		fmt.Fprintf(out, "initialized vrs repository in %s\n", root)
	} else {
		st, err = store.Open(root)
		if err != nil {
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
	base, err := st.Base()
	if err != nil {
		return err
	}
	var prev map[string]snap.Entry
	if base > 0 {
		if prev, err = st.SnapshotEntries(base); err != nil {
			return err
		}
	}
	// Saving while positioned on an older snapshot (after `vrs goto`)
	// starts a new line from that position — warn, never silently.
	forkFrom, oldTip := int64(0), int64(0)
	if base > 0 {
		tip, ok, err := st.LatestSave()
		if err != nil {
			return err
		}
		if ok && tip != base {
			forkFrom, oldTip = base, tip
		}
	}

	res, err := snap.Capture(root, pol, cache, ig)
	if err != nil {
		return err
	}
	added, modified, deleted := snap.Summarize(res.Entries, prev)
	if base > 0 && added+modified+deleted == 0 && !*force {
		fmt.Fprintf(out, "nothing to save — working tree matches #%d (vrs save --force to save anyway)\n", base)
		return nil
	}
	if message == "" {
		message = autoMessage(res.Entries, prev, deleted)
	}
	info, err := st.Save(res, message, "save", base)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "#%d saved — %d files: %d added, %d modified, %d deleted — %s new (%s compressed)\n",
		info.ID, len(res.Entries), added, modified, deleted,
		humanBytes(info.NewRaw), humanBytes(info.NewStored))
	if oldTip > 0 {
		fmt.Fprintf(out, "note: saved from a past position — #%d starts a new line from #%d (the tip was #%d)\n",
			info.ID, forkFrom, oldTip)
	}
	return nil
}

// autoMessage derives a message from the changed paths when none is given.
func autoMessage(entries []snap.Entry, prev map[string]snap.Entry, deleted int) string {
	if prev == nil {
		return fmt.Sprintf("initial snapshot (%d files)", len(entries))
	}
	changed := snap.ChangedPaths(entries, prev)
	if len(changed) == 0 {
		if deleted > 0 {
			return fmt.Sprintf("%d file(s) deleted", deleted)
		}
		return ""
	}
	var names []string
	for i, p := range changed {
		if i == 3 {
			names = append(names, fmt.Sprintf("+%d more", len(changed)-i))
			break
		}
		names = append(names, filepath.Base(p))
	}
	return strings.Join(names, ", ")
}
