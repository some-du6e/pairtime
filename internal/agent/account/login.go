// SPDX-License-Identifier: AGPL-3.0-or-later

package account

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"fmt"
	"net/http"
	"runtime"
	"time"

	"github.com/some-du6e/pairtime/internal/agent/config"
)

// ErrAlreadyLinked means this install is linked already; pairtime logout
// comes first.
var ErrAlreadyLinked = errors.New("account: this computer is already linked to a pairtime server")

// Options say where an install keeps its files and how to reach a server.
type Options struct {
	// Server is the server's base URL, as config.CheckServerURL writes it.
	Server string
	// DataDir is the install's data directory, where account.json goes.
	DataDir string
	// ConfigFile is config.toml, whose mode login and logout switch.
	ConfigFile string
	// Secrets is the device keystore.
	Secrets Secrets
	// HTTP makes the requests. Nil means a client with a 30 s timeout.
	HTTP *http.Client
	// UserAgent goes in every request, pairtime-cli/<version>.
	UserAgent string
	// Wait waits between polls. Nil means a timer.
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
	// ConfigErr is set when config.toml couldn't be switched, in which
	// case the teen sets the mode by hand; it says what to write.
	ConfigErr error
	// ServerErr is set when logout couldn't reach the server to remove
	// the device, which then stays listed on /account/devices until the
	// teen removes it there. Its token is forgotten here anyway.
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

// keyTries is how many times Login sends the key before it gives up.
const keyTries = 3

// Login links this install to o.Server as device d (RFC 8628;
// docs/spec/device-auth-v1.md). show gets the code as soon as the server
// gives one, to tell the teen where to type it; then Login polls until the
// teen answers. With the token it makes the device key, keeps both in the
// keystore, registers the key, writes account.json and switches
// config.toml to mode "online". A key the server won't take undoes the
// login: the keystore forgets the sign-in, and the server is asked to
// remove the device, so a second try starts clean.
//
// It returns ErrAlreadyLinked when account.json exists, ErrClosed when
// the server isn't taking sign-ins, and ErrDenied or ErrExpired when the
// code went unapproved.
func Login(ctx context.Context, o Options, d Device, show func(Code)) (Result, error) {
	if l, err := Load(o.DataDir); err == nil {
		return Result{Linked: l}, fmt.Errorf("%w (%s, device %s)", ErrAlreadyLinked, l.Server, l.DeviceID)
	} else if !errors.Is(err, ErrNotLinked) {
		return Result{}, err
	}
	c := o.client()
	code, err := c.RequestCode(ctx, d)
	if err != nil {
		return Result{}, err
	}
	show(code)
	tok, err := c.Poll(ctx, code)
	if err != nil {
		return Result{}, err
	}
	if tok.DeviceID != "" && tok.DeviceID != d.ID {
		return Result{}, fmt.Errorf("account: the server linked device %s, not this computer's %s", tok.DeviceID, d.ID)
	}

	seed := make([]byte, ed25519.SeedSize)
	defer clear(seed)
	rand.Read(seed)
	key := ed25519.NewKeyFromSeed(seed)
	if err := o.Secrets.SetAccount([]byte(tok.AccessToken), seed); err != nil {
		undo(ctx, c, o, tok.AccessToken)
		return Result{}, fmt.Errorf("account: keeping the sign-in in the keystore: %w", err)
	}
	if err := registerKey(ctx, c, tok.AccessToken, d.ID, key); err != nil {
		undo(ctx, c, o, tok.AccessToken)
		return Result{}, fmt.Errorf("account: registering the device key: %w", err)
	}
	l := Linked{
		Server:    o.Server,
		DeviceID:  d.ID,
		PublicKey: encodeKey(key.Public().(ed25519.PublicKey)),
		LinkedAt:  o.now().UTC().Truncate(time.Second),
	}
	if err := save(o.DataDir, l); err != nil {
		undo(ctx, c, o, tok.AccessToken)
		return Result{}, fmt.Errorf("account: writing %s: %w", FileName, err)
	}
	l.V = fileVersion
	r := Result{Linked: l}
	if err := config.SetOnline(o.ConfigFile, o.Server); err != nil {
		r.ConfigErr = err
	}
	return r, nil
}

// registerKey sends the key, trying again after a network error. A
// refusal from the server is final.
func registerKey(ctx context.Context, c *Client, token, deviceID string, key ed25519.PrivateKey) error {
	var err error
	for i := range keyTries {
		if i > 0 {
			if werr := c.wait(ctx, time.Duration(i)*2*time.Second); werr != nil {
				return werr
			}
		}
		err = c.RegisterKey(ctx, token, deviceID, key)
		var se *ServerError
		if err == nil || errors.As(err, &se) || ctx.Err() != nil {
			return err
		}
	}
	return err
}

// undo takes back a login that failed after the server handed out its
// token: it asks the server to remove the device, and forgets the sign-in.
// Both are best effort, since the login already failed.
func undo(ctx context.Context, c *Client, o Options, token string) {
	rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	c.RemoveDevice(rctx, token)
	o.Secrets.ForgetAccount()
}

// Logout unlinks this install: it asks the server to remove the device,
// which stops its token at once, then forgets the token and the device
// key, deletes account.json and switches config.toml back to mode
// "local", whatever the server answered. The ledger and everything else
// on the computer stay. account.json goes first, so uploads stop even when
// the keystore can't be reached to forget the sign-in.
//
// It returns ErrNotLinked, after cleaning up anything left of a sign-in,
// when this install wasn't linked.
func Logout(ctx context.Context, o Options) (Result, error) {
	l, lerr := Load(o.DataDir)
	if lerr != nil && !errors.Is(lerr, ErrNotLinked) {
		return Result{}, lerr
	}
	var r Result
	r.Linked = l
	if lerr == nil {
		token, seed, err := o.Secrets.Account()
		clear(seed)
		switch {
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
		case !isNotFound(err):
			r.ServerErr = fmt.Errorf("account: the keystore didn't give the token to send: %w", err)
		}
		if err := remove(o.DataDir); err != nil {
			return r, fmt.Errorf("account: deleting %s: %w", FileName, err)
		}
	}
	if err := o.Secrets.ForgetAccount(); err != nil {
		return r, fmt.Errorf("account: forgetting the sign-in in the keystore: %w", err)
	}
	if err := config.SetLocal(o.ConfigFile); err != nil {
		r.ConfigErr = err
	}
	if lerr != nil {
		return r, lerr
	}
	return r, nil
}
