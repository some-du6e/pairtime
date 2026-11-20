// SPDX-License-Identifier: AGPL-3.0-or-later

package git

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/pkg/event"
)

func TestClassify(t *testing.T) {
	cases := []struct {
		name string
		msg  string
		want action
	}{
		{"a commit is a commit", "commit: Fix wall clipping", action{step: stepCommit}},
		{"a repo's first commit is a commit", "commit (initial): first", action{step: stepCommit}},
		{"an amend is an amend", "commit (amend): Fix wall clipping", action{step: stepAmend}},
		{"a merge finished by a commit after conflicts is a merge", "commit (merge): Merge branch 'side'", action{step: stepMerge}},
		{"a cherry-pick finished by a commit is a commit", "commit (cherry-pick): x", action{step: stepCommit}},
		{"a merge is a merge", "merge side: Merge made by the 'ort' strategy.", action{step: stepMerge}},
		{"a fast-forward merge is a merge", "merge origin/main: Fast-forward", action{step: stepMerge}},
		{"a pull is a merge", "pull: Fast-forward", action{step: stepMerge}},
		{"a rebase's start is its start", "rebase (start): checkout main", action{step: stepRebaseStart}},
		{"an older interactive rebase's start is its start", "rebase -i (start): checkout HEAD~3", action{step: stepRebaseStart}},
		{"the apply backend's start is a start", "rebase: checkout main", action{step: stepRebaseStart}},
		{"a pick is a rebase step", "rebase (pick): Fix wall clipping", action{step: stepRebaseStep}},
		{"a squash folds", "rebase (squash): # This is a combination of 2 commits.", action{step: stepRebaseStep, fold: true}},
		{"a fixup folds", "rebase -i (fixup): Fix wall clipping", action{step: stepRebaseStep, fold: true}},
		{"a pull --rebase pick is a rebase step", "pull --rebase (pick): x", action{step: stepRebaseStep, pull: true}},
		{"a rebase's finish is its finish", "rebase (finish): returning to refs/heads/main", action{step: stepRebaseFinish}},
		{"the apply backend's finish is a finish", "rebase finished: returning to refs/heads/main", action{step: stepRebaseFinish}},
		{"a pull --rebase finish is a finish", "pull --rebase (finish): returning to refs/heads/main", action{step: stepRebaseFinish, pull: true}},
		// git pull puts its own arguments where the command goes, so the
		// word "rebase" can be missing from a pull's rebase, and present in
		// a merge's options or branch names.
		{"a plain pull's rebase under pull.rebase=true starts with a step and no word rebase", "pull (start): checkout 3f8b3efe2f0266c823fbd6cf0f4e2053294734db", action{step: stepRebaseStart, pull: true}},
		{"a plain pull's rebase picks with no word rebase", "pull -q (pick): local", action{step: stepRebaseStep, pull: true}},
		{"a plain pull's rebase finishes with no word rebase", "pull -q (finish): returning to refs/heads/main", action{step: stepRebaseFinish, pull: true}},
		{"a pull -r with a remote and branch is still a rebase", "pull -q -r origin main (squash): # This is a combination of 2 commits.", action{step: stepRebaseStep, fold: true, pull: true}},
		{"a pull --rebase that fast-forwards is a merge", "pull -q --rebase: Fast-forward", action{step: stepMerge}},
		{"a pull --no-rebase that merges is a merge", "pull -q --no-rebase --no-edit: Merge made by the 'ort' strategy.", action{step: stepMerge}},
		{"a merge of a branch named for a rebase is a merge", "merge fix-rebase-bug: Merge made by the 'ort' strategy.", action{step: stepMerge}},
		{"a fast-forward to a branch named for a rebase is a merge", "merge rebase-cleanup: Fast-forward", action{step: stepMerge}},
		{"the apply backend's start under a pull before git 2.26 is a start", "pull --rebase: checkout 26600cacc5ba5d3ed0c1cb2d154d46f444c03ea8", action{step: stepRebaseStart, pull: true}},
		{"a rebase's abort is an abort", "rebase (abort): returning to refs/heads/main", action{step: stepRebaseAbort}},
		{"a revert is a revert", `revert: Revert "x"`, action{step: stepRevert}},
		{"a checkout is a checkout", "checkout: moving from main to side", action{step: stepCheckout}},
		{"a reset is a reset", "reset: moving to HEAD~1", action{step: stepReset}},
		{"am is a commit", "am: Apply a patch", action{step: stepCommit}},
		{"a clone's first entry is nothing to report", "clone: from https://example.com/x.git", action{step: stepNone}},
		{"a message the adapter doesn't know is unknown", "jj: describe commit 1234", action{step: stepUnknown}},
		{"an empty message is unknown", "", action{step: stepUnknown}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := classify(tc.msg); got != tc.want {
				t.Errorf("classify(%q) = %+v, want %+v", tc.msg, got, tc.want)
			}
		})
	}
}

func TestParseReflog(t *testing.T) {
	a, b := strings.Repeat("a", 40), strings.Repeat("b", 40)
	cases := []struct {
		name    string
		out     string
		want    []entry
		wantErr bool
	}{
		{
			name: "records split on the record separator, each with git's second and offset",
			out:  a + "\x00HEAD@{1795134483 -0600}\x00commit: two\x1e\n" + b + "\x00HEAD@{1795134400 +0000}\x00commit (initial): one\x1e\n",
			want: []entry{{New: a, Time: 1795134483, TZ: "-0600", Msg: "commit: two"}, {New: b, Time: 1795134400, TZ: "+0000", Msg: "commit (initial): one"}},
		},
		{name: "no output is no entries", out: ""},
		{name: "a record without three fields is refused", out: a + "\x00HEAD@{1 +0000}\x1e", wantErr: true},
		{name: "a selector without a time is refused", out: a + "\x00HEAD@{0}x\x00commit: x\x1e", wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseReflog(tc.out)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err %v, want error %v", err, tc.wantErr)
			}
			if !slices.Equal(got, tc.want) {
				t.Errorf("got %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestCursor(t *testing.T) {
	e1 := entry{New: strings.Repeat("1", 40), Time: 10, Msg: "commit: one"}
	e2 := entry{New: strings.Repeat("2", 40), Time: 20, Msg: "commit: two"}
	e3 := entry{New: strings.Repeat("3", 40), Time: 30, Msg: "commit: three"}
	window := []entry{e3, e2, e1} // newest first, as git log -g prints
	cases := []struct {
		name      string
		cur       Cursor
		want      []entry
		wantFound bool
	}{
		{"the entries after the cursor come back oldest first", cursorAt(e1), []entry{e2, e3}, true},
		{"a cursor at the newest entry leaves nothing", cursorAt(e3), []entry{}, true},
		{"a cursor whose entry is gone isn't found", cursorAt(entry{New: strings.Repeat("9", 40), Time: 15, Msg: "x"}), nil, false},
		{"the same commit and second with another message is another entry", Cursor{New: e1.New, Time: e1.Time, Sum: entry{Msg: "checkout: x"}.sum(), Set: true}, nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, found := tc.cur.after(window)
			if found != tc.wantFound || len(got) != len(tc.want) || (len(got) > 0 && !slices.Equal(got, tc.want)) {
				t.Errorf("after = %+v, %v; want %+v, %v", got, found, tc.want, tc.wantFound)
			}
		})
	}
	t.Run("when the cursor's entry is gone, entries after its second are new", func(t *testing.T) {
		got := Cursor{Time: 15, Set: true}.since(window)
		if !slices.Equal(got, []entry{e2, e3}) {
			t.Errorf("since = %+v, want e2 and e3", got)
		}
	})
}

func TestPlan(t *testing.T) {
	h := func(c byte) string { return strings.Repeat(string(c), 40) }
	e := func(old, new byte, msg string) entry { return entry{Old: h(old), New: h(new), Msg: msg} }
	cases := []struct {
		name     string
		entries  []entry
		inRebase bool
		wantOps  []event.GitOp
		wantUsed int
	}{
		{
			name:     "each commit, checkout and reset is an operation",
			entries:  []entry{e('a', 'b', "commit: x"), e('b', 'c', "checkout: moving from a to b"), e('c', 'b', "reset: moving to HEAD~1")},
			wantOps:  []event.GitOp{event.GitCommit, event.GitCheckout, event.GitReset},
			wantUsed: 3,
		},
		{
			name:     "an entry that doesn't move HEAD, like a stash's reset, is no operation",
			entries:  []entry{e('a', 'a', "reset: moving to HEAD")},
			wantUsed: 1,
		},
		{
			name:     "a rebase from start to finish is one operation",
			entries:  []entry{e('d', 'a', "rebase (start): checkout main"), e('a', 'e', "rebase (pick): x"), e('e', 'f', "rebase (squash): y"), e('f', 'f', "rebase (finish): returning to refs/heads/main")},
			wantOps:  []event.GitOp{event.GitRebase},
			wantUsed: 4,
		},
		{
			name:     "an aborted rebase is no operation",
			entries:  []entry{e('d', 'a', "rebase (start): checkout main"), e('a', 'e', "rebase (pick): x"), e('e', 'd', "rebase (abort): returning to refs/heads/main")},
			wantUsed: 3,
		},
		{
			name:     "a rebase still in progress waits, with what came before it handled",
			entries:  []entry{e('a', 'b', "commit: x"), e('b', 'c', "rebase (start): checkout main"), e('c', 'd', "rebase (edit): y")},
			inRebase: true,
			wantOps:  []event.GitOp{event.GitCommit},
			wantUsed: 1,
		},
		{
			name:     "a rebase with no end that is no longer in progress was quit, and ends where it stopped",
			entries:  []entry{e('b', 'c', "rebase (start): checkout main"), e('c', 'd', "rebase (pick): y"), e('d', 'e', "checkout: moving from x to y")},
			wantOps:  []event.GitOp{event.GitRebase, event.GitCheckout},
			wantUsed: 3,
		},
		{
			name:     "an amend at an edit stop is part of the rebase",
			entries:  []entry{e('b', 'c', "rebase (start): checkout main"), e('c', 'd', "commit (amend): y"), e('d', 'e', "rebase (continue): z"), e('e', 'e', "rebase (finish): returning to refs/heads/main")},
			wantOps:  []event.GitOp{event.GitRebase},
			wantUsed: 4,
		},
		{
			name:     "a pull's rebase from start to finish is one rebase, though no entry says rebase",
			entries:  []entry{e('b', 'c', "pull (start): checkout cccc"), e('c', 'd', "pull (pick): x"), e('d', 'd', "pull (finish): returning to refs/heads/main")},
			wantOps:  []event.GitOp{event.GitRebase},
			wantUsed: 3,
		},
		{
			name:     "the apply backend's pull --rebase before git 2.26 is one rebase, its steps under the pull's own name",
			entries:  []entry{e('b', 'c', "pull --rebase: checkout cccc"), e('c', 'd', "pull --rebase: x"), e('d', 'e', "pull --rebase: y"), e('e', 'e', "rebase finished: returning to refs/heads/main")},
			wantOps:  []event.GitOp{event.GitRebase},
			wantUsed: 4,
		},
		{
			name:     "a merge of a branch named for a rebase is a merge, not a stray rebase step",
			entries:  []entry{e('a', 'b', "merge fix-rebase-bug: Merge made by the 'ort' strategy."), e('b', 'c', "pull -q --no-rebase: Merge made by the 'ort' strategy.")},
			wantOps:  []event.GitOp{event.GitMerge, event.GitMerge},
			wantUsed: 2,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ops, used := plan(tc.entries, tc.inRebase)
			var kinds []event.GitOp
			for _, o := range ops {
				kinds = append(kinds, o.kind)
			}
			if !slices.Equal(kinds, tc.wantOps) || used != tc.wantUsed {
				t.Errorf("plan = %v using %d entries, want %v using %d", kinds, used, tc.wantOps, tc.wantUsed)
			}
		})
	}
}

func TestParseMeta(t *testing.T) {
	sha, parent := strings.Repeat("a", 40), strings.Repeat("b", 40)
	rec := func(name, email, trailers string) string {
		return sha + "\x00" + parent + "\x00" + "1795000000 -0600\x00" + "1795000060 +0100\x00" + name + "\x00" + email + "\x00subject\x00" + trailers + "\x00\x1e\n"
	}
	cases := []struct {
		name      string
		text      string
		wantAgent string
	}{
		{"Claude Code's co-author trailer names claude", rec("Teen", "t@example.com", "Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>\n"), "claude"},
		{"Aider's co-author trailer names aider, whatever its model", rec("Teen", "t@example.com", "Co-authored-by: aider (gpt-5) <aider@aider.chat>\n"), "aider"},
		{"Replit's trailer names replit", rec("Teen", "t@example.com", "Replit-Commit-Author: Agent\n"), "replit"},
		{"Lovable's bot author names lovable", rec("gpt-engineer-app[bot]", "159125892+gpt-engineer-app[bot]@users.noreply.github.com", ""), "lovable"},
		{"a person co-author names no agent", rec("Teen", "t@example.com", "Co-authored-by: Sam <sam@example.com>\nSigned-off-by: Teen <t@example.com>\n"), ""},
		{"a claimed name with another address names no agent", rec("Teen", "t@example.com", "Co-authored-by: Claude <claude@example.com>\n"), ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ms, err := parseMeta(tc.text)
			if err != nil {
				t.Fatal(err)
			}
			if len(ms) != 1 {
				t.Fatalf("got %d commits, want 1", len(ms))
			}
			m := ms[0]
			if m.agent != tc.wantAgent {
				t.Errorf("agent %q, want %q", m.agent, tc.wantAgent)
			}
			if m.SHA != sha || !slices.Equal(m.Parents, []string{parent}) || m.AuthorTime != 1795000000 || m.AuthorTZ != "-0600" || m.CommitterTime != 1795000060 || m.CommitterTZ != "+0100" {
				t.Errorf("parsed %+v", m)
			}
		})
	}
}

func TestParseAINote(t *testing.T) {
	cases := []struct {
		name string
		note string
		want *AINote
	}{
		{
			name: "sessions, legacy prompts and humans give tools and line counts",
			note: "src/main.rs\n  s_c9883b05a2487d::t_9f8e7d6c5b4a32 1-10\n  h_31dce776f88375 15-20\n\"src/a b.rs\"\n  abcd1234abcd1234 1,4-6,9\n---\n" +
				`{"schema_version":"authorship/3.0.0","base_commit_sha":"x","prompts":{"abcd1234abcd1234":{"agent_id":{"tool":"Cursor","id":"1","model":"m"},"total_additions":1,"total_deletions":0,"accepted_lines":1,"overriden_lines":0}},"sessions":{"s_c9883b05a2487d":{"agent_id":{"tool":"claude","id":"2","model":"m"}}},"humans":{"h_31dce776f88375":{"author":"Teen <t@example.com>"}}}`,
			want: &AINote{Schema: "authorship/3.0.0", Tools: []string{"claude", "cursor"}, AILines: 15, HumanLines: 6},
		},
		{name: "no note is nil", note: "", want: nil},
		{name: "a note without the divider isn't git-ai's", note: "just a note\n", want: nil},
		{name: "a note whose JSON isn't an authorship log isn't git-ai's", note: "x\n---\n{\"schema_version\":\"other/1\"}", want: nil},
		{name: "a tool name that isn't label-shaped is dropped", note: "---\n" + `{"schema_version":"authorship/3.0.0","prompts":{},"sessions":{"s_1":{"agent_id":{"tool":"my tool/../x"}}}}`, want: &AINote{Schema: "authorship/3.0.0"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := parseAINote(tc.note)
			if (got == nil) != (tc.want == nil) {
				t.Fatalf("got %+v, want %+v", got, tc.want)
			}
			if got != nil && (got.Schema != tc.want.Schema || !slices.Equal(got.Tools, tc.want.Tools) || got.AILines != tc.want.AILines || got.HumanLines != tc.want.HumanLines) {
				t.Errorf("got %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestParseStats(t *testing.T) {
	a, b, c := strings.Repeat("a", 40), strings.Repeat("b", 40), strings.Repeat("c", 40)
	cases := []struct {
		name string
		out  string
		want map[string][3]int
	}{
		{
			name: "each commit's shortstat follows its id, and a commit that changes nothing has no line",
			out:  a + "\n 3 files changed, 10 insertions(+), 1 deletion(-)\n" + b + "\n 1 file changed, 1 insertion(+)\n" + c + "\n 2 files changed, 4 deletions(-)\n",
			want: map[string][3]int{a: {3, 10, 1}, b: {1, 1, 0}, c: {2, 0, 4}},
		},
		{name: "CRLF line ends parse the same", out: a + "\r\n 1 file changed, 2 insertions(+)\r\n", want: map[string][3]int{a: {1, 2, 0}}},
		{name: "no output is no stats", out: "", want: map[string][3]int{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := parseStats(tc.out)
			if len(got) != len(tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
			for k, v := range tc.want {
				if got[k] != v {
					t.Errorf("%s: got %v, want %v", k[:7], got[k], v)
				}
			}
		})
	}
}

func TestMatchRewrites(t *testing.T) {
	m := func(sha, author string, at int64, subject string) *meta {
		return &meta{SHA: sha, author: author, AuthorTime: at, AuthorTZ: "+0000", subject: subject}
	}
	o1, o2, o3, o4 := m("o1", "a", 100, "one"), m("o2", "a", 200, "two"), m("o3", "a", 300, "three"), m("o4", "a", 400, "four")
	cases := []struct {
		name      string
		originals []*meta
		rebased   []*meta
		folds     map[string]int
		want      []Rewrite
	}{
		{
			name:      "picked commits keep their author date and map one to one",
			originals: []*meta{o1, o2},
			rebased:   []*meta{m("n1", "a", 100, "one"), m("n2", "a", 200, "two")},
			want:      []Rewrite{{"o1", "n1"}, {"o2", "n2"}},
		},
		{
			name:      "a reworded commit still maps by its author date",
			originals: []*meta{o1},
			rebased:   []*meta{m("n1", "a", 100, "one, reworded")},
			want:      []Rewrite{{"o1", "n1"}},
		},
		{
			name:      "squashed and fixed-up commits map to the commit they folded into, and a dropped one maps nowhere",
			originals: []*meta{o1, o2, o3, o4},
			rebased:   []*meta{m("n", "a", 100, "one")},
			folds:     map[string]int{"n": 2},
			want:      []Rewrite{{"o1", "n"}, {"o2", "n"}, {"o3", "n"}},
		},
		{
			name:      "a dropped commit before a fold doesn't take the fold",
			originals: []*meta{o1, o2, o3, o4},
			rebased:   []*meta{m("n1", "a", 100, "one"), m("n3", "a", 300, "three")},
			folds:     map[string]int{"n3": 1},
			want:      []Rewrite{{"o1", "n1"}, {"o3", "n3"}, {"o4", "n3"}},
		},
		{
			name:      "a commit squashed into a later one after a reorder maps to it",
			originals: []*meta{o1, o2},
			rebased:   []*meta{m("n", "a", 200, "two")},
			folds:     map[string]int{"n": 1},
			want:      []Rewrite{{"o1", "n"}, {"o2", "n"}},
		},
		{
			name:      "commits with the same author second are told apart by subject",
			originals: []*meta{m("x", "a", 100, "first"), m("y", "a", 100, "second")},
			rebased:   []*meta{m("ny", "a", 100, "second"), m("nx", "a", 100, "first")},
			want:      []Rewrite{{"x", "nx"}, {"y", "ny"}},
		},
		{
			name:      "a commit with a reset author date matches nothing",
			originals: []*meta{o1},
			rebased:   []*meta{m("n1", "a", 999, "one")},
			want:      nil,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := matchRewrites(tc.originals, tc.rebased, tc.folds)
			if !slices.Equal(got, tc.want) {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestFoldCounts(t *testing.T) {
	h := func(c byte) string { return strings.Repeat(string(c), 40) }
	cases := []struct {
		name  string
		steps []entry
		want  map[string]int
	}{
		{
			name: "a squash then a fixup fold two commits into the last result",
			steps: []entry{
				{Old: h('d'), New: h('a'), Msg: "rebase (start): checkout x"},
				{Old: h('a'), New: h('b'), Msg: "rebase (squash): # This is a combination of 2 commits."},
				{Old: h('b'), New: h('c'), Msg: "rebase (fixup): one"},
			},
			want: map[string]int{h('b'): 1, h('c'): 2},
		},
		{
			name: "an amend at an edit stop carries the count on without adding to it",
			steps: []entry{
				{Old: h('d'), New: h('a'), Msg: "rebase (start): checkout x"},
				{Old: h('a'), New: h('b'), Msg: "rebase (fixup): one"},
				{Old: h('b'), New: h('c'), Msg: "commit (amend): one"},
			},
			want: map[string]int{h('b'): 1, h('c'): 1},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := foldCounts(tc.steps)
			if len(got) != len(tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
			for k, v := range tc.want {
				if got[k] != v {
					t.Errorf("got %v, want %v", got, tc.want)
				}
			}
		})
	}
}

func TestParseGitRuns(t *testing.T) {
	cwd := filepath.FromSlash("/home/teen/game")
	at := time.Unix(1795000000, 0)
	cases := []struct {
		name    string
		line    string
		wantDir []string
		wantSub []event.GitOp // the first op of each run
	}{
		{"a plain commit runs in the session's folder", `git commit -m "fix: wall clipping"`, []string{cwd}, []event.GitOp{event.GitCommit}},
		{"cd before git moves the folder", `cd engine && git rebase main`, []string{filepath.Join(cwd, "engine")}, []event.GitOp{event.GitRebase}},
		{"-C moves the folder for that git only", `git -C ../lib reset --hard HEAD~1; git checkout main`, []string{filepath.Join(filepath.Dir(cwd), "lib"), cwd}, []event.GitOp{event.GitReset, event.GitCheckout}},
		{"environment assignments and -c options before the subcommand are skipped", `GIT_EDITOR=true git -c core.editor=true rebase --continue`, []string{cwd}, []event.GitOp{event.GitRebase}},
		{"a quoted separator doesn't split the command", `git commit -m "a && b; c" && git push`, []string{cwd}, []event.GitOp{event.GitCommit}},
		{"git commands that don't move HEAD aren't kept", `git status && git diff && git log -1`, nil, nil},
		{"a command that isn't git isn't kept", `npm test && echo git commit`, nil, nil},
		{"an absolute git path counts as git", `/usr/bin/git merge --no-edit side`, []string{cwd}, []event.GitOp{event.GitMerge}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			runs := parseGitRuns(at, cwd, tc.line)
			var dirs []string
			var ops []event.GitOp
			for _, r := range runs {
				dirs = append(dirs, r.dir)
				ops = append(ops, r.ops[0])
				if !r.t.Equal(at) {
					t.Errorf("run time %v, want %v", r.t, at)
				}
			}
			if !slices.Equal(dirs, tc.wantDir) || !slices.Equal(ops, tc.wantSub) {
				t.Errorf("runs in %v doing %v, want %v doing %v", dirs, ops, tc.wantDir, tc.wantSub)
			}
		})
	}
}

func TestRunMatches(t *testing.T) {
	common := filepath.FromSlash("/home/teen/game/.git")
	other := filepath.FromSlash("/home/teen/other/.git")
	at := func(sec int64, ms int64) time.Time { return time.Unix(sec, ms*int64(time.Millisecond)) }
	commit := []event.GitOp{event.GitCommit, event.GitAmend}
	cases := []struct {
		name      string
		run       gitRun
		runCommon string
		op        event.GitOp
		start     int64
		end       int64
		want      bool
	}{
		{"an agent commit in the same repo in the same second is the agent's", gitRun{t: at(100, 500), ops: commit}, common, event.GitCommit, 100, 100, true},
		{"one that ends 2 s after git's second is still the agent's", gitRun{t: at(102, 900), ops: commit}, common, event.GitCommit, 100, 100, true},
		{"one 3 s after git's second isn't", gitRun{t: at(103, 100), ops: commit}, common, event.GitCommit, 100, 100, false},
		{"one 2 s before the operation began is the agent's", gitRun{t: at(98, 0), ops: commit}, common, event.GitCommit, 100, 100, true},
		{"one that began 3 s before isn't", gitRun{t: at(97, 0), ops: commit}, common, event.GitCommit, 100, 100, false},
		{"a run in another repo isn't", gitRun{t: at(100, 0), ops: commit}, other, event.GitCommit, 100, 100, false},
		{"a run of another git command isn't", gitRun{t: at(100, 0), ops: []event.GitOp{event.GitCheckout}}, common, event.GitCommit, 100, 100, false},
		{"a run whose folder didn't resolve isn't", gitRun{t: at(100, 0), ops: commit}, "", event.GitCommit, 100, 100, false},
		{"a long rebase matches at its end", gitRun{t: at(110, 200), ops: []event.GitOp{event.GitRebase}}, common, event.GitRebase, 100, 109, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := runMatches(tc.run, tc.runCommon, common, tc.op, tc.start, tc.end); got != tc.want {
				t.Errorf("runMatches = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestParseVersion(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want Version
		ok   bool
	}{
		{"a plain release", "git version 2.47.1\n", Version{2, 47, 1}, true},
		{"Apple's build", "git version 2.39.5 (Apple Git-154)", Version{2, 39, 5}, true},
		{"Git for Windows", "git version 2.51.0.windows.1", Version{2, 51, 0}, true},
		{"a release candidate counts as its release", "git version 2.52.0-rc1", Version{2, 52, 0}, true},
		{"output that isn't git's is refused", "hello", Version{}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseVersion(tc.in)
			if tc.ok && (err != nil || got != tc.want) {
				t.Errorf("ParseVersion(%q) = %v, %v; want %v", tc.in, got, err, tc.want)
			}
			if !tc.ok && err == nil {
				t.Errorf("ParseVersion(%q) = %v, want an error", tc.in, got)
			}
		})
	}
	if !(Version{2, 14, 9}).Less(MinVersion) || MinVersion.Less(Version{2, 15, 0}) {
		t.Errorf("MinVersion %v compares wrongly", MinVersion)
	}
}

func TestEnviron(t *testing.T) {
	cases := []struct {
		name    string
		base    []string
		gone    []string
		present []string
	}{
		{
			name:    "variables that point git at another repo are removed, and the adapter's own settings added",
			base:    []string{"PATH=/bin", "GIT_DIR=/elsewhere/.git", "git_work_tree=/x", "GIT_CONFIG_KEY_0=core.hooksPath", "GIT_CONFIG_VALUE_0=/tmp", "GIT_INDEX_FILE=/i"},
			gone:    []string{"GIT_DIR=/elsewhere/.git", "git_work_tree=/x", "GIT_CONFIG_KEY_0=core.hooksPath", "GIT_CONFIG_VALUE_0=/tmp", "GIT_INDEX_FILE=/i"},
			present: []string{"PATH=/bin", "GIT_OPTIONAL_LOCKS=0", "GIT_TERMINAL_PROMPT=0", "LC_ALL=C"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := environ(tc.base, nil)
			for _, kv := range tc.gone {
				if slices.Contains(env, kv) {
					t.Errorf("%s survived", kv)
				}
			}
			for _, kv := range tc.present {
				if !slices.Contains(env, kv) {
					t.Errorf("%s is missing", kv)
				}
			}
		})
	}
}

func TestParseRevParse(t *testing.T) {
	// git prints absolute paths with forward slashes, with a drive on
	// Windows.
	root := ""
	if filepath.Separator == '\\' {
		root = "C:"
	}
	p := func(s string) string { return filepath.FromSlash(root + s) }
	abs := p("/home/teen/game/src")
	cases := []struct {
		name       string
		out        string
		wantCommon string
		wantTop    string
		wantSuper  string
		ok         bool
	}{
		{"a relative common dir is relative to the folder", root + "/home/teen/game/.git\n../.git\nfalse\ntrue\nfalse\n" + root + "/home/teen/game\n", p("/home/teen/game/.git"), p("/home/teen/game"), "", true},
		{"a submodule names its superproject last", root + "/home/teen/game/.git/modules/lib\n" + root + "/home/teen/game/.git/modules/lib\nfalse\ntrue\nfalse\n" + root + "/home/teen/game/lib\n" + root + "/home/teen/game\n", p("/home/teen/game/.git/modules/lib"), p("/home/teen/game/lib"), p("/home/teen/game"), true},
		{"inside a git directory there is no top level", root + "/home/teen/game/.git\n.\nfalse\nfalse\nfalse\n", abs, "", "", true},
		{"CRLF line ends parse the same", root + "/home/teen/game/.git\r\n.git\r\nfalse\r\ntrue\r\nfalse\r\n" + root + "/home/teen/game\r\n", filepath.Join(abs, ".git"), p("/home/teen/game"), "", true},
		{"too few lines are refused", "/x\n", "", "", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			info, err := parseRevParse(abs, tc.out)
			if (err == nil) != tc.ok {
				t.Fatalf("err %v, want ok %v", err, tc.ok)
			}
			if !tc.ok {
				return
			}
			if info.commonDir != tc.wantCommon || info.toplevel != tc.wantTop || info.superproject != tc.wantSuper {
				t.Errorf("got common %q top %q super %q, want %q %q %q", info.commonDir, info.toplevel, info.superproject, tc.wantCommon, tc.wantTop, tc.wantSuper)
			}
		})
	}
}
