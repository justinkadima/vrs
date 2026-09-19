// Package diffx renders line-based unified diffs.
package diffx

import (
	"fmt"
	"strings"

	"github.com/sergi/go-diff/diffmatchpatch"
)

// LineOp is one line of a diff: ' ' context, '-' removed, '+' added.
type LineOp struct {
	Kind byte
	Text string
}

// LineDiff computes a line-level edit script from a to b.
// Trailing-newline differences are normalized away.
func LineDiff(a, b string) []LineOp {
	if a == b {
		return nil
	}
	dmp := diffmatchpatch.New()
	ca, cb, lines := dmp.DiffLinesToChars(a, b)
	diffs := dmp.DiffCharsToLines(dmp.DiffMain(ca, cb, false), lines)
	var ops []LineOp
	for _, d := range diffs {
		kind := byte(' ')
		switch d.Type {
		case diffmatchpatch.DiffDelete:
			kind = '-'
		case diffmatchpatch.DiffInsert:
			kind = '+'
		}
		for _, ln := range splitLines(d.Text) {
			ops = append(ops, LineOp{Kind: kind, Text: ln})
		}
	}
	return ops
}

func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(s, "\n"), "\n")
}

// Unified renders ops as unified hunks with n context lines, git style.
// It returns "" when ops contain no changes.
func Unified(ops []LineOp, n int) string {
	var changes []int
	for i, op := range ops {
		if op.Kind != ' ' {
			changes = append(changes, i)
		}
	}
	if len(changes) == 0 {
		return ""
	}

	// Group changes into hunks: changes within 2n+1 lines of each other merge.
	type span struct{ lo, hi int }
	var hunks []span
	cur := span{changes[0], changes[0]}
	for _, idx := range changes[1:] {
		if idx-cur.hi <= 2*n+1 {
			cur.hi = idx
		} else {
			hunks = append(hunks, cur)
			cur = span{idx, idx}
		}
	}
	hunks = append(hunks, cur)

	var b strings.Builder
	last := len(ops) - 1
	consumed, oldPos, newPos := 0, 0, 0
	for _, h := range hunks {
		lo, hi := h.lo-n, h.hi+n
		if lo < 0 {
			lo = 0
		}
		if hi > last {
			hi = last
		}
		for i := consumed; i < lo; i++ {
			if ops[i].Kind != '+' {
				oldPos++
			}
			if ops[i].Kind != '-' {
				newPos++
			}
		}
		oldCount, newCount := 0, 0
		for i := lo; i <= hi; i++ {
			if ops[i].Kind != '+' {
				oldCount++
			}
			if ops[i].Kind != '-' {
				newCount++
			}
		}
		fmt.Fprintf(&b, "@@ -%s +%s @@\n", rng(oldPos, oldCount), rng(newPos, newCount))
		for i := lo; i <= hi; i++ {
			b.WriteByte(ops[i].Kind)
			b.WriteString(ops[i].Text)
			b.WriteByte('\n')
		}
		consumed = hi + 1
		oldPos += oldCount
		newPos += newCount
	}
	return b.String()
}

// rng formats one side of a hunk header: "N" for a single line,
// "N,0" for an empty range, "N,count" otherwise.
func rng(pos, count int) string {
	switch count {
	case 0:
		return fmt.Sprintf("%d,0", pos)
	case 1:
		return fmt.Sprintf("%d", pos+1)
	default:
		return fmt.Sprintf("%d,%d", pos+1, count)
	}
}
