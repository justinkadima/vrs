package cli

import (
	"fmt"
	"io"

	"github.com/justinkadima/vrs/internal/remote"
	"github.com/justinkadima/vrs/internal/snap"
)

// checkpoint captures the current tree as a hidden checkpoint snapshot
// (kind='capture'): it never advances the position and never invalidates
// pending redos. Checkpointing is idempotent: if the tree already matches a
// recorded snapshot, no new snapshot is written and that snapshot's id is
// returned instead. Agents and scripts use this before risky steps; restore
// happens with `vrs goto @id`.
func checkpoint(rc *repoCtx, tag string) (id int64, created bool, changed int, err error) {
	base, err := rc.st.Base()
	if err != nil {
		return 0, false, 0, err
	}
	if base == 0 {
		return 0, false, 0, fmt.Errorf("no snapshots yet — run `vrs save` first")
	}
	res, err := snap.Capture(rc.root, rc.pol, rc.cache, rc.ig)
	if err != nil {
		return 0, false, 0, err
	}

	// Already recorded? Check the newest snapshot of any kind first (the
	// common case: a repeated checkpoint), then the working position.
	newest, ok, err := rc.st.LatestAny()
	if err != nil {
		return 0, false, 0, err
	}
	cands := []int64{base}
	if ok && newest != base {
		cands = []int64{newest, base}
	}
	for _, cand := range cands {
		prev, err := rc.st.SnapshotEntries(cand)
		if err != nil {
			return 0, false, 0, err
		}
		a, m, d := snap.Summarize(res.Entries, prev)
		if a+m+d == 0 {
			return cand, false, 0, nil
		}
		if cand == base {
			changed = a + m + d
		}
	}

	info, err := rc.st.Save(res, tag, "capture", base)
	if err != nil {
		return 0, false, 0, err
	}
	return info.ID, true, changed, nil
}

// scanProgress prints a heartbeat as the source walk considers files —
// SFTP walks are round-trip-bound and the most likely place to look
// stuck. Every 50 files keeps it informative without flooding.
func scanProgress(errW io.Writer) remote.ScanProgress {
	return func(scanned int, path string) {
		if scanned%50 == 0 {
			fmt.Fprintf(errW, "  scanned %d file(s) ...\n", scanned)
		}
	}
}

// fileProgress prints one line per transferred file, before the fetch:
// if a transfer hangs, the last line names the file that is stuck. For
// exports done is the file's position in the sorted snapshot.
func fileProgress(errW io.Writer) remote.FileProgress {
	return func(done, total int, path string, size int64) {
		fmt.Fprintf(errW, "  [%d/%d] %s \u00b7 %s\n", done, total, path, humanSize(size))
	}
}

// exportProgress reports the pass position through a snapshot: done is
// where the file sits in the sorted snapshot, total its size.
func exportProgress(errW io.Writer) remote.FileProgress {
	return func(done, total int, path string, size int64) {
		fmt.Fprintf(errW, "  [%d/%d] %s \u00b7 %s\n", done, total, path, humanSize(size))
	}
}

// humanSize renders byte counts for progress lines.
func humanSize(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GiB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MiB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KiB", float64(n)/(1<<10))
	default:
		return fmt.Sprintf("%d B", n)
	}
}
