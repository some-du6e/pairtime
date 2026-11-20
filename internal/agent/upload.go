// SPDX-License-Identifier: AGPL-3.0-or-later

package agent

import (
	"fmt"

	"github.com/some-du6e/pairtime/internal/agent/config"
	"github.com/some-du6e/pairtime/internal/agent/home"
	"github.com/some-du6e/pairtime/internal/agent/ledger"
	"github.com/some-du6e/pairtime/internal/agent/uploader"
	"github.com/some-du6e/pairtime/pkg/event"
	"github.com/some-du6e/pairtime/pkg/score"
)

// modeOnline is config.toml's mode once `pairtime login` has signed the
// device in (docs/spec/device-auth-v1.md 8). WP-SV3 adds it to package
// config; until then config.toml can only say "local".
const modeOnline = "online"

// SignedIn returns the sign-in that uploads go with, and false while there
// is none, by docs/spec/device-auth-v1.md 8's rule: an upload goes out
// only while config.toml's mode is "online" and the device keystore holds
// a token, and only to the server account.json names, never to one a
// later [server] url names. The agent's uploader asks it before every
// upload and `pairtime privacy show` before it prints one, so a local-only
// install sends nothing (DESIGN 4.2).
func SignedIn(dirs home.Dirs, cfg config.Config) (uploader.Credentials, bool) {
	if string(cfg.Mode) != modeOnline {
		return uploader.Credentials{}, false
	}
	return accountCredentials(dirs)
}

// accountCredentials reads what `pairtime login` keeps: the token and the
// device key's seed in the device keystore, and account.json's server and
// device id. TODO(WP-SV3): read them through internal/agent/account once
// it merges. Until then no device is signed in, so nothing is uploaded.
// Tests replace it.
var accountCredentials = func(home.Dirs) (uploader.Credentials, bool) {
	return uploader.Credentials{}, false
}

// startUploader makes and starts the uploader (WP-AG5) with the open
// ledger writer, and returns the step that stops it, which must run
// before the writer closes. Batches the writer sealed before the uploader
// existed are in batches.seg, which it reads when it first uploads.
func (a *agent) startUploader(w *ledger.Writer) func() {
	var flush func()
	if a.scorer != nil {
		flush = a.scorer.Flush
	}
	u, err := uploader.New(uploader.Options{
		Ledger:    w,
		LedgerDir: a.dirs.Ledger(),
		BucketDir: a.dirs.Buckets(),
		DataDir:   a.dirs.Data,
		State:     a.state,
		Login: uploader.LoginFunc(func() (uploader.Credentials, bool) {
			return SignedIn(a.dirs, a.config())
		}),
		Params:  a.table,
		Device:  score.Device{},
		Started: a.started,
		Sched:   a.sched,
		Flush:   flush,
		Problem: func(msg string) { a.setProblem("uploader", msg) },
		Log:     a.log.With("component", "uploader"),
	})
	if err != nil {
		a.setProblem("uploader", fmt.Sprintf("the agent uploads nothing, because its uploader can't start: %v", err))
		return func() {}
	}
	a.upload.Store(u)
	u.Start()
	return u.Close
}

// sealed hands each batch the ledger writer seals to the uploader and the
// scorer loop: it is ledger.Options.Sealed, so it must return quickly,
// and both only queue the batch.
func (a *agent) sealed(b ledger.Batch) {
	if u := a.upload.Load(); u != nil {
		u.Sealed(b)
	}
	if a.scorer != nil {
		a.scorer.Sealed(b)
	}
}

// sealAndUpload is presence's Seal: on lock and suspend it seals a batch,
// and once that is on disk the uploader sends at once (DESIGN 3.3, 4.3).
func (a *agent) sealAndUpload(op event.PowerOp) {
	a.ledgerIn.Seal(op)
	if u := a.upload.Load(); u != nil {
		u.Urgent()
	}
}
