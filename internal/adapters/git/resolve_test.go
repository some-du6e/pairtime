// SPDX-License-Identifier: AGPL-3.0-or-later

package git

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestResolve(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name string
		run  func(t *testing.T, g *gitEnv)
	}{
		{
			name: "a fresh clone resolves to the same project as the repo it was cloned from",
			run: func(t *testing.T, g *gitEnv) {
				r := g.initRepo()
				r.commit("one", map[string]string{"a.txt": "a\n"})
				r.commit("two", map[string]string{"a.txt": "b\n"})
				clone := filepath.Join(t.TempDir(), "clone")
				g.git(filepath.Dir(clone), "clone", "-q", r.dir, clone)
				orig, err := g.resolver(ProjectMap{}, Excludes{}).Resolve(ctx, r.dir)
				if err != nil {
					t.Fatal(err)
				}
				// A resolver that has never seen the original, as on a
				// reinstall, finds the same key from the root commits alone.
				fresh, err := g.resolver(ProjectMap{}, Excludes{}).Resolve(ctx, clone)
				if err != nil {
					t.Fatal(err)
				}
				roots := strings.Fields(r.git("rev-list", "--max-parents=0", "HEAD"))
				if orig.Kind != KindRepo || fresh.Kind != KindRepo || orig.Project != fresh.Project {
					t.Errorf("original %s %s, clone %s %s; want one repo project", orig.Kind, orig.Project, fresh.Kind, fresh.Project)
				}
				if !slices.Equal(fresh.Roots, roots) || orig.Project != rootsKey(testSecret, roots) {
					t.Errorf("roots %v, git says %v", fresh.Roots, roots)
				}
			},
		},
		{
			name: "project keys are HMACs under the device's secret: another secret gives another key, and every key is an opaque key",
			run: func(t *testing.T, g *gitEnv) {
				r := g.initRepo()
				r.commit("one", map[string]string{"a.txt": "a\n"})
				folder := t.TempDir()
				a := g.resolver(ProjectMap{}, Excludes{})
				b, err := NewResolver(ResolverOptions{Git: g.runner(), Secret: bytes.Repeat([]byte{7}, SecretLen)}, ProjectMap{})
				if err != nil {
					t.Fatal(err)
				}
				for _, dir := range []string{r.dir, folder} {
					la, err := a.Resolve(ctx, dir)
					if err != nil {
						t.Fatal(err)
					}
					lb, err := b.Resolve(ctx, dir)
					if err != nil {
						t.Fatal(err)
					}
					if la.Project == lb.Project {
						t.Errorf("%s: the same key %s under two secrets", la.Kind, la.Project)
					}
					for _, k := range []string{la.Project, lb.Project} {
						if !keyRE.MatchString(k) || (len(la.Roots) > 0 && strings.Contains(k, la.Roots[0])) {
							t.Errorf("key %q isn't an opaque key, or holds a root commit", k)
						}
					}
				}
			},
		},
		{
			name: "a subfolder of a repo resolves to the repo's project and top level",
			run: func(t *testing.T, g *gitEnv) {
				r := g.initRepo()
				r.commit("one", map[string]string{"src/deep/a.txt": "a\n"})
				res := g.resolver(ProjectMap{}, Excludes{})
				top, err := res.Resolve(ctx, r.dir)
				if err != nil {
					t.Fatal(err)
				}
				sub, err := res.Resolve(ctx, filepath.Join(r.dir, "src", "deep"))
				if err != nil {
					t.Fatal(err)
				}
				if sub.Project != top.Project || !sameDir(t, sub.Dir, r.dir) || !sameDir(t, sub.GitDir, filepath.Join(r.dir, ".git")) {
					t.Errorf("subfolder %+v, want the repo %+v", sub, top)
				}
			},
		},
		{
			name: "a folder without git gets a folder key, the same each time and different for another folder",
			run: func(t *testing.T, g *gitEnv) {
				a, b := t.TempDir(), t.TempDir()
				res := g.resolver(ProjectMap{}, Excludes{})
				la1, _ := res.Resolve(ctx, a)
				res.Forget()
				la2, _ := res.Resolve(ctx, a)
				lb, _ := res.Resolve(ctx, b)
				if la1.Kind != KindFolder || !strings.HasPrefix(la1.Project, prefixFolder) || la1.Project != la2.Project || la1.Project == lb.Project {
					t.Errorf("keys %s, %s and %s; want one stable folder key per folder", la1.Project, la2.Project, lb.Project)
				}
			},
		},
		{
			name: "a .pairtime-ignore in a repo keeps the repo and every folder in it out",
			run: func(t *testing.T, g *gitEnv) {
				r := g.initRepo()
				r.commit("one", map[string]string{"src/a.txt": "a\n"})
				r.write(map[string]string{IgnoreFile: ""})
				res := g.resolver(ProjectMap{}, Excludes{})
				for _, dir := range []string{r.dir, filepath.Join(r.dir, "src")} {
					if loc, err := res.Resolve(ctx, dir); !errors.Is(err, ErrIgnored) || loc.Project != "" {
						t.Errorf("%s: got %+v, %v; want ErrIgnored", dir, loc, err)
					}
				}
			},
		},
		{
			name: "a .pairtime-ignore added after a folder was resolved keeps it out from the next check",
			run: func(t *testing.T, g *gitEnv) {
				r := g.initRepo()
				r.commit("one", map[string]string{"a.txt": "a\n"})
				res := g.resolver(ProjectMap{}, Excludes{})
				if _, err := res.Resolve(ctx, r.dir); err != nil {
					t.Fatal(err)
				}
				r.write(map[string]string{IgnoreFile: ""})
				res.recheck = -1 // as if DefaultRecheck had passed
				if _, err := res.Resolve(ctx, r.dir); !errors.Is(err, ErrIgnored) {
					t.Errorf("got %v, want ErrIgnored", err)
				}
			},
		},
		{
			name: "an exclude pattern keeps a folder and the folders below it out, and leaves its neighbours in",
			run: func(t *testing.T, g *gitEnv) {
				root := t.TempDir()
				school := filepath.Join(root, "school")
				game := filepath.Join(root, "game")
				for _, d := range []string{filepath.Join(school, "essay"), game} {
					if err := os.MkdirAll(d, 0o755); err != nil {
						t.Fatal(err)
					}
				}
				ex, problems := NewExcludes([]string{"school"}, "")
				if len(problems) > 0 {
					t.Fatal(problems)
				}
				res := g.resolver(ProjectMap{}, ex)
				for _, dir := range []string{school, filepath.Join(school, "essay")} {
					if _, err := res.Resolve(ctx, dir); !errors.Is(err, ErrIgnored) {
						t.Errorf("%s: got %v, want ErrIgnored", dir, err)
					}
				}
				if _, err := res.Resolve(ctx, game); err != nil {
					t.Errorf("game: %v, want it resolved", err)
				}
			},
		},
		{
			name: "a repo whose history gains a root keeps its key, and a fresh clone with both roots joins it",
			run: func(t *testing.T, g *gitEnv) {
				r := g.initRepo()
				r.commit("one", map[string]string{"a.txt": "a\n"})
				other := g.initRepo()
				other.commit("other", map[string]string{"o.txt": "o\n"})
				res := g.resolver(ProjectMap{}, Excludes{})
				before, err := res.Resolve(ctx, r.dir)
				if err != nil {
					t.Fatal(err)
				}
				r.git("pull", "-q", "--no-rebase", "--allow-unrelated-histories", "--no-edit", other.dir, "main")
				if n := len(strings.Fields(r.git("rev-list", "--max-parents=0", "HEAD"))); n != 2 {
					t.Fatalf("the history has %d roots, want 2", n)
				}
				clone := filepath.Join(t.TempDir(), "clone")
				g.git(filepath.Dir(clone), "clone", "-q", r.dir, clone)
				after, err := res.Resolve(ctx, clone)
				if err != nil {
					t.Fatal(err)
				}
				if after.Project != before.Project {
					t.Errorf("clone %s, original %s; want the original's key", after.Project, before.Project)
				}
				// The map remembers it across a restart.
				again, err := g.resolver(res.Map(), Excludes{}).Resolve(ctx, clone)
				if err != nil {
					t.Fatal(err)
				}
				if again.Project != before.Project {
					t.Errorf("after a restart %s, want %s", again.Project, before.Project)
				}
			},
		},
		{
			name: "a repo reached through a link resolves to one common dir, git dir and project, whichever spelling the folder comes in",
			run: func(t *testing.T, g *gitEnv) {
				r := g.initRepo()
				r.commit("one", map[string]string{"src/a.txt": "a\n"})
				wt := filepath.Join(filepath.Dir(r.dir), "wt")
				r.git("worktree", "add", "-q", wt, "-b", "wt")
				link := linkTo(t, filepath.Dir(r.dir), true)
				via := filepath.Join(link, filepath.Base(r.dir))
				res := g.resolver(ProjectMap{}, Excludes{})
				var main []Location
				for _, dir := range []string{via, filepath.Join(via, "src"), r.dir, filepath.Join(via, ".git")} {
					loc, err := res.Resolve(ctx, dir)
					if err != nil {
						t.Fatal(err)
					}
					if loc.Linked || loc.CommonDir != loc.GitDir {
						t.Errorf("%s: common dir %q, git dir %q, linked %v; want a main worktree whose common dir is its git dir", dir, loc.CommonDir, loc.GitDir, loc.Linked)
					}
					main = append(main, loc)
				}
				for _, loc := range main[1:] {
					if loc.CommonDir != main[0].CommonDir || loc.GitDir != main[0].GitDir || loc.Project != main[0].Project {
						t.Errorf("%+v differs from %+v", loc, main[0])
					}
				}
				linked, err := res.Resolve(ctx, filepath.Join(link, "wt"))
				if err != nil {
					t.Fatal(err)
				}
				if !linked.Linked || linked.CommonDir != main[0].CommonDir || linked.Project != main[0].Project {
					t.Errorf("linked worktree %+v, want a linked worktree with the common dir %q", linked, main[0].CommonDir)
				}
				if m := res.Map(); len(m.Repos) != 1 {
					t.Errorf("the project map has %d repos, want 1: %+v", len(m.Repos), m.Repos)
				}
			},
		},
		{
			name: "resolving a repo nested in another's worktree runs none of the outer repo's hooks, though the superproject lookup reads the outer index",
			run: func(t *testing.T, g *gitEnv) {
				outer := g.initRepo()
				outer.commit("one", map[string]string{"a.txt": "a\n"})
				nested := filepath.Join(outer.dir, "nested")
				if err := os.Mkdir(nested, 0o755); err != nil {
					t.Fatal(err)
				}
				g.git(nested, "init", "-q")
				(&testRepo{g: g, dir: nested}).commit("n", map[string]string{"n.txt": "n\n"})
				marker := filepath.Join(t.TempDir(), "fsmonitor-ran")
				hook := filepath.Join(t.TempDir(), "fsmonitor.sh")
				script := "#!/bin/sh\necho ran >> '" + filepath.ToSlash(marker) + "'\nexit 1\n"
				if err := os.WriteFile(hook, []byte(script), 0o755); err != nil {
					t.Fatal(err)
				}
				outer.git("config", "core.fsmonitor", filepath.ToSlash(hook))
				// The oracle: git on its own runs the hook for the lookup.
				if _, err := g.gitErr(nested, nil, "rev-parse", "--show-superproject-working-tree"); err != nil {
					t.Fatal(err)
				}
				if _, err := os.Stat(marker); err != nil {
					t.Skipf("this git ran no fsmonitor hook for the superproject lookup, so there is nothing to check: %v", err)
				}
				if err := os.Remove(marker); err != nil {
					t.Fatal(err)
				}
				if _, err := g.resolver(ProjectMap{}, Excludes{}).Resolve(ctx, nested); err != nil {
					t.Fatal(err)
				}
				if _, err := os.Stat(marker); err == nil {
					t.Error("resolving the nested repo ran the outer repo's fsmonitor hook")
				}
			},
		},
		{
			name: "a folder inside a git directory resolves to its repo with no top level of its own",
			run: func(t *testing.T, g *gitEnv) {
				r := g.initRepo()
				r.commit("one", map[string]string{"a.txt": "a\n"})
				res := g.resolver(ProjectMap{}, Excludes{})
				top, _ := res.Resolve(ctx, r.dir)
				in, err := res.Resolve(ctx, filepath.Join(r.dir, ".git"))
				if err != nil {
					t.Fatal(err)
				}
				if in.Project != top.Project || !sameDir(t, in.Dir, r.dir) {
					t.Errorf("inside .git %+v, want the repo %+v", in, top)
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

func TestExcludes(t *testing.T) {
	home := "/home/teen"
	if os.PathSeparator == '\\' {
		home = `C:\Users\Teen`
	}
	abs := func(p string) string {
		if os.PathSeparator == '\\' {
			return `C:\` + strings.ReplaceAll(p, "/", `\`)
		}
		return "/" + p
	}
	cases := []struct {
		name     string
		patterns []string
		dir      string
		want     bool
	}{
		{"a bare name matches a folder of that name anywhere", []string{"school"}, abs("home/teen/school"), true},
		{"a bare name matches the folders below the one it names", []string{"school"}, abs("home/teen/school/essay/drafts"), true},
		{"a bare name doesn't match a longer name", []string{"school"}, abs("home/teen/schoolwork"), false},
		{"a wildcard stays within one folder name", []string{"client-*"}, abs("home/teen/client-acme/src"), true},
		{"an absolute pattern matches only under its own path", []string{abs("home/teen/work")}, abs("srv/home/teen/work"), false},
		{"an absolute pattern matches its folder and below", []string{abs("home/teen/work")}, abs("home/teen/work/x"), true},
		{"** matches any number of folders", []string{abs("home/**/secret")}, abs("home/teen/a/b/secret/c"), true},
		{"a relative pattern with a separator matches that run of folders anywhere", []string{"work/private"}, abs("home/teen/work/private/notes"), true},
		{"a relative pattern with a separator needs the whole run", []string{"work/private"}, abs("home/teen/private"), false},
		{"~ is the home folder", []string{"~/school"}, filepath.Join(home, "school", "essay"), true},
		{"~ doesn't match outside the home folder", []string{"~/school"}, abs("school"), false},
		{"no patterns match nothing", nil, abs("home/teen"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ex, problems := NewExcludes(tc.patterns, home)
			if len(problems) > 0 {
				t.Fatal(problems)
			}
			if got := ex.Match(tc.dir); got != tc.want {
				t.Errorf("Match(%q) with %q = %v, want %v", tc.dir, tc.patterns, got, tc.want)
			}
		})
	}
}

func TestExcludesProblems(t *testing.T) {
	cases := []struct {
		name     string
		patterns []string
		home     string
		want     int
	}{
		{"a pattern that doesn't compile is reported and left out", []string{"[unclosed"}, "", 1},
		{"~ with no known home folder is reported", []string{"~/x"}, "", 1},
		{"blank patterns are skipped quietly", []string{"", "  "}, "", 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ex, problems := NewExcludes(tc.patterns, tc.home)
			if len(problems) != tc.want || len(ex.pats) != 0 {
				t.Errorf("problems %q and %d patterns, want %d problems and none kept", problems, len(ex.pats), tc.want)
			}
		})
	}
}

func TestProjectSecret(t *testing.T) {
	kDev := make([]byte, 32)
	for i := range kDev {
		kDev[i] = byte(i)
	}
	cases := []struct {
		name string
		kDev []byte
		ok   bool
	}{
		{"a 32-byte K_dev gives a 32-byte secret", kDev, true},
		{"a K_dev of another length is refused", kDev[:16], false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, err := ProjectSecret(tc.kDev)
			if (err == nil) != tc.ok {
				t.Fatalf("err %v, want ok %v", err, tc.ok)
			}
			if tc.ok && (len(s) != SecretLen || bytes.Equal(s, tc.kDev)) {
				t.Errorf("secret %x, want 32 bytes derived from K_dev", s)
			}
		})
	}
}
