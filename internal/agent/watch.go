// SPDX-License-Identifier: AGPL-3.0-or-later

package agent

import (
	"fmt"
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
// old one. If the watch can't start, edits apply at the next start, and the
// status says so; uploads follow config.toml's mode at once either way,
// since they read the file itself (UploadConfig). It returns the step that
// stops the watch.
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
				if filepath.Base(ev.Name) != name {
					continue
				}
				if reload == nil {
					reload = a.sched.After("config.reload", configDebounce, func(time.Time) { a.loadConfig(false) })
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
