// SPDX-License-Identifier: AGPL-3.0-or-later

package account

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/some-du6e/pairtime/internal/agent/keys"
	"github.com/some-du6e/pairtime/internal/devtoken"
)

// ClientID is the pairtime app's client id for the device grant.
const ClientID = "pairtime"

// The server's endpoints (docs/spec/device-auth-v1.md).
const (
	codePath  = "/oauth/device/code"
	tokenPath = "/oauth/token"
	mePath    = "/api/v1/devices/me"
	grantType = "urn:ietf:params:oauth:grant-type:device_code"
	// keyLabel starts the message the device key signs when the app asks
	// for a code.
	keyLabel = "pairtime-device-key-v1\n"
)

// The device grant's timing on the client's side (RFC 8628 3.2, 3.5).
const (
	// defaultInterval is the wait between polls when the server names
	// none.
	defaultInterval = 5 * time.Second
	// slowDownStep is what each slow_down adds to the wait.
	slowDownStep = 5 * time.Second
	// maxBackoff bounds the wait after network errors, and after answers
	// that aren't the server's, which double it.
	maxBackoff = time.Minute
)

// The errors of the device grant.
var (
	// ErrDenied means the code was turned down on the server's page.
	ErrDenied = errors.New("account: the code was turned down, so this computer isn't linked")
	// ErrExpired means the code expired before anyone approved it, or the
	// server restarted meanwhile.
	ErrExpired = errors.New("account: the code expired before anyone approved it")
	// ErrClosed means the server isn't taking sign-ins yet.
	ErrClosed = errors.New("account: this pairtime server isn't taking sign-ins yet")
	// ErrTaken means the account that approved the code isn't the one this
	// computer's device id belongs to on the server, which keeps the id
	// with that account while it holds what the computer uploaded. Only
	// that account can link it, and any other only as a new device, with
	// a new device id and key (D-device-account-binding; WP-AG5's
	// new-device rule).
	ErrTaken = errors.New("account: the code was approved by an account other than the one this computer's device ID belongs to")
)

// ServerError is an error the server answered with (RFC 6749 5.2).
type ServerError struct {
	Status      int
	Code        string
	Description string
}

func (e *ServerError) Error() string {
	msg := fmt.Sprintf("account: the server answered %d", e.Status)
	if e.Code != "" {
		msg += " " + e.Code
	}
	if e.Description != "" {
		msg += ": " + e.Description
	}
	return msg
}

// Client talks to one pairtime server.
type Client struct {
	// Server is the server's base URL.
	Server string
	// HTTP makes the requests. Nil means a client with a 30 s timeout.
	HTTP *http.Client
	// UserAgent goes in every request, pairtime-cli/<version>.
	UserAgent string
	// Wait waits d, or until ctx ends. Nil means a timer.
	Wait func(ctx context.Context, d time.Duration) error
}

// Code is the server's answer to a device authorization request (RFC 8628
// 3.2).
type Code struct {
	DeviceCode              string `json:"device_code"`
	UserCode                string `json:"user_code"`
	VerificationURI         string `json:"verification_uri"`
	VerificationURIComplete string `json:"verification_uri_complete"`
	ExpiresIn               int    `json:"expires_in"`
	Interval                int    `json:"interval"`
	// SignupsOpen is false while the server's sign-ups are closed, so
	// only an account that exists there already can approve the code. Nil
	// means the server didn't say.
	SignupsOpen *bool `json:"signups_open"`
}

// Device is what the server asks about this computer when it requests a
// code (docs/spec/device-auth-v1.md section 2).
type Device struct {
	// ID is the install's ledger chain id.
	ID string
	// OS is the OS family: windows, macos, linux or other.
	OS string
	// Build is the pairtime version.
	Build string
}

func (c *Client) httpClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 30 * time.Second}
}

func (c *Client) wait(ctx context.Context, d time.Duration) error {
	if c.Wait != nil {
		return c.Wait(ctx, d)
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// maxReply bounds what the client reads of an answer.
const maxReply = 64 << 10

// send sends req and decodes a JSON answer with a 2xx status into out,
// when out isn't nil. Any other status becomes a *ServerError, a redirect
// too: the device API never redirects, and following one could send the
// token over plain http, or the device code to another host.
func (c *Client) send(req *http.Request, out any) (int, error) {
	req.Header.Set("User-Agent", c.UserAgent)
	req.Header.Set("Accept", "application/json")
	hc := *c.httpClient()
	hc.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := hc.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxReply))
	if err != nil {
		return resp.StatusCode, err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		se := &ServerError{Status: resp.StatusCode}
		var e struct {
			Error       string `json:"error"`
			Description string `json:"error_description"`
		}
		if json.Unmarshal(body, &e) == nil {
			se.Code, se.Description = printable(e.Error), printable(e.Description)
		}
		return resp.StatusCode, se
	}
	if out != nil && resp.StatusCode != http.StatusNoContent {
		if err := json.Unmarshal(body, out); err != nil {
			return resp.StatusCode, fmt.Errorf("account: the server's answer isn't the JSON pairtime expects: %v", err)
		}
	}
	return resp.StatusCode, nil
}

func (c *Client) postForm(ctx context.Context, path string, form url.Values, out any) (int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.Server+path, strings.NewReader(form.Encode()))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return c.send(req, out)
}

// RequestCode asks the server for a device code and a user code for d
// (RFC 8628 3.1), naming key's public half as the device key, with the
// signature that shows this computer holds it: the token the code yields
// works only with that key (D-key-at-grant). With sign-in off on the
// server it returns ErrClosed.
func (c *Client) RequestCode(ctx context.Context, d Device, key ed25519.PrivateKey) (Code, error) {
	pub := encodeKey(key.Public().(ed25519.PublicKey))
	var code Code
	_, err := c.postForm(ctx, codePath, url.Values{
		"client_id":     {ClientID},
		"device_id":     {d.ID},
		"os":            {d.OS},
		"client_build":  {d.Build},
		"public_key":    {pub},
		"key_signature": {base64.StdEncoding.EncodeToString(ed25519.Sign(key, KeyMessage(d.ID, pub)))},
	}, &code)
	var se *ServerError
	if errors.As(err, &se) && se.Code == "temporarily_unavailable" {
		return Code{}, ErrClosed
	}
	if err != nil {
		return Code{}, err
	}
	if code.DeviceCode == "" || code.UserCode == "" || code.VerificationURI == "" {
		return Code{}, errors.New("account: the server's code answer is missing its codes")
	}
	// The codes and the page are shown in the teen's terminal.
	for _, s := range []string{code.UserCode, code.VerificationURI, code.VerificationURIComplete} {
		if printable(s) != s {
			return Code{}, errors.New("account: the server's code answer has characters that can't be shown")
		}
	}
	return code, nil
}

// maxShown bounds text from a server that pairtime shows.
const maxShown = 300

// printable keeps the printable ASCII of s, at most maxShown bytes, so
// text a server sends can't steer the terminal it is shown in. RFC 6749
// 5.2 allows nothing else in an error_description anyway.
func printable(s string) string {
	b := make([]byte, 0, min(len(s), maxShown))
	for i := 0; i < len(s) && len(b) < maxShown; i++ {
		if c := s[i]; c >= 0x20 && c < 0x7f {
			b = append(b, c)
		}
	}
	return string(b)
}

// Token is what the device grant ends with.
type Token struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	DeviceID    string `json:"device_id"`
	// PublicKey is the device key the token works with, in standard
	// base64: the one the code request named.
	PublicKey string `json:"public_key"`
	// Account names the account that approved the code, as the server's
	// pages show it.
	Account string `json:"account"`
}

// Poll asks for the token of code until the teen answers or the code
// expires (RFC 8628 3.4, 3.5). It waits the server's interval before each
// request, 5 s more after each slow_down, and twice as long after a
// network error, up to a minute, as RFC 8628 3.5 asks. An answer without
// an error code, such as a proxy's 502 page or a 429, isn't the server's
// answer to the code, so it waits as after a network error and asks
// again. Any error code but authorization_pending and slow_down ends the
// polling, as RFC 8628 3.5 says: ErrDenied, ErrTaken, ErrExpired, or a
// *ServerError for the rest. Once it has waited longer than the code
// lives, as when the network never came back, it stops with ErrExpired.
func (c *Client) Poll(ctx context.Context, code Code) (Token, error) {
	interval := time.Duration(code.Interval) * time.Second
	if interval <= 0 {
		interval = defaultInterval
	}
	expires := time.Duration(code.ExpiresIn) * time.Second
	if expires <= 0 {
		expires = 15 * time.Minute
	}
	lifetime := expires + time.Minute
	wait, waited := interval, time.Duration(0)
	for {
		if waited >= lifetime {
			return Token{}, ErrExpired
		}
		if err := c.wait(ctx, wait); err != nil {
			return Token{}, err
		}
		waited += wait
		var tok Token
		_, err := c.postForm(ctx, tokenPath, url.Values{
			"grant_type":  {grantType},
			"device_code": {code.DeviceCode},
			"client_id":   {ClientID},
		}, &tok)
		var se *ServerError
		switch {
		case err == nil:
			if !strings.EqualFold(tok.TokenType, "Bearer") || devtoken.Check(tok.AccessToken) != nil {
				return Token{}, errors.New("account: the server's token isn't a pairtime device token")
			}
			if tok.DeviceID != "" && !validDeviceID(tok.DeviceID) {
				return Token{}, errors.New("account: the server's token answer names a device id that isn't one")
			}
			tok.Account = printable(tok.Account)
			return tok, nil
		case errors.As(err, &se):
			switch se.Code {
			case "authorization_pending":
				wait = interval
			case "slow_down":
				interval += slowDownStep
				wait = interval
			case "access_denied":
				return Token{}, ErrDenied
			case "device_taken":
				return Token{}, ErrTaken
			case "expired_token":
				return Token{}, ErrExpired
			case "":
				if se.Status < 500 && se.Status != http.StatusTooManyRequests {
					return Token{}, se
				}
				// No RFC 6749 error body, so a proxy or a rate limit
				// answered, not the server's grant: wait as after a network
				// error.
				wait = backoff(wait, interval)
			default:
				return Token{}, se
			}
		case ctx.Err() != nil:
			return Token{}, ctx.Err()
		default:
			// The network, not the server.
			wait = backoff(wait, interval)
		}
	}
}

// backoff is the wait before the next poll after one that got no answer
// from the server: twice the last, up to maxBackoff, and never below the
// server's interval.
func backoff(wait, interval time.Duration) time.Duration {
	return max(min(2*max(wait, interval), maxBackoff), interval)
}

// KeyMessage is the message the device key signs in a code request, for
// device id and the public key as sent (docs/spec/device-auth-v1.md
// section 2).
func KeyMessage(id, publicKey string) []byte {
	return []byte(keyLabel + id + "\n" + publicKey)
}

// encodeKey writes a public key as the API sends it: standard base64.
func encodeKey(pub ed25519.PublicKey) string {
	return base64.StdEncoding.EncodeToString(pub)
}

// RemoveDevice asks the server to remove this device, which stops its
// token at once: what pairtime logout does first.
func (c *Client) RemoveDevice(ctx context.Context, token string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, c.Server+mePath, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	_, err = c.send(req, nil)
	return err
}

// isNotFound reports whether the keystore has no sign-in.
func isNotFound(err error) bool { return errors.Is(err, keys.ErrNotFound) }

// validDeviceID reports whether id can be a device id, by the server's
// rule: 16 to 128 letters, digits, '_' and '-' (docs/spec/device-auth-v1.md
// section 1).
func validDeviceID(id string) bool {
	if len(id) < 16 || len(id) > 128 {
		return false
	}
	for i := 0; i < len(id); i++ {
		c := id[i]
		if !('a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' || '0' <= c && c <= '9' || c == '_' || c == '-') {
			return false
		}
	}
	return true
}
