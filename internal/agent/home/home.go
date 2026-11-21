// SPDX-License-Identifier: AGPL-3.0-or-later

// Package home finds and guards the directories the client keeps its files
// in: where they are on each OS (DESIGN 3.5; CP 7), the lock that lets one
// agent at a time use them, and the atomic writes that every small file
// goes through.
package home

import (
	"fmt"
	"os"
	"path/filepath"
)

// EnvHome names the environment variable that, set to an absolute path,
// puts every pairtime directory there instead of in the OS's usual places.
// CI uses it for a throwaway agent (footprint.yml), and it lets someone run
// a second, separate pairtime on one account.
const EnvHome = "PAIRTIME_HOME"

// Dirs are the directories of one pairtime installation.
type Dirs struct {
	// Data holds the stores of DESIGN 3.5 (ledger, vault, buckets, days,
	// coach, inbox, state.json, port.json) and the agent's lock.
	Data string
	// Config holds config.toml. It is the OS's config directory on Windows
	// and Linux, and Data on macOS and under PAIRTIME_HOME (CP 7).
	Config string
	// Runtime is where the agent's socket goes (CP 6.3; WP-AG2): a per-user
	// directory the OS clears where there is one, and Data otherwise.
	Runtime string
}

// Default returns this user's directories on this OS, honoring
// PAIRTIME_HOME.
func Default() (Dirs, error) {
	userHome, err := os.UserHomeDir()
	if err != nil {
		userHome = "" // not every OS needs it; osDirs reports it missing when it does
	}
	return resolve(os.Getenv, userHome)
}

func resolve(getenv func(string) string, userHome string) (Dirs, error) {
	if h := getenv(EnvHome); h != "" {
		if !filepath.IsAbs(h) {
			return Dirs{}, fmt.Errorf("%s must be an absolute path, not %q", EnvHome, h)
		}
		h = filepath.Clean(h)
		return Dirs{Data: h, Config: h, Runtime: h}, nil
	}
	return osDirs(getenv, userHome)
}

// Create makes the data and config directories, and the parents they need,
// for this user only: mode 0700 on Unix, and on Windows a protected DACL
// that lets only the current user in and that every file made inside
// inherits. Directories that exist are left as they are. The runtime
// directory is the socket's business (WP-AG2).
func (d Dirs) Create() error {
	for _, dir := range []string{d.Data, d.Config} {
		if err := mkdirAll(dir); err != nil {
			return err
		}
	}
	return nil
}

// ConfigFile is config.toml, the settings a teen can edit.
func (d Dirs) ConfigFile() string { return filepath.Join(d.Config, "config.toml") }

// StateFile is state.json, what the agent resumes from.
func (d Dirs) StateFile() string { return filepath.Join(d.Data, "state.json") }

// PortFile is port.json, with the local HTTP port and bearer token.
func (d Dirs) PortFile() string { return filepath.Join(d.Data, "port.json") }

// The rest of DESIGN 3.5's table, named here so the whole layout is in one
// place. The packages that write these directories create them.

// Ledger holds the ledger segments and batches.seg.
func (d Dirs) Ledger() string { return filepath.Join(d.Data, "ledger") }

// Vault holds the encrypted prompt and output text.
func (d Dirs) Vault() string { return filepath.Join(d.Data, "vault") }

// Buckets holds the scorer's minute buckets.
func (d Dirs) Buckets() string { return filepath.Join(d.Data, "buckets") }

// Days holds the per-project day rollups.
func (d Dirs) Days() string { return filepath.Join(d.Data, "days") }

// Coach holds recaps, moments, the logbook and devlog drafts.
func (d Dirs) Coach() string { return filepath.Join(d.Data, "coach") }

// Inbox holds hook calls made while the agent was down, and plugin events.
func (d Dirs) Inbox() string { return filepath.Join(d.Data, "inbox") }

// Commits holds the commit records of DESIGN 4.3 that the git adapter made,
// a file per UTC day, until the uploader sends them (WP-AG7,
// D-commit-records-local). They hold tier-1 fields only: no commit message
// and no path.
func (d Dirs) Commits() string { return filepath.Join(d.Data, "commits") }

// Held holds the hook calls the agent took in before it could read them:
// before its keystore and its ledger were open, its adapters have no keys
// to key them with and no ledger to write them to (WP-AG7). Like the
// inbox, it holds what the harness sent, prompts included.
func (d Dirs) Held() string { return filepath.Join(d.Data, "held.jsonl") }
