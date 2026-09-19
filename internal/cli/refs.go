package cli

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/justinkadima/vrs/internal/store"
)

// refSpec is a parsed @ref: one of @N (absolute id), @-N (N saves back from
// the tip) or @<duration> (newest save at least that old).
type refSpec struct {
	kind string        // "id" | "back" | "time"
	n    int64         // for id/back
	dur  time.Duration // for time
	raw  string        // original argument, for error messages
}

// parseRef parses @N, @-N and @<duration> (@2h, @3d, @1h30m; units s m h d w).
func parseRef(arg string) (refSpec, error) {
	bad := func() (refSpec, error) {
		return refSpec{}, fmt.Errorf(
			"bad snapshot reference %q — use @N, @-N, or a duration like @2h (see `vrs log`)", arg)
	}
	if !strings.HasPrefix(arg, "@") || len(arg) == 1 {
		return bad()
	}
	s := arg[1:]
	if strings.HasPrefix(s, "-") {
		n, err := strconv.ParseInt(s[1:], 10, 64)
		if err != nil || n < 0 {
			return bad()
		}
		return refSpec{kind: "back", n: n, raw: arg}, nil
	}
	if n, err := strconv.ParseInt(s, 10, 64); err == nil {
		return refSpec{kind: "id", n: n, raw: arg}, nil
	}
	dur, err := parseDur(s)
	if err != nil {
		return bad()
	}
	return refSpec{kind: "time", dur: dur, raw: arg}, nil
}

var durUnits = map[byte]time.Duration{
	's': time.Second, 'm': time.Minute, 'h': time.Hour,
	'd': 24 * time.Hour, 'w': 7 * 24 * time.Hour,
}

// parseDur parses a compound duration like "2h", "1h30m", "45s".
func parseDur(s string) (time.Duration, error) {
	var total time.Duration
	for s != "" {
		i := 0
		for i < len(s) && s[i] >= '0' && s[i] <= '9' {
			i++
		}
		if i == 0 || i >= len(s) {
			return 0, fmt.Errorf("bad duration")
		}
		n, err := strconv.ParseInt(s[:i], 10, 64)
		if err != nil || n <= 0 {
			return 0, fmt.Errorf("bad duration")
		}
		unit, ok := durUnits[s[i]]
		if !ok {
			return 0, fmt.Errorf("bad duration unit")
		}
		total += time.Duration(n) * unit
		s = s[i+1:]
	}
	return total, nil
}

// resolveRef turns a parsed spec into a concrete snapshot id. Relative and
// time refs resolve against the visible timeline (saves only); absolute ids
// may address captures too (`vrs log --all`).
func resolveRef(st *store.Store, spec refSpec) (int64, error) {
	switch spec.kind {
	case "id":
		ok, err := st.SnapshotExists(spec.n)
		if err != nil {
			return 0, err
		}
		if !ok {
			return 0, fmt.Errorf("no snapshot #%d (see `vrs log --all`)", spec.n)
		}
		return spec.n, nil
	case "back":
		id, err := st.NthNewestSave(spec.n)
		if err != nil {
			return 0, fmt.Errorf("%s: %w", spec.raw, err)
		}
		return id, nil
	case "time":
		id, err := st.NewestSaveBefore(time.Now().Add(-spec.dur).UnixNano())
		if err != nil {
			return 0, fmt.Errorf("%s: %w", spec.raw, err)
		}
		return id, nil
	}
	panic("unreachable ref kind " + spec.kind)
}

// scanTargetArgs splits a command's positional arguments into an optional
// path and an optional @ref, in either order, at most one of each.
func scanTargetArgs(args []string) (path string, ref *refSpec, err error) {
	for _, a := range args {
		if strings.HasPrefix(a, "@") {
			if ref != nil {
				return "", nil, fmt.Errorf("multiple snapshot references given")
			}
			spec, e := parseRef(a)
			if e != nil {
				return "", nil, e
			}
			ref = &spec
		} else {
			if path != "" {
				return "", nil, fmt.Errorf("multiple paths given")
			}
			path = a
		}
	}
	return path, ref, nil
}
