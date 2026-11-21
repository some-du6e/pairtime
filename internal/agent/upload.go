// SPDX-License-Identifier: AGPL-3.0-or-later

package agent

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/some-du6e/pairtime/internal/agent/account"
	"github.com/some-du6e/pairtime/internal/agent/config"
	"github.com/some-du6e/pairtime/internal/agent/home"
	"github.com/some-du6e/pairtime/internal/agent/keys"
	"github.com/some-du6e/pairtime/internal/agent/ledger"
	"github.com/some-du6e/pairtime/internal/agent/uploader"
	"github.com/some-du6e/pairtime/internal/platform/clock"
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
// computer whose config.toml has an error (UploadConfig), and ConfigFixed
// what follows it: the server takes no skip without a new sign-in, so a
// batch sealed while the error lasted can stop the uploads after the fix
// too (D-local-gap), which doctor then says.
const (
	ConfigBroken = "config.toml has an error, so this computer uploads nothing until it's fixed, and what pairtime records meanwhile stays on this computer"
	ConfigFixed  = "once it's fixed, pairtime doctor says if uploading again also takes pairtime logout, then pairtime login"
)

// Linked reports whether `pairtime login` linked this computer to a
// server: whether account.json reads.
func Linked(dirs home.Dirs) bool {
	_, err := account.Load(dirs.Data)
	return err == nil
}

// online is the ledger writer's Online: whether config.toml lets the
// computer upload right now, as the uploads obey it (UploadConfig). The
// writer records it in each batch's seal frame, and batches sealed while
// it says no never go, nor their minutes (D-local-gap), so a batch counts
// as local-only exactly when nothing could have uploaded it. It reads one
// small file, quick enough for a seal, and calls nothing of the writer.
func (a *agent) online() bool {
	cfg, _ := UploadConfig(a.dirs)
	return string(cfg.Mode) == modeOnline
}

// modes is config.toml's mode as uploads obey it, as the agent has seen it
// since the ledger's start (startLedger), for the ledger writer: the
// start's read, the config watch's notes and its first look at each change
// (noteBack). A record made, or waiting to reach the writer, while it said
// local-only counts as made in local-only mode (ledger.Writer.Write), and
// the ledger may open long after the agent starts, as while a locked
// keyring keeps it shut, so until publishWriter gives the writer to the
// watch, modes keeps what it saw (ledger.Seen), and gives it to the writer
// as it opens and as it is published. From then on each note goes to the
// writer under mu, so none is lost or reordered with that hand-off. What
// it saw also goes to state.json as it changes (modesKey), so a run whose
// ledger never opened, or a kill, doesn't lose it for the next start.
type modes struct {
	mu sync.Mutex
	w  *ledger.Writer
	// local is set while the newest thing seen said local-only, and ended
	// is when the newest stretch seen ended, the moment the agent saw the
	// file back online, by Go's clock with its monotonic reading and by the
	// OS's (os), zero before any.
	local bool
	ended time.Time
	os    clock.Stamp
}

// modesKey is what modes saw last, in state.json (modesState).
const modesKey = "modes"

// modesState is what state.json keeps of modes: whether config.toml said
// local-only when the agent last looked, and when the newest stretch it
// saw ended, by the wall clock.
type modesState struct {
	Local bool      `json:"local,omitempty"`
	Ended time.Time `json:"ended,omitzero"`
}

// seeLocked takes config.toml's mode, seen now, and keeps a change of what
// modes saw in state.json. a.modes.mu is held.
func (a *agent) seeLocked(online bool) {
	m := &a.modes
	if online == !m.local {
		return
	}
	if online {
		m.ended, m.os = time.Now(), clock.Now()
	}
	m.local = !online
	if a.state == nil {
		return
	}
	err := a.state.Put(modesKey, modesState{Local: m.local, Ended: m.ended})
	if err == nil {
		err = a.state.Save()
	}
	if err != nil {
		a.log.Warn("couldn't keep config.toml's mode in state.json", "err", err)
	}
}

// startModes is what the ledger's start sees of config.toml's mode: every
// install starts in local mode, and that stretch counts from here
// (ledger.Writer.Write), so the records made before the teen's first
// pairtime login, and those waiting meanwhile, stay on the device. It goes
// on from what the last run saw (modesState): a stretch it saw end still
// counts, and one it saw going on lasted, as far as the agent knows, until
// now, so the hook calls held.jsonl or the spool kept from it are
// local-only too.
func (a *agent) startModes() {
	a.modes.mu.Lock()
	defer a.modes.mu.Unlock()
	var last modesState
	if a.state != nil {
		if _, err := a.state.Get(modesKey, &last); err != nil {
			last = modesState{} // rebuilt, like any damaged state
		}
	}
	a.modes.local, a.modes.ended = last.Local, last.Ended
	a.seeLocked(a.online())
}

// noteMode is the config watch's note of config.toml's mode, at its reload
// after the editors' steps: it tells the ledger writer, once published
// (ledger.Writer.NoteMode), and modes keeps it until then.
func (a *agent) noteMode() {
	online := a.online()
	a.modes.mu.Lock()
	defer a.modes.mu.Unlock()
	a.seeLocked(online)
	if w := a.modes.w; w != nil {
		w.NoteMode(online)
	}
}

// noteBack notes config.toml back online the moment the config watch hears
// of a change to it, before the reload's debounce: pairtime login writes
// the whole file and renames it into place (home.WriteFile), so the first
// look already reads it, and the records made just after the login don't
// count as made while it said local-only. Only a switch back is noted
// here, and only after one to local-only: a look in the middle of an
// editor's save can read local-only, as an empty file does, but never
// online when the saved file won't say so. A switch to local-only waits
// for the reload, and the seals read the file meanwhile.
func (a *agent) noteBack() {
	if !a.online() {
		return
	}
	a.modes.mu.Lock()
	defer a.modes.mu.Unlock()
	if !a.modes.local {
		return
	}
	a.seeLocked(true)
	if w := a.modes.w; w != nil {
		w.NoteMode(true)
	}
}

// seen is the ledger writer's Seen: what modes saw of config.toml's mode
// before the writer could hear the watch's notes.
func (a *agent) seen() ledger.Seen {
	a.modes.mu.Lock()
	defer a.modes.mu.Unlock()
	return a.seenLocked()
}

// seenLocked is seen, as a link the agent didn't see counts it: a record
// made before pairtime login linked the computer was made while
// config.toml said local, which every install starts in and pairtime
// logout goes back to. So when the newest stretch modes saw ended before
// the link, as when the agent started after the login, the stretch counts
// to the end of the second the link was made in, whose time account.json
// keeps cut to the second (D-relink-boundary). A switch back the agent saw
// comes after the link's time, which login takes before it writes
// config.toml, and counts as seen. a.modes.mu is held.
func (a *agent) seenLocked() ledger.Seen {
	s := ledger.Seen{Local: a.modes.local, Ended: a.modes.ended, OS: a.modes.os}
	if l, err := account.Load(a.dirs.Data); err == nil && !l.LinkedAt.IsZero() && s.Ended.Before(l.LinkedAt) {
		// The OS's clock wasn't read then.
		s.Ended, s.OS = l.LinkedAt.Add(time.Second), clock.Stamp{}
	}
	return s
}

// publishWriter makes w the writer the config watch's notes go to, and
// first gives it what modes saw since the ledger's start, so the records
// written once it is published already count by it.
func (a *agent) publishWriter(w *ledger.Writer) {
	a.modes.mu.Lock()
	defer a.modes.mu.Unlock()
	w.NoteSeen(a.seenLocked())
	a.modes.w = w
	a.writer.Store(w)
}

// reloadConfig is the config watch's reload: config.toml as the agent's
// settings, and its mode for the ledger writer.
func (a *agent) reloadConfig() {
	a.loadConfig(false)
	a.noteMode()
}

// uploadMode is config.toml's mode as uploads obey it, for GET /v1/status.
func (a *agent) uploadMode() config.Mode {
	cfg, _ := UploadConfig(a.dirs)
	return cfg.Mode
}

// credentials is the uploader's Login: the sign-in, while config.toml, as
// it reads now, says online, as SignedIn has it, from the agent's cache of
// it (signInCache). Doctor hears why a linked computer uploads nothing
// when config.toml has an error or its sign-in doesn't read.
func (a *agent) credentials() (uploader.Credentials, bool) {
	cfg, broken := UploadConfig(a.dirs)
	var (
		c   uploader.Credentials
		ok  bool
		err error
	)
	if string(cfg.Mode) == modeOnline {
		c, ok, err = a.signIn.get(a.dirs, a.keyStores)
	}
	switch {
	case err != nil:
		a.setProblem("account", fmt.Sprintf("pairtime uploads nothing, because this computer is linked to a server but its sign-in doesn't read: %v", err))
	case broken != "" && Linked(a.dirs):
		a.setProblem("account", ConfigBroken+" ("+broken+"); "+ConfigFixed)
	default:
		a.setProblem("account", "")
	}
	return c, ok
}

// signInCache keeps the agent's sign-in between uploads. The uploader asks
// for it at every wake and before every request, and each read of a
// keystore's secrets can wait for the teen to unlock a keyring, so it reads
// them again only when account.json or keystore.json changed, as pairtime
// login and logout change them, and after an error only once signInRetry
// has passed.
type signInCache struct {
	mu    sync.Mutex
	stamp string // signInStamp when c was read
	c     uploader.Credentials
	ok    bool
	err   error
	at    time.Time
}

// signInRetry is how long a sign-in that didn't read waits before the
// keystore is asked again, while nothing changed.
const signInRetry = 5 * time.Minute

// get returns the sign-in accountCredentials reads.
func (s *signInCache) get(dirs home.Dirs, stores []keys.Store) (uploader.Credentials, bool, error) {
	stamp := signInStamp(dirs)
	s.mu.Lock()
	defer s.mu.Unlock()
	if stamp == s.stamp && (s.err == nil || time.Since(s.at) < signInRetry) {
		return s.c, s.ok, s.err
	}
	c, ok, err := accountCredentials(dirs, stores)
	s.stamp, s.c, s.ok, s.err, s.at = stamp, c, ok, err, time.Now()
	return c, ok, err
}

// signInStamp is what pairtime login and logout change: account.json's
// bytes, and keystore.json's size and time (package keys keeps it as
// keys/keystore.json in the data directory).
func signInStamp(dirs home.Dirs) string {
	b := []byte("v1\x00")
	if data, err := os.ReadFile(account.File(dirs.Data)); err == nil {
		b = append(b, data...)
	}
	b = append(b, 0)
	if fi, err := os.Stat(filepath.Join(dirs.Data, "keys", "keystore.json")); err == nil {
		b = fmt.Appendf(b, "%d %d", fi.Size(), fi.ModTime().UnixNano())
	}
	return string(b)
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
	a.handEarly(u)
	u.Start()
	return u.Close
}

// earlyMax bounds the batches kept for the uploader before it exists. The
// writer seals only what a crash left before it, and maybe one more.
const earlyMax = 64

// handSealed is how the agent hands a sealed batch to its uploader. Tests
// replace it.
var handSealed = (*uploader.Uploader).Sealed

// handEarly hands u the batches sealed before it existed, and makes u the
// one sealed hands the rest to.
func (a *agent) handEarly(u *uploader.Uploader) {
	a.earlyMu.Lock()
	defer a.earlyMu.Unlock()
	for _, b := range a.early {
		handSealed(u, b)
	}
	a.early = nil
	a.upload.Store(u)
}

// sealed hands each batch the ledger writer seals to the uploader and the
// scorer loop: it is ledger.Options.Sealed, so it must return quickly,
// and both only queue the batch. A batch sealed before the uploader
// exists, as the one ledger.Open seals of what a crash left, waits for it
// (handEarly), so the harnesses its records name reach the labels. Its
// mode is in its seal frame, and so in its row in batches.seg, which the
// uploader reads when it first uploads: one past earlyMax isn't kept, and
// loses only its harnesses.
func (a *agent) sealed(b ledger.Batch) {
	a.earlyMu.Lock()
	u := a.upload.Load()
	if u == nil && len(a.early) < earlyMax {
		a.early = append(a.early, b)
	}
	a.earlyMu.Unlock()
	if u != nil {
		handSealed(u, b)
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
