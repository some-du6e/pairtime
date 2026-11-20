// SPDX-License-Identifier: AGPL-3.0-or-later

package git

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/pkg/event"
)

// gitWith runs git with extra environment, such as a sequence editor.
func (r *testRepo) gitWith(extra []string, args ...string) string {
	r.g.t.Helper()
	out, err := r.g.gitErr(r.dir, extra, args...)
	if err != nil {
		r.g.t.Fatalf("git %s: %v", strings.Join(args, " "), err)
	}
	return out
}

// revList is git's own list of the commits reachable from tip and from
// none of exclude, oldest first.
func (r *testRepo) revList(tip string, exclude ...string) []string {
	r.g.t.Helper()
	args := append([]string{"rev-list", "--reverse", tip}, prefixAll("^", exclude)...)
	return strings.Fields(r.git(args...))
}

func prefixAll(p string, ss []string) []string {
	out := make([]string, len(ss))
	for i, s := range ss {
		out[i] = p + s
	}
	return out
}

// one returns the only change, failing unless there is exactly one.
func one(t *testing.T, changes []Change) Change {
	t.Helper()
	if len(changes) != 1 {
		var ops []event.GitOp
		for _, c := range changes {
			ops = append(ops, c.Op())
		}
		t.Fatalf("got %d changes %v, want 1", len(changes), ops)
	}
	return changes[0]
}

func gitChange(t *testing.T, c Change) *event.GitChange {
	t.Helper()
	g, ok := c.Event.Data.(*event.GitChange)
	if !ok {
		t.Fatalf("event data is %T, want *event.GitChange", c.Event.Data)
	}
	return g
}

// checkAgainstGit compares a commit record with what git log and git diff
// --shortstat say about the same commit.
func checkAgainstGit(t *testing.T, r *testRepo, c Commit) {
	t.Helper()
	o := r.oracle(c.SHA)
	if !slices.Equal(c.Parents, o.parents) {
		t.Errorf("%s: parents %v, git log says %v", c.SHA[:7], c.Parents, o.parents)
	}
	if c.AuthorTime != o.authorTime || c.CommitterTime != o.committerTime {
		t.Errorf("%s: author and committer times %d and %d, git log says %d and %d", c.SHA[:7], c.AuthorTime, c.CommitterTime, o.authorTime, o.committerTime)
	}
	if c.FilesChanged != o.files || c.Insertions != o.ins || c.Deletions != o.del {
		t.Errorf("%s: shortstat %d files, +%d -%d; git diff --shortstat says %d files, +%d -%d", c.SHA[:7], c.FilesChanged, c.Insertions, c.Deletions, o.files, o.ins, o.del)
	}
	if c.ObservedAt != testNow.UnixMilli() {
		t.Errorf("%s: observed_at %d, want the scan's time %d", c.SHA[:7], c.ObservedAt, testNow.UnixMilli())
	}
}

func shas(cs []Commit) []string {
	var out []string
	for _, c := range cs {
		out = append(out, c.SHA)
	}
	return out
}

func allRewrites(cs []Commit) []Rewrite {
	var out []Rewrite
	for _, c := range cs {
		out = append(out, c.Rewrites...)
	}
	return sortedRewrites(out)
}

func sortedRewrites(rs []Rewrite) []Rewrite {
	out := slices.Clone(rs)
	slices.SortFunc(out, func(a, b Rewrite) int {
		if c := strings.Compare(a.OldSHA, b.OldSHA); c != 0 {
			return c
		}
		return strings.Compare(a.NewSHA, b.NewSHA)
	})
	return out
}

func TestScanAgainstGit(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name string
		run  func(t *testing.T, g *gitEnv)
	}{
		{
			name: "a commit yields one commit event and a record that match git log, diff --shortstat and the reflog",
			run: func(t *testing.T, g *gitEnv) {
				r := g.initRepo()
				r.commit("one", map[string]string{"a.txt": lines("a", 3), "gone.txt": lines("g", 4)})
				res := g.resolver(ProjectMap{}, Excludes{})
				s := g.scanner(res, r.dir)
				r.write(map[string]string{"a.txt": lines("a", 5), "b.txt": lines("b", 2)})
				if err := os.Remove(filepath.Join(r.dir, "gone.txt")); err != nil {
					t.Fatal(err)
				}
				sha := r.commit("two", nil)
				c := one(t, mustScan(t, s))
				ev := gitChange(t, c)
				if ev.Op != event.GitCommit || ev.SHA != sha {
					t.Fatalf("event op %s sha %s, want commit %s", ev.Op, ev.SHA, sha)
				}
				if reflogSHA, msg := r.reflogNewest(); reflogSHA != sha || !strings.HasPrefix(msg, "commit:") {
					t.Errorf("git's newest reflog entry is %s %q, want the commit", reflogSHA, msg)
				}
				if len(c.Commits) != 1 || c.Commits[0].SHA != sha {
					t.Fatalf("records %v, want just %s", shas(c.Commits), sha)
				}
				cm := c.Commits[0]
				checkAgainstGit(t, r, cm)
				if ev.FilesChanged != cm.FilesChanged || ev.Insertions != cm.Insertions || ev.Deletions != cm.Deletions || !slices.Equal(ev.Parents, cm.Parents) {
					t.Errorf("the event's parents and stats differ from the record's")
				}
				if cm.Project != s.Location().Project || c.Event.ProjectKey != cm.Project {
					t.Errorf("project %q and event project %q, want the repo's %q", cm.Project, c.Event.ProjectKey, s.Location().Project)
				}
				if cm.Actor != event.ActorHuman || ev.Actor != event.ActorHuman || cm.Local.Origin != MadeHere || len(cm.Rewrites) != 0 {
					t.Errorf("actor %s/%s origin %s rewrites %v, want a human's commit made here with no rewrites", cm.Actor, ev.Actor, cm.Local.Origin, cm.Rewrites)
				}
				if c.Event.Source != event.SourceGit || c.Event.Harness != event.HarnessNone || c.Event.SessionKey != event.NoSession || c.Event.T != testNow.UnixMilli() {
					t.Errorf("envelope %+v, want a git event with no harness or session at the scan's time", c.Event.Envelope)
				}
				if again := mustScan(t, s); len(again) != 0 {
					t.Errorf("a second scan found %d changes, want none", len(again))
				}
			},
		},
		{
			name: "the first scan reports nothing that happened before it",
			run: func(t *testing.T, g *gitEnv) {
				r := g.initRepo()
				r.commit("one", map[string]string{"a.txt": "a\n"})
				r.commit("two", map[string]string{"a.txt": "b\n"})
				res := g.resolver(ProjectMap{}, Excludes{})
				loc, err := res.Resolve(ctx, r.dir)
				if err != nil {
					t.Fatal(err)
				}
				s := NewScanner(g.runner(), loc, ScannerState{}, func() time.Time { return testNow }, nil)
				if changes := mustScan(t, s); len(changes) != 0 {
					t.Errorf("the first scan found %d changes, want none", len(changes))
				}
				if st := s.State(); !st.Cursor.Set || st.Cursor.New != r.head() {
					t.Errorf("cursor %+v, want it set at HEAD %s", st.Cursor, r.head())
				}
			},
		},
		{
			name: "an amend's record names the commit it replaced, as git's post-rewrite hook does",
			run: func(t *testing.T, g *gitEnv) {
				r := g.initRepo()
				r.recordRewrites()
				r.commit("one", map[string]string{"a.txt": "a\n"})
				old := r.commit("two", map[string]string{"b.txt": "b\n"})
				s := g.scanner(g.resolver(ProjectMap{}, Excludes{}), r.dir)
				r.write(map[string]string{"b.txt": lines("b", 3)})
				r.git("add", "-A")
				r.git("commit", "-q", "--amend", "-m", "two, amended")
				amended := r.head()
				c := one(t, mustScan(t, s))
				if op := c.Op(); op != event.GitAmend {
					t.Fatalf("op %s, want amend", op)
				}
				want := r.readRewrites("amend")
				if len(want) != 1 || want[0] != (Rewrite{old, amended}) {
					t.Fatalf("git's post-rewrite list is %v, want %s to %s", want, old, amended)
				}
				if len(c.Commits) != 1 {
					t.Fatalf("got %d records, want 1", len(c.Commits))
				}
				checkAgainstGit(t, r, c.Commits[0])
				if got := allRewrites(c.Commits); !slices.Equal(got, want) {
					t.Errorf("rewrites %v, git says %v", got, want)
				}
			},
		},
		{
			name: "a rebase through GIT_SEQUENCE_EDITOR maps the squashed and fixed-up commits to the result and drops the dropped one, as git's post-rewrite list does",
			run: func(t *testing.T, g *gitEnv) {
				r := g.initRepo()
				r.recordRewrites()
				base := r.commit("base", map[string]string{"base.txt": "base\n"})
				c1 := r.commit("c1", map[string]string{"c1.txt": lines("c1", 2)})
				c2 := r.commit("c2", map[string]string{"c2.txt": lines("c2", 3)})
				c3 := r.commit("c3", map[string]string{"c3.txt": lines("c3", 4)})
				c4 := r.commit("c4", map[string]string{"c4.txt": lines("c4", 5)})
				s := g.scanner(g.resolver(ProjectMap{}, Excludes{}), r.dir)
				todo := "pick " + c1 + "\nsquash " + c2 + "\nfixup " + c3 + "\ndrop " + c4 + "\n"
				r.gitWith([]string{r.sequenceEditor(todo)}, "rebase", "-q", "-i", base)
				final := r.head()
				c := one(t, mustScan(t, s))
				if ev := gitChange(t, c); ev.Op != event.GitRebase || ev.SHA != final {
					t.Fatalf("event %s %s, want rebase to %s", ev.Op, ev.SHA, final)
				}
				if want := r.revList(final, c4, base); !slices.Equal(shas(c.Commits), want) {
					t.Fatalf("records %v, want the commits git says the rebase made: %v", shas(c.Commits), want)
				}
				for _, cm := range c.Commits {
					checkAgainstGit(t, r, cm)
					if cm.Local.Op != event.GitRebase || cm.Local.Origin != MadeHere {
						t.Errorf("%s: op %s origin %s, want a rebase's commit made here", cm.SHA[:7], cm.Local.Op, cm.Local.Origin)
					}
				}
				want := sortedRewrites(r.readRewrites("rebase"))
				if len(want) != 3 {
					t.Fatalf("git's post-rewrite list is %v, want c1, c2 and c3 mapped", want)
				}
				if got := allRewrites(c.Commits); !slices.Equal(got, want) {
					t.Errorf("rewrites %v, git says %v", got, want)
				}
			},
		},
		{
			name: "a rebase onto a moved main maps each commit to its copy and records none of main's commits",
			run: func(t *testing.T, g *gitEnv) {
				r := g.initRepo()
				r.recordRewrites()
				r.commit("base", map[string]string{"base.txt": "base\n"})
				r.git("checkout", "-q", "-b", "feature")
				r.commit("f1", map[string]string{"f1.txt": lines("f1", 2)})
				f2 := r.commit("f2", map[string]string{"f2.txt": lines("f2", 3)})
				r.git("checkout", "-q", "main")
				m1 := r.commit("m1", map[string]string{"m1.txt": lines("m1", 4)})
				r.git("checkout", "-q", "feature")
				s := g.scanner(g.resolver(ProjectMap{}, Excludes{}), r.dir)
				r.git("rebase", "-q", "main")
				c := one(t, mustScan(t, s))
				if want := r.revList("HEAD", f2, m1); !slices.Equal(shas(c.Commits), want) || len(want) != 2 {
					t.Fatalf("records %v, want the two copies git made: %v", shas(c.Commits), want)
				}
				for _, cm := range c.Commits {
					checkAgainstGit(t, r, cm)
				}
				want := sortedRewrites(r.readRewrites("rebase"))
				if got := allRewrites(c.Commits); !slices.Equal(got, want) || len(want) != 2 {
					t.Errorf("rewrites %v, git says %v", got, want)
				}
			},
		},
		{
			name: "a rebase that stops for an edit is held until it continues, then maps every commit as git does",
			run: func(t *testing.T, g *gitEnv) {
				r := g.initRepo()
				r.recordRewrites()
				base := r.commit("base", map[string]string{"base.txt": "base\n"})
				c1 := r.commit("c1", map[string]string{"c1.txt": "c1\n"})
				c2 := r.commit("c2", map[string]string{"c2.txt": "c2\n"})
				s := g.scanner(g.resolver(ProjectMap{}, Excludes{}), r.dir)
				r.gitWith([]string{r.sequenceEditor("edit " + c1 + "\npick " + c2 + "\n")}, "rebase", "-q", "-i", base)
				if changes := mustScan(t, s); len(changes) != 0 {
					t.Fatalf("a scan during the edit stop found %d changes, want none until the rebase ends", len(changes))
				}
				r.write(map[string]string{"c1.txt": "c1 edited\n"})
				r.git("add", "-A")
				r.git("commit", "-q", "--amend", "--no-edit")
				r.git("rebase", "--continue")
				c := one(t, mustScan(t, s))
				if c.Op() != event.GitRebase {
					t.Fatalf("op %s, want rebase", c.Op())
				}
				want := sortedRewrites(r.readRewrites("rebase"))
				if got := allRewrites(c.Commits); !slices.Equal(got, want) || len(want) != 2 {
					t.Errorf("rewrites %v, git says %v", got, want)
				}
			},
		},
		{
			name: "an aborted rebase records nothing, and the next commit is still seen",
			run: func(t *testing.T, g *gitEnv) {
				r := g.initRepo()
				base := r.commit("base", map[string]string{"base.txt": "base\n"})
				c1 := r.commit("c1", map[string]string{"c1.txt": "c1\n"})
				s := g.scanner(g.resolver(ProjectMap{}, Excludes{}), r.dir)
				r.gitWith([]string{r.sequenceEditor("edit " + c1 + "\n")}, "rebase", "-q", "-i", base)
				if changes := mustScan(t, s); len(changes) != 0 {
					t.Fatalf("a scan during the stop found %d changes, want none", len(changes))
				}
				r.git("rebase", "--abort")
				if changes := mustScan(t, s); len(changes) != 0 {
					t.Fatalf("the abort yielded %d changes, want none", len(changes))
				}
				sha := r.commit("after", map[string]string{"after.txt": "x\n"})
				if c := one(t, mustScan(t, s)); c.Op() != event.GitCommit || gitChange(t, c).SHA != sha {
					t.Errorf("got %s, want the commit after the abort", c.Op())
				}
			},
		},
		{
			name: "a merge commit is recorded with its shortstat against the first parent, and the merged branch's commits aren't recorded again",
			run: func(t *testing.T, g *gitEnv) {
				r := g.initRepo()
				r.commit("base", map[string]string{"base.txt": "base\n"})
				s := g.scanner(g.resolver(ProjectMap{}, Excludes{}), r.dir)
				r.git("checkout", "-q", "-b", "side")
				side := r.commit("side", map[string]string{"side.txt": lines("s", 6)})
				r.git("checkout", "-q", "main")
				r.commit("m1", map[string]string{"m1.txt": lines("m", 2)})
				mustScan(t, s)
				r.git("merge", "-q", "--no-ff", "--no-edit", "side")
				merge := r.head()
				c := one(t, mustScan(t, s))
				if c.Op() != event.GitMerge {
					t.Fatalf("op %s, want merge", c.Op())
				}
				if got := shas(c.Commits); !slices.Equal(got, []string{merge}) {
					t.Fatalf("records %v, want only the merge commit %s; %s was recorded when it was made", got, merge, side)
				}
				checkAgainstGit(t, r, c.Commits[0])
				if c.Commits[0].Local.Origin != MadeHere || len(c.Commits[0].Parents) != 2 {
					t.Errorf("origin %s parents %v, want a merge commit made here", c.Commits[0].Local.Origin, c.Commits[0].Parents)
				}
			},
		},
		{
			name: "a fast-forward merge of a local branch yields a merge event and records no commit",
			run: func(t *testing.T, g *gitEnv) {
				r := g.initRepo()
				r.commit("base", map[string]string{"base.txt": "base\n"})
				s := g.scanner(g.resolver(ProjectMap{}, Excludes{}), r.dir)
				r.git("checkout", "-q", "-b", "topic")
				topic := r.commit("topic", map[string]string{"t.txt": "t\n"})
				r.git("checkout", "-q", "main")
				mustScan(t, s)
				r.git("merge", "-q", "--ff-only", "topic")
				c := one(t, mustScan(t, s))
				if ev := gitChange(t, c); ev.Op != event.GitMerge || ev.SHA != topic || len(c.Commits) != 0 {
					t.Errorf("got %s to %s with %d records, want a merge to %s with none", ev.Op, ev.SHA, len(c.Commits), topic)
				}
			},
		},
		{
			name: "a pull that fast-forwards records the pulled commit as made elsewhere",
			run: func(t *testing.T, g *gitEnv) {
				up := g.initRepo()
				up.commit("one", map[string]string{"a.txt": "a\n"})
				dir := filepath.Join(t.TempDir(), "clone")
				g.git(filepath.Dir(dir), "clone", "-q", up.dir, dir)
				clone := &testRepo{g: g, dir: dir}
				s := g.scanner(g.resolver(ProjectMap{}, Excludes{}), dir)
				pulled := up.commit("two", map[string]string{"b.txt": lines("b", 3)})
				clone.git("pull", "-q", "--ff-only")
				c := one(t, mustScan(t, s))
				if c.Op() != event.GitMerge || len(c.Commits) != 1 || c.Commits[0].SHA != pulled {
					t.Fatalf("got %s with records %v, want a merge recording %s", c.Op(), shas(c.Commits), pulled)
				}
				cm := c.Commits[0]
				checkAgainstGit(t, clone, cm)
				if cm.Local.Origin != MadeElsewhere || cm.Actor != event.ActorUnknown {
					t.Errorf("origin %s actor %s, want a commit made elsewhere by someone unknown", cm.Local.Origin, cm.Actor)
				}
			},
		},
		{
			name: "a reset yields a reset event and no commit record",
			run: func(t *testing.T, g *gitEnv) {
				r := g.initRepo()
				first := r.commit("one", map[string]string{"a.txt": "a\n"})
				r.commit("two", map[string]string{"a.txt": "b\n"})
				s := g.scanner(g.resolver(ProjectMap{}, Excludes{}), r.dir)
				r.git("reset", "-q", "--hard", "HEAD~1")
				c := one(t, mustScan(t, s))
				if ev := gitChange(t, c); ev.Op != event.GitReset || ev.SHA != first || len(c.Commits) != 0 {
					t.Errorf("got %s to %s with %d records, want a reset to %s with none", ev.Op, ev.SHA, len(c.Commits), first)
				}
			},
		},
		{
			name: "a checkout to a detached HEAD yields a checkout event, and a commit on it is recorded",
			run: func(t *testing.T, g *gitEnv) {
				r := g.initRepo()
				first := r.commit("one", map[string]string{"a.txt": "a\n"})
				r.commit("two", map[string]string{"a.txt": "b\n"})
				s := g.scanner(g.resolver(ProjectMap{}, Excludes{}), r.dir)
				r.git("checkout", "-q", "--detach", first)
				c := one(t, mustScan(t, s))
				if ev := gitChange(t, c); ev.Op != event.GitCheckout || ev.SHA != first {
					t.Fatalf("got %s to %s, want a checkout of %s", ev.Op, ev.SHA, first)
				}
				sha := r.commit("detached", map[string]string{"d.txt": "d\n"})
				c = one(t, mustScan(t, s))
				if c.Op() != event.GitCommit || len(c.Commits) != 1 || c.Commits[0].SHA != sha {
					t.Fatalf("got %s with %v, want the detached commit %s", c.Op(), shas(c.Commits), sha)
				}
				checkAgainstGit(t, r, c.Commits[0])
			},
		},
		{
			name: "a revert yields op revert, which SM never counts as progress",
			run: func(t *testing.T, g *gitEnv) {
				r := g.initRepo()
				r.commit("one", map[string]string{"a.txt": "a\n"})
				r.commit("two", map[string]string{"a.txt": "b\n"})
				s := g.scanner(g.resolver(ProjectMap{}, Excludes{}), r.dir)
				r.git("revert", "--no-edit", "HEAD")
				c := one(t, mustScan(t, s))
				if c.Op() != event.GitRevert || len(c.Commits) != 1 {
					t.Fatalf("got %s with %d records, want a revert with 1", c.Op(), len(c.Commits))
				}
				checkAgainstGit(t, r, c.Commits[0])
			},
		},
		{
			name: "a cherry-pick yields a commit made here",
			run: func(t *testing.T, g *gitEnv) {
				r := g.initRepo()
				r.commit("one", map[string]string{"a.txt": "a\n"})
				r.git("checkout", "-q", "-b", "side")
				side := r.commit("side", map[string]string{"s.txt": lines("s", 2)})
				r.git("checkout", "-q", "main")
				s := g.scanner(g.resolver(ProjectMap{}, Excludes{}), r.dir)
				r.git("cherry-pick", side)
				c := one(t, mustScan(t, s))
				if c.Op() != event.GitCommit || len(c.Commits) != 1 || c.Commits[0].Local.Origin != MadeHere {
					t.Fatalf("got %s with %d records, want one commit made here", c.Op(), len(c.Commits))
				}
				checkAgainstGit(t, r, c.Commits[0])
			},
		},
		{
			name: "a commit in a linked worktree reaches that worktree's scanner under the repo's project, and not the main worktree's",
			run: func(t *testing.T, g *gitEnv) {
				r := g.initRepo()
				r.commit("one", map[string]string{"a.txt": "a\n"})
				wt := filepath.Join(t.TempDir(), "wt")
				r.git("worktree", "add", "-q", wt, "-b", "wt")
				res := g.resolver(ProjectMap{}, Excludes{})
				main, err := res.Resolve(ctx, r.dir)
				if err != nil {
					t.Fatal(err)
				}
				linked, err := res.Resolve(ctx, wt)
				if err != nil {
					t.Fatal(err)
				}
				if !linked.Linked || linked.Project != main.Project || !sameDir(t, linked.CommonDir, main.CommonDir) {
					t.Fatalf("linked worktree %+v, want a linked worktree of the main one's repo and project", linked)
				}
				ms, ws := g.scanner(res, r.dir), g.scanner(res, wt)
				w := &testRepo{g: g, dir: wt}
				sha := w.commit("in the worktree", map[string]string{"w.txt": "w\n"})
				c := one(t, mustScan(t, ws))
				if c.Op() != event.GitCommit || c.Commits[0].SHA != sha || c.Commits[0].Project != main.Project {
					t.Errorf("got %s %v in project %s, want %s in %s", c.Op(), shas(c.Commits), c.Commits[0].Project, sha, main.Project)
				}
				checkAgainstGit(t, w, c.Commits[0])
				if changes := mustScan(t, ms); len(changes) != 0 {
					t.Errorf("the main worktree's scanner found %d changes, want none: the commit moved the other worktree's HEAD", len(changes))
				}
			},
		},
		{
			name: "a submodule is a project of its own, and its own scanner sees its commits",
			run: func(t *testing.T, g *gitEnv) {
				lib := g.initRepo()
				lib.commit("lib", map[string]string{"lib.txt": "lib\n"})
				app := g.initRepo()
				app.commit("app", map[string]string{"app.txt": "app\n"})
				app.git("submodule", "add", "-q", filepath.ToSlash(lib.dir), "lib")
				app.git("commit", "-q", "-m", "add lib")
				res := g.resolver(ProjectMap{}, Excludes{})
				sub := filepath.Join(app.dir, "lib")
				subLoc, err := res.Resolve(ctx, sub)
				if err != nil {
					t.Fatal(err)
				}
				appLoc, _ := res.Resolve(ctx, app.dir)
				libLoc, _ := res.Resolve(ctx, lib.dir)
				if !subLoc.Submodule || subLoc.Project != libLoc.Project || subLoc.Project == appLoc.Project {
					t.Fatalf("submodule %+v; want a submodule with lib's project %s, not app's %s", subLoc, libLoc.Project, appLoc.Project)
				}
				s := g.scanner(res, sub)
				subRepo := &testRepo{g: g, dir: sub}
				sha := subRepo.commit("in the submodule", map[string]string{"more.txt": "more\n"})
				c := one(t, mustScan(t, s))
				if c.Commits[0].SHA != sha || c.Event.ProjectKey != libLoc.Project {
					t.Errorf("got %v in %s, want %s in lib's project", shas(c.Commits), c.Event.ProjectKey, sha)
				}
				checkAgainstGit(t, subRepo, c.Commits[0])
			},
		},
		{
			name: "a shallow clone is keyed by its folder, since git hasn't its roots, and its commits are still recorded",
			run: func(t *testing.T, g *gitEnv) {
				up := g.initRepo()
				up.commit("one", map[string]string{"a.txt": "a\n"})
				up.commit("two", map[string]string{"a.txt": "b\n"})
				up.commit("three", map[string]string{"a.txt": "c\n"})
				dir := filepath.Join(t.TempDir(), "shallow")
				g.git(filepath.Dir(dir), "clone", "-q", "--depth", "1", fileURL(up.dir), dir)
				res := g.resolver(ProjectMap{}, Excludes{})
				loc, err := res.Resolve(ctx, dir)
				if err != nil {
					t.Fatal(err)
				}
				upLoc, _ := res.Resolve(ctx, up.dir)
				if loc.Kind != KindShallow || !strings.HasPrefix(loc.Project, prefixFolder) || loc.Project == upLoc.Project || len(loc.Roots) != 0 {
					t.Fatalf("shallow clone %+v, want a folder-keyed shallow location with no roots", loc)
				}
				s := g.scanner(res, dir)
				sc := &testRepo{g: g, dir: dir}
				sha := sc.commit("local", map[string]string{"l.txt": "l\n"})
				c := one(t, mustScan(t, s))
				if c.Commits[0].SHA != sha || c.Commits[0].Project != loc.Project {
					t.Errorf("got %v in %s, want %s in the clone's project", shas(c.Commits), c.Commits[0].Project, sha)
				}
				checkAgainstGit(t, sc, c.Commits[0])
			},
		},
		{
			name: "a new repo's first commit moves it from its folder key to its root-commit key and keeps the folder key as an alias",
			run: func(t *testing.T, g *gitEnv) {
				dir := filepath.Join(t.TempDir(), "game")
				if err := os.Mkdir(dir, 0o755); err != nil {
					t.Fatal(err)
				}
				res := g.resolver(ProjectMap{}, Excludes{})
				before, err := res.Resolve(ctx, dir)
				if err != nil {
					t.Fatal(err)
				}
				g.git(dir, "init", "-q")
				res.Forget()
				unborn, err := res.Resolve(ctx, dir)
				if err != nil {
					t.Fatal(err)
				}
				if before.Kind != KindFolder || unborn.Kind != KindUnborn || unborn.Project != before.Project {
					t.Fatalf("before %s %s, after git init %s %s; want the folder's key to carry over", before.Kind, before.Project, unborn.Kind, unborn.Project)
				}
				s := g.scanner(res, dir)
				r := &testRepo{g: g, dir: dir}
				sha := r.commit("first", map[string]string{"main.go": "package main\n"})
				c := one(t, mustScan(t, s))
				roots := strings.Fields(r.git("rev-list", "--max-parents=0", "HEAD"))
				want := rootsKey(testSecret, roots)
				if c.Event.ProjectKey != want || c.Commits[0].SHA != sha || c.Commits[0].Project != want {
					t.Errorf("first commit in project %s, want the root-commit key %s", c.Event.ProjectKey, want)
				}
				if aliases := res.Aliases(want); !slices.Contains(aliases, before.Project) {
					t.Errorf("aliases %v, want the folder key %s", aliases, before.Project)
				}
				checkAgainstGit(t, r, c.Commits[0])
			},
		},
		{
			name: "a repo with its reflog turned off still reports a commit, from HEAD",
			run: func(t *testing.T, g *gitEnv) {
				g.realDates = true
				r := g.initRepo()
				r.git("config", "core.logAllRefUpdates", "false")
				r.commit("one", map[string]string{"a.txt": "a\n"})
				res := g.resolver(ProjectMap{}, Excludes{})
				loc, err := res.Resolve(ctx, r.dir)
				if err != nil {
					t.Fatal(err)
				}
				s := NewScanner(g.runner(), loc, ScannerState{}, time.Now, res.Rekey)
				mustScan(t, s)
				sha := r.commit("two", map[string]string{"a.txt": "b\n"})
				c := one(t, mustScan(t, s))
				if c.Op() != event.GitCommit || len(c.Commits) != 1 || c.Commits[0].SHA != sha {
					t.Errorf("got %s with %v, want a commit of %s", c.Op(), shas(c.Commits), sha)
				}
			},
		},
		{
			name: "an agent trailer becomes the agent's key and the commit's actor unknown, and no name, email, subject or path leaves in JSON",
			run: func(t *testing.T, g *gitEnv) {
				r := g.initRepo()
				r.commit("one", map[string]string{"a.txt": "a\n"})
				s := g.scanner(g.resolver(ProjectMap{}, Excludes{}), r.dir)
				r.write(map[string]string{"secret-plan.txt": "x\n"})
				r.git("add", "-A")
				r.git("commit", "-q", "-m", "Add the secret plan\n\nCo-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>")
				c := one(t, mustScan(t, s))
				cm := c.Commits[0]
				if cm.AgentTrailer != "claude" || gitChange(t, c).AgentTrailer != "claude" || cm.Actor != event.ActorUnknown {
					t.Errorf("trailer %q actor %s, want claude and unknown", cm.AgentTrailer, cm.Actor)
				}
				data, err := json.Marshal(struct {
					E event.Event
					C []Commit
				}{c.Event, c.Commits})
				if err != nil {
					t.Fatal(err)
				}
				for _, private := range []string{authorName, authorEmail, "secret plan", "secret-plan", "noreply@anthropic.com", "Sonnet", filepath.Base(r.dir), filepath.ToSlash(r.dir)} {
					if strings.Contains(strings.ToLower(string(data)), strings.ToLower(private)) {
						t.Errorf("JSON holds %q: %s", private, data)
					}
				}
			},
		},
		{
			name: "a git-ai note under refs/notes/ai becomes local labels, without its paths",
			run: func(t *testing.T, g *gitEnv) {
				r := g.initRepo()
				r.commit("one", map[string]string{"a.txt": "a\n"})
				s := g.scanner(g.resolver(ProjectMap{}, Excludes{}), r.dir)
				sha := r.commit("two", map[string]string{"src/main.rs": lines("fn", 20)})
				note := "src/main.rs\n  s_c9883b05a2487d::t_9f8e7d6c5b4a32 1-10\n  h_31dce776f88375 15-20\n---\n" +
					`{"schema_version":"authorship/3.0.0","base_commit_sha":"` + sha + `","prompts":{},"sessions":{"s_c9883b05a2487d":{"agent_id":{"tool":"claude","id":"abc","model":"m"},"human_author":"` + authorEmail + `"}}}`
				r.git("notes", "--ref=refs/notes/ai", "add", "-m", note, sha)
				c := one(t, mustScan(t, s))
				ai := c.Commits[0].Local.AI
				if ai == nil || !slices.Equal(ai.Tools, []string{"claude"}) || ai.AILines != 10 || ai.HumanLines != 6 {
					t.Fatalf("git-ai labels %+v, want claude with 10 agent lines and 6 human lines", ai)
				}
				data, _ := json.Marshal(c.Commits[0])
				if strings.Contains(string(data), "main.rs") || strings.Contains(string(data), "claude") {
					t.Errorf("a commit record's JSON holds the note: %s", data)
				}
			},
		},
		{
			name: "a git.change event validates once the agent fills in the envelope",
			run: func(t *testing.T, g *gitEnv) {
				r := g.initRepo()
				r.commit("one", map[string]string{"a.txt": "a\n"})
				s := g.scanner(g.resolver(ProjectMap{}, Excludes{}), r.dir)
				r.commit("two", map[string]string{"a.txt": "b\n"})
				ev := one(t, mustScan(t, s)).Event
				ev.EventID = event.NewEventID(testNow)
				ev.DeviceID = "dev_test"
				ev.Seq = 1
				ev.Mono = 1
				ev.BootID = "boot_test"
				ev.Via = event.ViaGit
				if err := ev.Validate(); err != nil {
					t.Errorf("Validate: %v", err)
				}
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tc.run(t, newGitEnv(t))
		})
	}
}

// TestCommitJSON pins the fields a commit record can upload to DESIGN
// 4.3's list. A new field here changes what pairtime may upload.
func TestCommitJSON(t *testing.T) {
	cases := []struct {
		name string
		c    Commit
		want []string
	}{
		{
			name: "a record with every field set marshals DESIGN 4.3's commit fields and nothing local",
			c: Commit{
				SHA: strings.Repeat("a", 40), Parents: []string{strings.Repeat("b", 40)}, Project: "gproject",
				ObservedAt: 1, AuthorTime: 2, CommitterTime: 3, FilesChanged: 4, Insertions: 5, Deletions: 6,
				Rewrites: []Rewrite{{OldSHA: strings.Repeat("c", 40), NewSHA: strings.Repeat("a", 40)}},
				Actor:    event.ActorHuman, AgentTrailer: "claude",
				Local: CommitLocal{Op: event.GitCommit, Origin: MadeHere, AuthorTZ: "-0600", CommitterTZ: "-0600", ReflogTime: 7, AI: &AINote{Tools: []string{"claude"}}},
			},
			want: []string{"actor", "agent_trailer", "author_time", "committer_time", "deletions", "files_changed", "insertions", "observed_at", "parents", "project", "rewrites", "sha"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			data, err := json.Marshal(tc.c)
			if err != nil {
				t.Fatal(err)
			}
			var m map[string]json.RawMessage
			if err := json.Unmarshal(data, &m); err != nil {
				t.Fatal(err)
			}
			var got []string
			for k := range m {
				got = append(got, k)
			}
			slices.Sort(got)
			if !slices.Equal(got, tc.want) {
				t.Errorf("keys %v, want %v", got, tc.want)
			}
			if strings.Contains(string(data), "-0600") {
				t.Errorf("JSON holds the time zone: %s", data)
			}
		})
	}
}
