// SPDX-License-Identifier: AGPL-3.0-or-later

package git

import (
	"context"
	"strconv"
	"strings"
)

// rebased returns the commits a rebase made, oldest first, and the
// rewrites that map the commits it replaced onto them (SP 4.4).
//
// The originals are the commits the old branch tip had and the new one
// doesn't; the rebased commits are the ones the new tip has and neither
// the old tip nor the rebase's starting point (the new base, or a commit
// it fast-forwarded through) has. git keeps no record of which replaced
// which once a rebase is over, apart from what it hands a post-rewrite
// hook, so matchRewrites works it out.
func (s *Scanner) rebased(ctx context.Context, o op) ([]*meta, []Rewrite, error) {
	orig, start, final := o.first.Old, o.first.New, o.last.New
	if orig == "" || zeroID(orig) {
		return nil, nil, nil
	}
	originals, err := s.revList(ctx, orig, final)
	if err != nil {
		return nil, nil, err
	}
	exclude := []string{orig}
	if start != "" && !zeroID(start) {
		exclude = append(exclude, start)
	}
	made, err := s.revList(ctx, final, exclude...)
	if err != nil {
		return nil, nil, err
	}
	metas, err := s.readMeta(ctx, append(append([]string(nil), originals...), made...))
	if err != nil {
		return nil, nil, err
	}
	pick := func(ids []string) []*meta {
		var ms []*meta
		for _, id := range ids {
			if m := metas[id]; m != nil {
				ms = append(ms, m)
			}
		}
		return ms
	}
	om, nm := pick(originals), pick(made)
	return nm, matchRewrites(om, nm, foldCounts(o.steps)), nil
}

// revList lists the commits reachable from tip and not from exclude,
// oldest first, at most maxRebase of the newest.
func (s *Scanner) revList(ctx context.Context, tip string, exclude ...string) ([]string, error) {
	args := []string{"rev-list", "--topo-order", "--reverse", "-n", strconv.Itoa(maxRebase), tip}
	if len(exclude) > 0 {
		args = append(append(args, "--not"), exclude...)
	}
	out, err := s.git.Run(ctx, s.loc.Dir, append(args, "--")...)
	if err != nil {
		return nil, err
	}
	return parseIDs(string(out))
}

// foldCounts reads a rebase's entries and returns, for each commit a
// squash or fixup step produced, how many original commits were folded
// into it. A fold's entry moves HEAD from the commit it folds into to the
// result, and an amend at an edit stop moves it on without a fold, so the
// count follows the chain to its last commit.
func foldCounts(steps []entry) map[string]int {
	counts := make(map[string]int)
	for i, e := range steps {
		if i == 0 {
			continue // the start
		}
		a := classify(e.Msg)
		switch {
		case a.step == stepRebaseStep && a.fold:
			counts[e.New] = counts[e.Old] + 1
		case a.step == stepAmend:
			counts[e.New] = counts[e.Old]
		}
	}
	return counts
}

// authorKey is what a rebase or an amend keeps of a commit: its author and
// author date. Both stay unless the command is told to reset them.
type authorKey struct {
	author string
	time   int64
	tz     string
}

func keyOf(m *meta) authorKey { return authorKey{m.author, m.AuthorTime, m.AuthorTZ} }

// matchRewrites pairs a rebase's original commits with the commits that
// replaced them, both oldest first.
//
//  1. An original and a rebased commit with the same author and author
//     date are the same commit: a pick, a reword, an edit, or the commit a
//     squash or fixup folded into. When several share both, the subject
//     decides, and then the order.
//  2. A rebased commit that folds took that many more originals. An
//     original whose subject is "fixup! X", "squash! X" or "amend! X"
//     folded into whatever the commit X became, found the way git's
//     --autosquash finds it, wherever the two sat.
//  3. The rest of the folds took the unmatched originals nearest after the
//     fold's own original, then the nearest before it, passing over
//     matched ones. This is how git's own list reads, where every commit
//     of a squash maps to the result.
//  4. Any original still unmatched was dropped and maps to nothing.
//
// Pairing a fold step with the original whose patch-id equals its diff's
// would also place a fold that a person moved by hand, but it would read
// the diffs, code included, into pairtime, which takes only counts from
// git.
func matchRewrites(originals, rebased []*meta, folds map[string]int) []Rewrite {
	target := make([]string, len(originals))
	used := make([]bool, len(rebased))
	// The same author, author date and subject first, for every original,
	// so a reworded commit can't take the copy of another commit with the
	// same author and second; then the same author and date alone.
	for _, sameSubject := range []bool{true, false} {
		for i, o := range originals {
			if target[i] != "" {
				continue
			}
			for j, r := range rebased {
				if !used[j] && keyOf(r) == keyOf(o) && (!sameSubject || r.subject == o.subject) {
					target[i] = r.SHA
					used[j] = true
					break
				}
			}
		}
	}

	matched := make([]bool, len(originals))
	for i := range target {
		matched[i] = target[i] != ""
	}
	left := make(map[string]int, len(folds))
	for k, v := range folds {
		left[k] = v
	}
	for i, o := range originals {
		if target[i] != "" {
			continue
		}
		if j := autosquashTarget(originals, i, o.subject); j >= 0 && target[j] != "" && left[target[j]] > 0 {
			target[i] = target[j]
			left[target[j]]--
		}
	}
	for i := range originals {
		if !matched[i] {
			continue
		}
		into := target[i]
		for j := i + 1; j < len(originals) && left[into] > 0; j++ {
			if target[j] == "" {
				target[j] = into
				left[into]--
			}
		}
		for j := i - 1; j >= 0 && left[into] > 0; j-- {
			if target[j] == "" {
				target[j] = into
				left[into]--
			}
		}
	}

	var out []Rewrite
	for i, o := range originals {
		if target[i] != "" && target[i] != o.SHA {
			out = append(out, Rewrite{OldSHA: o.SHA, NewSHA: target[i]})
		}
	}
	return out
}

// autosquashTarget returns the index of the original that the commit at i
// fixes up, squashes into or amends, by its subject, as git's --autosquash
// reads "fixup! X": an earlier commit whose subject is X, else one whose id
// starts with X, else the first whose subject starts with X. It returns -1
// for a subject without the prefix, or with no such commit.
func autosquashTarget(originals []*meta, i int, subject string) int {
	x, found := subject, false
	for {
		rest, ok := strings.CutPrefix(x, "fixup! ")
		if !ok {
			rest, ok = strings.CutPrefix(x, "squash! ")
		}
		if !ok {
			rest, ok = strings.CutPrefix(x, "amend! ")
		}
		if !ok {
			break
		}
		x, found = rest, true
	}
	if !found || x == "" {
		return -1
	}
	for j := 0; j < i; j++ {
		if originals[j].subject == x {
			return j
		}
	}
	if len(x) >= 4 && !strings.Contains(x, " ") {
		for j := 0; j < i; j++ {
			if strings.HasPrefix(originals[j].SHA, strings.ToLower(x)) {
				return j
			}
		}
	}
	for j := 0; j < i; j++ {
		if strings.HasPrefix(originals[j].subject, x) {
			return j
		}
	}
	return -1
}
