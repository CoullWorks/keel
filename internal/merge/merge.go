// Package merge implements a line-based 3-way (diff3) merge with no dependencies,
// so `keel update` can evolve a project with the recipe while preserving the
// user's edits — Copier-style "template once, update everywhere".
package merge

import "strings"

// Markers used for unresolved conflicts (git-style).
const (
	markOurs   = "<<<<<<< yours"
	markSplit  = "======="
	markTheirs = ">>>>>>> keel"
)

// ThreeWay merges ours and theirs against their common base, line by line.
// Returns the merged text and the number of conflict regions (0 = clean merge).
// A change made on only one side is taken automatically; overlapping different
// changes are wrapped in conflict markers.
func ThreeWay(base, ours, theirs string) (string, int) {
	// Fast paths.
	if ours == theirs {
		return ours, 0
	}
	if base == ours {
		return theirs, 0
	}
	if base == theirs {
		return ours, 0
	}
	b, o, t := lines(base), lines(ours), lines(theirs)
	ma := lcsMatch(b, o) // base index -> matching ours index
	mb := lcsMatch(b, t) // base index -> matching theirs index

	type anchor struct{ o, a, c int } // base idx, ours idx, theirs idx
	anchors := []anchor{{-1, -1, -1}}
	for bi := 0; bi < len(b); bi++ {
		if ai, ok := ma[bi]; ok {
			if ci, ok2 := mb[bi]; ok2 {
				anchors = append(anchors, anchor{bi, ai, ci})
			}
		}
	}
	anchors = append(anchors, anchor{len(b), len(o), len(t)})

	var out []string
	conflicts := 0
	prev := anchors[0]
	for i := 1; i < len(anchors); i++ {
		cur := anchors[i]
		region, c := resolve(b[prev.o+1:cur.o], o[prev.a+1:cur.a], t[prev.c+1:cur.c])
		out = append(out, region...)
		conflicts += c
		if cur.o < len(b) { // emit the anchor line itself (common to all three)
			out = append(out, b[cur.o])
		}
		prev = cur
	}
	return join(out, base), conflicts
}

// resolve merges one region (the lines strictly between two shared anchors).
func resolve(base, ours, theirs []string) ([]string, int) {
	switch {
	case eq(ours, base): // only theirs changed here
		return theirs, 0
	case eq(theirs, base): // only ours changed here
		return ours, 0
	case eq(ours, theirs): // both made the same change
		return ours, 0
	}
	// overlapping, different changes -> conflict
	out := []string{markOurs}
	out = append(out, ours...)
	out = append(out, markSplit)
	out = append(out, theirs...)
	out = append(out, markTheirs)
	return out, 1
}

// lcsMatch returns, for each index in a, the index in b it pairs with in a
// longest common subsequence (only for lines on the LCS).
func lcsMatch(a, b []string) map[int]int {
	n, m := len(a), len(b)
	// DP table of LCS lengths.
	dp := make([][]int, n+1)
	for i := range dp {
		dp[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if a[i] == b[j] {
				dp[i][j] = dp[i+1][j+1] + 1
			} else if dp[i+1][j] >= dp[i][j+1] {
				dp[i][j] = dp[i+1][j]
			} else {
				dp[i][j] = dp[i][j+1]
			}
		}
	}
	match := map[int]int{}
	i, j := 0, 0
	for i < n && j < m {
		if a[i] == b[j] {
			match[i] = j
			i++
			j++
		} else if dp[i+1][j] >= dp[i][j+1] {
			i++
		} else {
			j++
		}
	}
	return match
}

func eq(x, y []string) bool {
	if len(x) != len(y) {
		return false
	}
	for i := range x {
		if x[i] != y[i] {
			return false
		}
	}
	return true
}

// lines splits into lines, dropping a single trailing newline so a file ending
// in "\n" doesn't yield a phantom empty final line.
func lines(s string) []string {
	if s == "" {
		return nil
	}
	s = strings.TrimSuffix(s, "\n")
	return strings.Split(s, "\n")
}

// join reassembles lines, restoring a trailing newline if the base had one.
func join(ls []string, base string) string {
	out := strings.Join(ls, "\n")
	if strings.HasSuffix(base, "\n") && out != "" {
		out += "\n"
	}
	return out
}
