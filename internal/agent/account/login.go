// SPDX-License-Identifier: AGPL-3.0-or-later

package account

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/some-du6e/pairtime/internal/agent/config"
	"github.com/some-du6e/pairtime/internal/agent/home"
)

// The errors of login and logout.
var (
	// ErrAlreadyLinked means this install is linked already; pairtime
	// logout comes first.
	ErrAlreadyLinked = errors.New("account: this computer is already linked to a pairtime server")
	// ErrBusy means another pairtime login or logout is running on this
	// install.
	ErrBusy = errors.New("account: another pairtime login or logout is running on this computer")
	// ErrNoKeystore means logout couldn't read the device's sign-in from
	// the keystore, as with a locked keyring, so it couldn't tell the
	// server or forget the token. It switched config.toml to local, so
	// nothing uploads, and kept account.json, so running it again once the
	// keystore opens finishes the job.
	ErrNoKeystore = errors.New("account: the device keystore couldn't be read")
)

// Options say where an install keeps its files and how to reach a server.
type Options struct {
	// Server is the server's base URL, as config.CheckServerURL writes it.
	Server string
	// DataDir is the install's data directory, where account.json goes.
	DataDir string
	// ConfigFile is config.toml, whose mode login and logout switch.
	ConfigFile string
	// Secrets is the device keystore, or nil for logout when it couldn't
	// be opened.
	Secrets Secrets
	// HTTP makes the requests. Nil means a client with a 30 s timeout.
	// Redirects are never followed, whatever it says.
	HTTP *http.Client
	// UserAgent goes in every request, pairtime-cli/<version>.
	UserAgent string
	// Wait waits between polls and retries. Nil means a timer.
	Wait func(ctx context.Context, d time.Duration) error
	// Now returns the current time. Nil means time.Now.
	Now func() time.Time
}

func (o Options) client() *Client {
	return &Client{Server: o.Server, HTTP: o.HTTP, UserAgent: o.UserAgent, Wait: o.Wait}
}

func (o Options) now() time.Time {
	if o.Now != nil {
		return o.Now()
	}
	return time.Now()
}

// Result is how a login or a logout went, beyond its error.
type Result struct {
	Linked Linked
	// Account names the account a login linked to, as the server's pages
	// show it: a Slack ID, or a Hack Club Auth ID.
	Account string
	// ConfigErr is set when config.toml couldn't be switched, in which
	// case the teen sets the mode by hand; it says what to write.
	ConfigErr error
	// ServerErr is set when logout couldn't remove the device on the
	// server, which then stays listed on /account/devices until the teen
	// removes it there. Its token is forgotten here anyway.
	ServerErr error
}

// OSFamily is this computer's OS family, as the server wants it.
func OSFamily() string {
	switch runtime.GOOS {
	case "windows":
		return "windows"
	case "darwin":
		return "macos"
	case "linux":
		return "linux"
	}
	return "other"
}

// Build returns version, a pairtime version, when the server can take it
// as a client build, and "unknown" otherwise.
func Build(version string) string {
	if version == "" || len(version) > 64 {
		return "unknown"
	}
	for i := 0; i < len(version); i++ {
		c := version[i]
		if !('a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' || '0' <= c && c <= '9' || c == '.' || c == '_' || c == '+' || c == '-') {
			return "unknown"
		}
	}
	return version
}

// lockDir is the directory in the data directory whose lock login and
// logout hold, so two of them at once can't leave one login's token with
// another's account.json, or forget a token another just kept.
const lockDir = "account"

// lock takes the lock login and logout share, or returns ErrBusy at once.
func lock(dataDir string) (func(), error) {
	dir := filepath.Join(dataDir, lockDir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	l, err := home.Acquire(dir)
	if errors.Is(err, home.ErrLocked) {
		return nil, ErrBusy
	}
	if err != nil {
		return nil, err
	}
	return func() { l.Release() }, nil
}

// Login links this install to o.Server as device d (RFC 8628;
// docs/spec/device-auth-v1.md). It makes the device key first and keeps
// it in the keystore, then asks for a code that names the key, since the
// token the code yields works only with that key (D-key-at-grant). show
// gets the code as soon as the server gives one, to tell the teen where to
// type it; then Login polls until the teen answers. With the token, which
// must name this device and key, it keeps the token beside the key, writes
// account.json and switches config.toml to mode "online". A login that
// fails leaves no sign-in behind: the keystore forgets the key, and once
// the server has handed out a token, the server is asked to remove the
// device too. Login holds the lock it shares with Logout throughout.
//
// It returns ErrAlreadyLinked when account.json exists, ErrBusy when
// another login or logout runs, ErrClosed when the server isn't taking
// sign-ins, ErrDenied or ErrExpired when the code went unapproved, and
// ErrTaken when an account other than the one the device id belongs to
// approved it.
func Login(ctx context.Context, o Options, d Device, show func(Code)) (Result, error) {
	unlock, err := lock(o.DataDir)
	if err != nil {
		return Result{}, err
	}
	defer unlock()
	if l, err := Load(o.DataDir); err == nil {
		return Result{Linked: l}, fmt.Errorf("%w (%s, device %s)", ErrAlreadyLinked, l.Server, l.DeviceID)
	} else if !errors.Is(err, ErrNotLinked) {
		return Result{}, fmt.Errorf("%w; pairtime logout clears it", err)
	}

	seed := make([]byte, ed25519.SeedSize)
	defer clear(seed)
	rand.Read(seed)
	key := ed25519.NewKeyFromSeed(seed)
	pub := encodeKey(key.Public().(ed25519.PublicKey))
	if err := o.Secrets.SetSignKey(seed); err != nil {
		return Result{}, fmt.Errorf("account: keeping the device key in the keystore: %w", err)
	}
	c := o.client()
	code, err := c.RequestCode(ctx, d, key)
	if err != nil {
		o.Secrets.ForgetAccount()
		return Result{}, err
	}
	show(code)
	tok, err := c.Poll(ctx, code)
	if err != nil {
		o.Secrets.ForgetAccount()
		return Result{}, err
	}
	switch {
	case tok.DeviceID != "" && tok.DeviceID != d.ID:
		undo(ctx, c, o, tok.AccessToken)
		return Result{}, fmt.Errorf("account: the server linked device %q, not this computer's %s", tok.DeviceID, d.ID)
	case tok.PublicKey != "" && tok.PublicKey != pub:
		undo(ctx, c, o, tok.AccessToken)
		return Result{}, errors.New("account: the server's token is for another device key than the one this computer sent")
	}
	if err := o.Secrets.SetAccount([]byte(tok.AccessToken), seed); err != nil {
		undo(ctx, c, o, tok.AccessToken)
		return Result{}, fmt.Errorf("account: keeping the sign-in in the keystore: %w", err)
	}
	l := Linked{
		Server:    o.Server,
		DeviceID:  d.ID,
		PublicKey: pub,
		LinkedAt:  o.now().UTC().Truncate(time.Second),
	}
	if err := save(o.DataDir, l); err != nil {
		undo(ctx, c, o, tok.AccessToken)
		return Result{}, fmt.Errorf("account: writing %s: %w", FileName, err)
	}
	l.V = fileVersion
	r := Result{Linked: l, Account: tok.Account}
	if err := config.SetOnline(o.ConfigFile, o.Server); err != nil {
		r.ConfigErr = err
	}
	return r, nil
}

// undo takes back a login that failed after the server handed out its
// token: it asks the server to remove the device, and forgets the sign-in.
// Both are best effort, since the login already failed. The caller holds
// the lock, so the sign-in it forgets is this login's.
func undo(ctx context.Context, c *Client, o Options, token string) {
	rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	c.RemoveDevice(rctx, token)
	o.Secrets.ForgetAccount()
}

// Logout unlinks this install: it asks the server to remove the device,
// which stops its token at once, then deletes account.json, forgets the
// token and the device key and switches config.toml back to mode
// "local", whatever the server answered. The ledger and everything else
// on the computer stay. It holds the lock it shares with Login
// throughout.
//
// The token goes only to the server account.json names, and only when the
// keystore's key is the one account.json says was registered. When the
// keystore can't be read (o.Secrets nil, or Account failing), Logout only
// switches config.toml to local, so nothing uploads, keeps account.json,
// so a later logout can still tell the server, and returns ErrNoKeystore.
// A damaged account.json is deleted like a good one; its server isn't
// known then, so ServerErr says the device wasn't removed there. When ctx
// ends before Logout changes anything here, it returns ctx.Err() and
// changes nothing.
//
// It returns ErrNotLinked, after forgetting anything left of a sign-in,
// when this install wasn't linked.
func Logout(ctx context.Context, o Options) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	unlock, err := lock(o.DataDir)
	if err != nil {
		return Result{}, err
	}
	defer unlock()
	l, lerr := Load(o.DataDir)
	r := Result{Linked: l}
	damaged := lerr != nil && !errors.Is(lerr, ErrNotLinked)
	stopUploads := func() {
		if err := config.SetLocal(o.ConfigFile); err != nil {
			r.ConfigErr = err
		}
	}
	switch {
	case damaged:
		r.ServerErr = fmt.Errorf("account: %w, so pairtime doesn't know which server to remove this computer from", lerr)
		if o.Secrets == nil {
			stopUploads()
			if err := remove(o.DataDir); err != nil {
				return r, fmt.Errorf("account: deleting %s: %w", FileName, err)
			}
			return r, ErrNoKeystore
		}
	case lerr != nil:
		// Not linked: forget whatever a sign-in left, if the keystore
		// can be read.
		if o.Secrets == nil {
			return r, lerr
		}
	case o.Secrets == nil:
		stopUploads()
		return r, ErrNoKeystore
	default:
		token, seed, err := o.Secrets.Account()
		var pub ed25519.PublicKey
		if err == nil {
			pub = ed25519.NewKeyFromSeed(seed).Public().(ed25519.PublicKey)
		}
		clear(seed)
		if cerr := ctx.Err(); cerr != nil {
			clear(token)
			return r, cerr
		}
		switch {
		case err == nil && l.PublicKey != "" && encodeKey(pub) != l.PublicKey:
			r.ServerErr = errors.New("account: the keystore's sign-in isn't the one account.json says this computer registered, so pairtime didn't send it")
		case err == nil:
			c := o.client()
			c.Server = l.Server
			rctx, cancel := context.WithTimeout(ctx, 20*time.Second)
			r.ServerErr = c.RemoveDevice(rctx, string(token))
			cancel()
			var se *ServerError
			if errors.As(r.ServerErr, &se) && se.Status == http.StatusUnauthorized {
				// The server had removed it already.
				r.ServerErr = nil
			}
			if cerr := ctx.Err(); cerr != nil {
				clear(token)
				return r, cerr
			}
		case !isNotFound(err):
			stopUploads()
			return r, fmt.Errorf("%w: %v", ErrNoKeystore, err)
		}
		clear(token)
	}
	if lerr == nil || damaged {
		if err := remove(o.DataDir); err != nil {
			stopUploads()
			return r, fmt.Errorf("account: deleting %s: %w", FileName, err)
		}
	}
	if err := o.Secrets.ForgetAccount(); err != nil {
		stopUploads()
		return r, fmt.Errorf("account: forgetting the sign-in in the keystore: %w", err)
	}
	stopUploads()
	if lerr != nil && !damaged {
		return r, lerr
	}
	return r, nil
}
