// SPDX-License-Identifier: AGPL-3.0-or-later

package account

import (
	"bytes"
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
	keyPath   = "/api/v1/devices/me/key"
	grantType = "urn:ietf:params:oauth:grant-type:device_code"
	// keyLabel starts the message the device key signs to register.
	keyLabel = "pairtime-device-key-v1\n"
)

// The device grant's timing on the client's side (RFC 8628 3.2, 3.5).
const (
	// defaultInterval is the wait between polls when the server names
	// none.
	defaultInterval = 5 * time.Second
	// slowDownStep is what each slow_down adds to the wait.
	slowDownStep = 5 * time.Second
	// maxBackoff bounds the wait after network errors, which double it.
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
// when out isn't nil. Any other status becomes a *ServerError.
func (c *Client) send(req *http.Request, out any) (int, error) {
	req.Header.Set("User-Agent", c.UserAgent)
	req.Header.Set("Accept", "application/json")
	resp, err := c.httpClient().Do(req)
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
// (RFC 8628 3.1). With sign-in off on the server it returns ErrClosed.
func (c *Client) RequestCode(ctx context.Context, d Device) (Code, error) {
	var code Code
	_, err := c.postForm(ctx, codePath, url.Values{
		"client_id":    {ClientID},
		"device_id":    {d.ID},
		"os":           {d.OS},
		"client_build": {d.Build},
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
}

// Poll asks for the token of code until the teen answers or the code
// expires (RFC 8628 3.4, 3.5). It waits the server's interval before each
// request, 5 s more after each slow_down, and twice as long after a
// network error, up to a minute, as RFC 8628 3.5 asks. It returns
// ErrDenied, ErrExpired, or a *ServerError for any other refusal. Once it
// has waited longer than the code lives, as when the network never came
// back, it stops with ErrExpired.
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
			case "expired_token":
				return Token{}, ErrExpired
			default:
				return Token{}, se
			}
		case ctx.Err() != nil:
			return Token{}, ctx.Err()
		default:
			// The network, not the server: slow down before trying again.
			wait = min(2*max(wait, interval), maxBackoff)
		}
	}
}

// KeyMessage is the message the device key signs to register, for device
// id and the public key as sent (docs/spec/device-auth-v1.md section 6).
func KeyMessage(id, publicKey string) []byte {
	return []byte(keyLabel + id + "\n" + publicKey)
}

// encodeKey writes a public key as the API sends it: standard base64.
func encodeKey(pub ed25519.PublicKey) string {
	return base64.StdEncoding.EncodeToString(pub)
}

// RegisterKey registers key's public half as the device's key, with the
// signature that shows this computer holds it.
func (c *Client) RegisterKey(ctx context.Context, token, deviceID string, key ed25519.PrivateKey) error {
	pub := encodeKey(key.Public().(ed25519.PublicKey))
	body, err := json.Marshal(map[string]string{
		"public_key": pub,
		"signature":  base64.StdEncoding.EncodeToString(ed25519.Sign(key, KeyMessage(deviceID, pub))),
	})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.Server+keyPath, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	_, err = c.send(req, nil)
	return err
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
