package cli

import (
	"fmt"

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
