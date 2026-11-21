// SPDX-License-Identifier: AGPL-3.0-or-later

package agent

import (
	"errors"
	"fmt"
	"io/fs"
	"strings"

	"github.com/some-du6e/pairtime/internal/agent/account"
	"github.com/some-du6e/pairtime/internal/agent/config"
	"github.com/some-du6e/pairtime/internal/agent/home"
	"github.com/some-du6e/pairtime/internal/agent/keys"
	"github.com/some-du6e/pairtime/internal/agent/ledger"
	"github.com/some-du6e/pairtime/internal/agent/uploader"
	"github.com/some-du6e/pairtime/pkg/event"
	"github.com/some-du6e/pairtime/pkg/score"
)

// modeOnline is config.toml's mode once `pairtime login` has signed the
// device in (docs/spec/device-auth-v1.md 8), config.Online. It stays an
// untyped constant, which the tests use as a config.Mode.
const modeOnline = "online"

// UploadConfig reads config.toml as uploads obey it: from the file itself
// at each call, so an edit the config watch misses, as to the target of a
// symlinked config.toml, takes effect at the next upload and the next
// sealed batch (device-auth-v1 8). Its mode is "online" only for a file
// that parses and says so. A file that can't be read, isn't TOML or has a
// value of the wrong type keeps the computer local-only until it's fixed,
// and broken says what is wrong with it: a typo can make pairtime more
// private, never less.
func UploadConfig(dirs home.Dirs) (cfg config.Config, broken string) {
	cfg, problems, parsed, err := config.LoadParsed(dirs.ConfigFile())
	switch {
	case err != nil && !errors.Is(err, fs.ErrNotExist):
		return cfg, err.Error()
	case !parsed:
		return cfg, strings.Join(problems, "; ")
	}
	return cfg, ""
}

// ConfigBroken is what doctor and `pairtime privacy show` say of a linked
// computer whose config.toml has an error (UploadConfig).
const ConfigBroken = "config.toml has an error, so this computer uploads nothing until it's fixed, and what pairtime records meanwhile stays on this computer"

// Linked reports whether `pairtime login` linked this computer to a
// server: whether account.json reads.
func Linked(dirs home.Dirs) bool {
	_, err := account.Load(dirs.Data)
	return err == nil
}

// localOnly is the uploader's LocalOnly: whether config.toml keeps the
// computer local-only right now, as the uploads obey it (UploadConfig).
// Batches sealed then never go, nor their minutes (D-local-gap), so a
// batch counts as local-only exactly when nothing could have uploaded it.
// It reads one small file, quick enough for the ledger writer's Sealed.
func (a *agent) localOnly() (local, known bool) {
	cfg, _ := UploadConfig(a.dirs)
	return string(cfg.Mode) != modeOnline, true
}

// credentials is the uploader's Login: the sign-in, while config.toml, as
// it reads now, says online (SignedIn). Doctor hears why a linked computer
// uploads nothing when config.toml has an error or its sign-in doesn't
// read.
func (a *agent) credentials() (uploader.Credentials, bool) {
	cfg, broken := UploadConfig(a.dirs)
	c, ok, err := SignedIn(a.dirs, cfg, a.keyStores)
	switch {
	case err != nil:
		a.setProblem("account", fmt.Sprintf("pairtime uploads nothing, because this computer is linked to a server but its sign-in doesn't read: %v", err))
	case broken != "" && Linked(a.dirs):
		a.setProblem("account", ConfigBroken+": "+broken)
	default:
		a.setProblem("account", "")
	}
	return c, ok
}

// SignedIn returns the sign-in that uploads go with, and false while there
// is none, by docs/spec/device-auth-v1.md 8's rule: an upload goes out
// only while config.toml's mode is "online" and the device keystore holds
// a token, and only to the server account.json names, never to one a
// later [server] url names. The agent's uploader asks it before every
// upload and `pairtime privacy show` before it prints one, so a local-only
// install sends nothing (DESIGN 4.2). stores are the keystore's stores,
// agent.Options.KeyStores. The error says why the sign-in of a computer
// that is linked doesn't read, as from a damaged keystore; it is nil when
// the computer isn't linked or config.toml keeps it local-only.
func SignedIn(dirs home.Dirs, cfg config.Config, stores []keys.Store) (uploader.Credentials, bool, error) {
	if string(cfg.Mode) != modeOnline {
		return uploader.Credentials{}, false, nil
	}
	return accountCredentials(dirs, stores)
}

// accountCredentials reads what `pairtime login` keeps, through package
// account: account.json's server, device id and linked_at, and the token
// and the device key's seed in the device keystore. It reads account.json
// first, so a computer that isn't linked opens no keystore, which at a
// first run would make K_dev. Tests replace it.
var accountCredentials = func(dirs home.Dirs, stores []keys.Store) (uploader.Credentials, bool, error) {
	if _, err := account.Load(dirs.Data); err != nil {
		if errors.Is(err, account.ErrNotLinked) {
			return uploader.Credentials{}, false, nil
		}
		return uploader.Credentials{}, false, err
	}
	ks, err := keys.Open(keys.Options{Dir: dirs.Data, Stores: stores})
	if err != nil {
		return uploader.Credentials{}, false, err
	}
	c, err := account.Open(dirs.Data, ks)
	if err != nil {
		if errors.Is(err, account.ErrNotLinked) {
			return uploader.Credentials{}, false, nil
		}
		return uploader.Credentials{}, false, err
	}
	return uploader.Credentials{Server: c.Server, Token: c.Token, DeviceID: c.DeviceID, Sign: uploader.KeySigner(c.Key), LinkedAt: c.LinkedAt}, true, nil
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
		Login:     uploader.LoginFunc(a.credentials),
		LocalOnly: a.localOnly,
		Params:    a.table,
		Device:    score.Device{},
		Started:   a.started,
		Sched:     a.sched,
		Flush:     flush,
		Problem:   func(msg string) { a.setProblem("uploader", msg) },
		Log:       a.log.With("component", "uploader"),
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
