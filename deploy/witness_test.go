// SPDX-License-Identifier: AGPL-3.0-or-later

package deploy

import (
	"errors"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// witnessRig is one run of the witness sidecar's loop: the directory of
// roots pairtimed writes, the sidecar's clone, and a bare repository on
// disk in place of the public witness repository.
type witnessRig struct {
	script, src, work, remote string
}

// env is the environment the loop runs one round in, with no git
// configuration but its own.
func (w *witnessRig) env() []string {
	return []string{
		"PATH=" + os.Getenv("PATH"),
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_GLOBAL=" + os.DevNull,
		"PAIRTIME_WITNESS_REPO=some-du6e/pairtime-witness",
		"PAIRTIME_WITNESS_REMOTE=" + w.remote,
		"PAIRTIME_WITNESS_SRC=" + w.src,
		"PAIRTIME_WITNESS_WORK=" + w.work,
		"PAIRTIME_WITNESS_ONCE=1",
	}
}

func (w *witnessRig) git(t *testing.T, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"--git-dir=" + w.remote}, args...)...)
	cmd.Env = w.env()
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return string(out)
}

// root writes a root as pairtimed would, at path under the roots.
func (w *witnessRig) root(t *testing.T, path, text string) {
	t.Helper()
	p := filepath.Join(w.src, filepath.FromSlash(path))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

// refusePushes makes the repository refuse every push until allowPushes,
// as when the deploy key lacks write access or the network drops.
func (w *witnessRig) refusePushes(t *testing.T) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(w.remote, "hooks", "pre-receive"), []byte("#!/bin/sh\necho refused >&2\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
}

func (w *witnessRig) allowPushes(t *testing.T) {
	t.Helper()
	if err := os.Remove(filepath.Join(w.remote, "hooks", "pre-receive")); err != nil {
		t.Fatal(err)
	}
}

// round runs one round of the loop and reports whether it exited 0.
func (w *witnessRig) round(t *testing.T) (bool, string) {
	t.Helper()
	cmd := exec.Command("sh", w.script)
	cmd.Env = w.env()
	out, err := cmd.CombinedOutput()
	var exit *exec.ExitError
	if err != nil && !errors.As(err, &exit) {
		t.Fatal(err)
	}
	return err == nil, string(out)
}

// published returns each file on the repository's main branch and its
// text, and how many commits main has.
func (w *witnessRig) published(t *testing.T) (map[string]string, int) {
	t.Helper()
	files := make(map[string]string)
	if strings.TrimSpace(w.git(t, "for-each-ref", "refs/heads/main")) == "" {
		return files, 0
	}
	for _, name := range strings.Fields(w.git(t, "ls-tree", "-r", "--name-only", "main")) {
		files[name] = w.git(t, "show", "main:"+name)
	}
	n, err := strconv.Atoi(strings.TrimSpace(w.git(t, "rev-list", "--count", "main")))
	if err != nil {
		t.Fatal(err)
	}
	return files, n
}

// TestWitnessPush runs witness/push.sh, the sidecar's loop, one round at a
// time against a bare repository on disk. Each case sets the roots and the
// repository up, then runs rounds, each after its own change.
func TestWitnessPush(t *testing.T) {
	for _, tool := range []string{"sh", "git"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s isn't installed, and the sidecar's image has it: %v", tool, err)
		}
	}
	script, err := filepath.Abs(filepath.Join("witness", "push.sh"))
	if err != nil {
		t.Fatal(err)
	}
	type round struct {
		// before changes the roots or the repository first.
		before func(t *testing.T, w *witnessRig)
		ok     bool
	}
	tests := []struct {
		name   string
		setup  func(t *testing.T, w *witnessRig)
		rounds []round
		// want is every file the repository holds afterwards, and
		// wantCommits how many commits main has.
		want        map[string]string
		wantCommits int
	}{
		{
			name: "a round publishes the roots the repository lacks and leaves out dot-files, such as a root pairtimed is still writing",
			setup: func(t *testing.T, w *witnessRig) {
				w.root(t, "2026/2026-11-19.json", "{\"day\":19}\n")
				w.root(t, "2026/.2026-11-20.json.tmp", "{\"da")
				w.root(t, ".partial/2026-11-21.json", "{}\n")
			},
			rounds:      []round{{ok: true}},
			want:        map[string]string{"2026/2026-11-19.json": "{\"day\":19}\n"},
			wantCommits: 1,
		},
		{
			name:  "after a failed first push to an empty repository, the next round pushes the roots again",
			setup: func(t *testing.T, w *witnessRig) { w.root(t, "2026/2026-11-19.json", "{\"day\":19}\n") },
			rounds: []round{
				{before: func(t *testing.T, w *witnessRig) { w.refusePushes(t) }, ok: false},
				{before: func(t *testing.T, w *witnessRig) { w.allowPushes(t) }, ok: true},
			},
			want:        map[string]string{"2026/2026-11-19.json": "{\"day\":19}\n"},
			wantCommits: 1,
		},
		{
			name:  "after a failed push to a repository that has a branch, the next round pushes what the failed one held",
			setup: func(t *testing.T, w *witnessRig) { w.root(t, "2026/2026-11-19.json", "{\"day\":19}\n") },
			rounds: []round{
				{ok: true},
				{before: func(t *testing.T, w *witnessRig) {
					w.root(t, "2026/2026-11-20.json", "{\"day\":20}\n")
					w.refusePushes(t)
				}, ok: false},
				{before: func(t *testing.T, w *witnessRig) { w.allowPushes(t) }, ok: true},
			},
			want:        map[string]string{"2026/2026-11-19.json": "{\"day\":19}\n", "2026/2026-11-20.json": "{\"day\":20}\n"},
			wantCommits: 2,
		},
		{
			name:  "a published root is never replaced by a later, different copy of it",
			setup: func(t *testing.T, w *witnessRig) { w.root(t, "2026/2026-11-19.json", "{\"day\":19}\n") },
			rounds: []round{
				{ok: true},
				{before: func(t *testing.T, w *witnessRig) { w.root(t, "2026/2026-11-19.json", "{\"changed\":true}\n") }, ok: true},
			},
			want:        map[string]string{"2026/2026-11-19.json": "{\"day\":19}\n"},
			wantCommits: 1,
		},
		{
			name:        "a round that finds nothing new pushes no commit",
			setup:       func(t *testing.T, w *witnessRig) { w.root(t, "2026/2026-11-19.json", "{\"day\":19}\n") },
			rounds:      []round{{ok: true}, {ok: true}},
			want:        map[string]string{"2026/2026-11-19.json": "{\"day\":19}\n"},
			wantCommits: 1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			w := &witnessRig{script: script, src: filepath.Join(dir, "witness"), work: filepath.Join(dir, "repo"), remote: filepath.Join(dir, "remote.git")}
			if err := os.Mkdir(w.src, 0o755); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command("git", "init", "--quiet", "--bare", w.remote)
			cmd.Env = w.env()
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("git init: %v\n%s", err, out)
			}
			tt.setup(t, w)
			for i, r := range tt.rounds {
				if r.before != nil {
					r.before(t, w)
				}
				if ok, out := w.round(t); ok != r.ok {
					t.Fatalf("round %d exited ok=%v, want %v:\n%s", i+1, ok, r.ok, out)
				}
			}
			got, commits := w.published(t)
			if !maps.Equal(got, tt.want) {
				t.Errorf("the repository holds %q, want %q", got, tt.want)
			}
			if commits != tt.wantCommits {
				t.Errorf("main has %d commits, want %d", commits, tt.wantCommits)
			}
		})
	}
}
