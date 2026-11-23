// SPDX-License-Identifier: AGPL-3.0-or-later

package agent

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/internal/agent/config"
	"github.com/some-du6e/pairtime/internal/agent/home"
	"github.com/some-du6e/pairtime/internal/agent/sched"
)

// TestWatchConfigLink runs the config watch over a config.toml that is a
// link into another folder, as a dotfiles one: a change to the file it
// points to, as pairtime privacy keep-text makes there, reaches the
// running agent, and so does one to the file it points to after the link
// is pointed elsewhere.
func TestWatchConfigLink(t *testing.T) {
	dir := t.TempDir()
	dirs := home.Dirs{Data: filepath.Join(dir, "data"), Config: filepath.Join(dir, "config"), Runtime: dir}
	first, second := filepath.Join(dir, "dotfiles", "pairtime.toml"), filepath.Join(dir, "elsewhere", "pairtime.toml")
	for _, name := range []string{first, second} {
		if err := os.MkdirAll(filepath.Dir(name), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(name, []byte("[privacy]\nkeep_prompt_text = true\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, d := range []string{dirs.Data, dirs.Config} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(first, dirs.ConfigFile()); err != nil {
		t.Skipf("this system can't make a symlink here: %v", err)
	}
	a := &agent{dirs: dirs, log: slog.New(slog.DiscardHandler), problems: map[string]string{}, sched: sched.New()}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go a.sched.Run(ctx)
	a.loadConfig(false)
	t.Cleanup(a.watchConfig())
	steps := []struct {
		name string
		do   func(t *testing.T)
		off  bool // "keep prompt text" is off once the agent has read config.toml again
	}{
		{
			name: "keep-text off, which changes the file the link points to, reaches the agent",
			do: func(t *testing.T) {
				if err := config.SetKeepText(dirs.ConfigFile(), false); err != nil {
					t.Fatal(err)
				}
			},
			off: true,
		},
		{
			name: "a link pointed at another file reaches the agent, which reads that file",
			do: func(t *testing.T) {
				if err := os.Remove(dirs.ConfigFile()); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(second, dirs.ConfigFile()); err != nil {
					t.Fatal(err)
				}
			},
			off: false,
		},
		{
			name: "a change to the file the link points to now reaches the agent",
			do: func(t *testing.T) {
				if err := config.SetKeepText(dirs.ConfigFile(), false); err != nil {
					t.Fatal(err)
				}
			},
			off: true,
		},
	}
	for _, st := range steps {
		t.Run(st.name, func(t *testing.T) {
			st.do(t)
			deadline := time.Now().Add(10 * time.Second)
			for a.privacy().TextOff != st.off {
				if time.Now().After(deadline) {
					t.Fatalf("after 10 s the agent has keep prompt text off %v, want %v", a.privacy().TextOff, st.off)
				}
				time.Sleep(20 * time.Millisecond)
			}
		})
	}
}
