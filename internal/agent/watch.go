// SPDX-License-Identifier: AGPL-3.0-or-later

package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/fsnotify/fsnotify"

	"github.com/some-du6e/pairtime/internal/agent/sched"
)

// configDebounce is how long after the last change to config.toml the agent
// reads it, because editors save in several steps.
const configDebounce = 300 * time.Millisecond

// watchConfig reloads config.toml soon after it changes, so that, for one,
// switching an opt-in off takes effect at once. It watches the directory,
// not the file, because many editors save by renaming a new file over the
// old one. A config.toml that is a link, as into a dotfiles folder, changes
// where it points, as pairtime privacy keep-text and pairtime login change
// it (config.SetKeepText), so the folder of the file it points to is
// watched too, and the link followed again at each change of config.toml.
// If the watch can't start, edits apply at the next start, and the status
// says so; uploads follow config.toml's mode at once either way, since they
// read the file itself (UploadConfig). It returns the step that stops the
// watch.
func (a *agent) watchConfig() func() {
	w, err := fsnotify.NewWatcher()
	if err == nil {
		if err = w.Add(a.dirs.Config); err != nil {
			w.Close()
		}
	}
	if err != nil {
		a.setProblem("config-watch", fmt.Sprintf("changes to config.toml apply at the next start, apart from its mode, which uploads follow at once, because watching it failed: %v", err))
		return func() {}
	}
	name := filepath.Base(a.dirs.ConfigFile())
	// target is the file config.toml links to, "" while it isn't a link
	// whose folder the watch has. Only the watch's goroutine touches it.
	target := ""
	follow := func() {
		t := linkTarget(a.dirs.ConfigFile())
		if t == target {
			return
		}
		if target != "" && filepath.Dir(target) != filepath.Clean(a.dirs.Config) {
			w.Remove(filepath.Dir(target))
		}
		target = ""
		if t == "" {
			return
		}
		if dir := filepath.Dir(t); dir != filepath.Clean(a.dirs.Config) {
			if err := w.Add(dir); err != nil {
				a.log.Warn("watching the folder config.toml links to, so its changes apply at the next start", "err", err)
				return
			}
		}
		target = t
	}
	follow()
	done := make(chan struct{})
	go func() {
		defer close(done)
		var reload *sched.Job // only this goroutine touches it
		for {
			select {
			case ev, ok := <-w.Events:
				if !ok {
					return
				}
				switch {
				case filepath.Base(ev.Name) == name && filepath.Dir(ev.Name) == filepath.Clean(a.dirs.Config):
					// config.toml itself, or the link, which may point
					// elsewhere now.
					follow()
				case target != "" && filepath.Clean(ev.Name) == target:
				default:
					continue
				}
				// A switch back online is noted at the first look, so the
				// records just after pairtime login count as online, and the
				// rest waits for the reload (noteBack).
				a.noteBack()
				if reload == nil {
					reload = a.sched.After("config.reload", configDebounce, func(time.Time) { a.reloadConfig() })
				} else {
					reload.Reset(configDebounce)
				}
			case err, ok := <-w.Errors:
				if !ok {
					return
				}
				a.log.Warn("watching config.toml", "err", err)
			}
		}
	}()
	return func() {
		w.Close()
		<-done
	}
}

// linkTarget returns the file the link name points to, after every link
// on the way, and "" when name isn't a link or its target can't be found.
func linkTarget(name string) string {
	fi, err := os.Lstat(name)
	if err != nil || fi.Mode()&os.ModeSymlink == 0 {
		return ""
	}
	t, err := filepath.EvalSymlinks(name)
	if err != nil {
		return ""
	}
	return filepath.Clean(t)
}
