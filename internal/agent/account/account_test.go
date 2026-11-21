// SPDX-License-Identifier: AGPL-3.0-or-later

package account

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/internal/agent/config"
	"github.com/some-du6e/pairtime/internal/agent/keys"
	"github.com/some-du6e/pairtime/internal/devtoken"
)

// reply is one scripted answer of the fake server, or a network error.
type reply struct {
	status int
	body   string
	err    error
}

// fakeServer answers each request to a path with the next reply scripted
// for it, and records the requests.
type fakeServer struct {
	mu      sync.Mutex
	replies map[string][]reply // by "METHOD path"
	seen    []*http.Request
	bodies  []string
	// before, when set, sees each request first, with its body.
	before func(r *http.Request, body string)
}

func (f *fakeServer) RoundTrip(r *http.Request) (*http.Response, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var body string
	if r.Body != nil {
		b, _ := io.ReadAll(r.Body)
		body = string(b)
	}
	if f.before != nil {
		f.before(r, body)
	}
	f.seen = append(f.seen, r)
	f.bodies = append(f.bodies, body)
	k := r.Method + " " + r.URL.Path
	q := f.replies[k]
	if len(q) == 0 {
		return nil, fmt.Errorf("the fake server has no reply left for %s", k)
	}
	rep := q[0]
	if len(q) > 1 {
		f.replies[k] = q[1:]
	}
	if rep.err != nil {
		return nil, rep.err
	}
	return &http.Response{
		StatusCode: rep.status,
		Header:     http.Header{"Content-Type": {"application/json"}},
		Body:       io.NopCloser(strings.NewReader(rep.body)),
		Request:    r,
	}, nil
}

// body returns the body of the first request to "METHOD path", or "".
func (f *fakeServer) body(k string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i, r := range f.seen {
		if r.Method+" "+r.URL.Path == k {
			return f.bodies[i]
		}
	}
	return ""
}

// count returns how many requests went to "METHOD path".
func (f *fakeServer) count(k string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, r := range f.seen {
		if r.Method+" "+r.URL.Path == k {
			n++
		}
	}
	return n
}

const testServer = "https://pairtime.example.org"

var testToken = devtoken.New()

func oauthErr(code string) reply {
	return reply{status: http.StatusBadRequest, body: `{"error":"` + code + `"}`}
}

func tokenReply(tok string) reply {
	return reply{status: http.StatusOK, body: `{"access_token":"` + tok + `","token_type":"Bearer","device_id":"d_testdevice00000000000000a"}`}
}

// recordWaits returns a Wait that records each wait and returns at once.
func recordWaits(waits *[]time.Duration) func(context.Context, time.Duration) error {
	return func(ctx context.Context, d time.Duration) error {
		*waits = append(*waits, d)
		return ctx.Err()
	}
}

func TestPoll(t *testing.T) {
	s := time.Second
	tests := []struct {
		name      string
		code      Code
		replies   []reply
		wantErr   error
		wantWaits []time.Duration
	}{
		{name: "pending, then a token, waiting the interval before each poll", code: Code{Interval: 5, ExpiresIn: 900},
			replies: []reply{oauthErr("authorization_pending"), tokenReply(testToken)}, wantWaits: []time.Duration{5 * s, 5 * s}},
		{name: "each slow_down adds 5 s to the wait for that poll and every later one", code: Code{Interval: 5, ExpiresIn: 900},
			replies:   []reply{oauthErr("slow_down"), oauthErr("authorization_pending"), oauthErr("slow_down"), tokenReply(testToken)},
			wantWaits: []time.Duration{5 * s, 10 * s, 10 * s, 15 * s}},
		{name: "no interval means 5 s", code: Code{ExpiresIn: 900}, replies: []reply{tokenReply(testToken)}, wantWaits: []time.Duration{5 * s}},
		{name: "access_denied stops with ErrDenied", code: Code{Interval: 5, ExpiresIn: 900},
			replies: []reply{oauthErr("access_denied")}, wantErr: ErrDenied, wantWaits: []time.Duration{5 * s}},
		{name: "expired_token stops with ErrExpired", code: Code{Interval: 5, ExpiresIn: 900},
			replies: []reply{oauthErr("authorization_pending"), oauthErr("expired_token")}, wantErr: ErrExpired, wantWaits: []time.Duration{5 * s, 5 * s}},
		{name: "a network error doubles the wait, up to a minute, and the interval comes back after", code: Code{Interval: 5, ExpiresIn: 900},
			replies:   []reply{{err: errors.New("connection refused")}, {err: errors.New("connection refused")}, {err: errors.New("connection refused")}, {err: errors.New("connection refused")}, oauthErr("authorization_pending"), tokenReply(testToken)},
			wantWaits: []time.Duration{5 * s, 10 * s, 20 * s, 40 * s, 60 * s, 5 * s}},
		{name: "a poll that has waited longer than the code lives stops with ErrExpired", code: Code{Interval: 5, ExpiresIn: 10},
			replies: []reply{oauthErr("authorization_pending")}, wantErr: ErrExpired,
			wantWaits: slices.Repeat([]time.Duration{5 * s}, 14)},
		{name: "a token that isn't a ptd_ token is refused", code: Code{Interval: 5, ExpiresIn: 900},
			replies: []reply{tokenReply("ghp_notours")}, wantWaits: []time.Duration{5 * s}},
		{name: "device_taken stops with ErrTaken", code: Code{Interval: 5, ExpiresIn: 900},
			replies: []reply{oauthErr("device_taken")}, wantErr: ErrTaken, wantWaits: []time.Duration{5 * s}},
		{name: "a proxy's 502 page without an error code is waited out like a network error, since the code still works", code: Code{Interval: 5, ExpiresIn: 900},
			replies:   []reply{{status: http.StatusBadGateway, body: "<html>Bad gateway</html>"}, {status: http.StatusServiceUnavailable, body: ""}, oauthErr("authorization_pending"), tokenReply(testToken)},
			wantWaits: []time.Duration{5 * s, 10 * s, 20 * s, 5 * s}},
		{name: "a 429 without an error code is waited out too", code: Code{Interval: 5, ExpiresIn: 900},
			replies: []reply{{status: http.StatusTooManyRequests, body: "slow down"}, tokenReply(testToken)}, wantWaits: []time.Duration{5 * s, 10 * s}},
		{name: "an error code other than pending and slow_down stops the polling, even on a 5xx (RFC 8628 3.5)", code: Code{Interval: 5, ExpiresIn: 900},
			replies: []reply{{status: http.StatusInternalServerError, body: `{"error":"server_error"}`}}, wantErr: errServerError, wantWaits: []time.Duration{5 * s}},
		{name: "a 404 without an error code stops the polling, since waiting won't change it", code: Code{Interval: 5, ExpiresIn: 900},
			replies: []reply{{status: http.StatusNotFound, body: "not found"}}, wantErr: errServerError, wantWaits: []time.Duration{5 * s}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := &fakeServer{replies: map[string][]reply{"POST " + tokenPath: tt.replies}}
			var waits []time.Duration
			c := &Client{Server: testServer, HTTP: &http.Client{Transport: fake}, UserAgent: "pairtime-cli/test", Wait: recordWaits(&waits)}
			tok, err := c.Poll(context.Background(), tt.code)
			var se *ServerError
			switch {
			case tt.wantErr == errServerError:
				if !errors.As(err, &se) {
					t.Errorf("Poll = %v, want a *ServerError", err)
				}
			case tt.wantErr != nil && !errors.Is(err, tt.wantErr):
				t.Errorf("Poll = %v, want %v", err, tt.wantErr)
			case tt.wantErr == nil && strings.HasPrefix(tt.name, "a token that isn't"):
				if err == nil {
					t.Errorf("Poll took %q", tok.AccessToken)
				}
			case tt.wantErr == nil && (err != nil || tok.AccessToken != testToken):
				t.Errorf("Poll = %+v, %v", tok, err)
			}
			if !slices.Equal(waits, tt.wantWaits) {
				t.Errorf("waits = %v, want %v", waits, tt.wantWaits)
			}
			for _, r := range fake.seen {
				if r.Header.Get("User-Agent") != "pairtime-cli/test" {
					t.Errorf("a poll's User-Agent is %q", r.Header.Get("User-Agent"))
				}
			}
		})
	}
}

func TestRequestCode(t *testing.T) {
	key := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{9}, ed25519.SeedSize))
	tests := []struct {
		name    string
		reply   reply
		wantErr error
		// wantClosed says the code reports sign-ups closed.
		wantClosed bool
	}{
		{name: "a code comes back", reply: reply{status: http.StatusOK, body: `{"device_code":"dc","user_code":"WDJB-MJHT","verification_uri":"https://pairtime.example.org/device","expires_in":900,"interval":5}`}},
		{name: "a code says when sign-ups are closed", reply: reply{status: http.StatusOK, body: `{"device_code":"dc","user_code":"WDJB-MJHT","verification_uri":"https://pairtime.example.org/device","expires_in":900,"interval":5,"signups_open":false}`}, wantClosed: true},
		{name: "temporarily_unavailable means the server isn't taking sign-ins", reply: reply{status: http.StatusServiceUnavailable, body: `{"error":"temporarily_unavailable"}`}, wantErr: ErrClosed},
		{name: "a code with a terminal escape in it is refused, since the terminal shows it", reply: reply{status: http.StatusOK, body: `{"device_code":"dc","user_code":"WDJB-MJHT\u001b]0;pwned\u0007","verification_uri":"https://pairtime.example.org/device","expires_in":900,"interval":5}`}, wantErr: errShown},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := &fakeServer{replies: map[string][]reply{"POST " + codePath: {tt.reply}}}
			c := &Client{Server: testServer, HTTP: &http.Client{Transport: fake}, UserAgent: "pairtime-cli/test"}
			code, err := c.RequestCode(context.Background(), Device{ID: "d_testdevice00000000000000a", OS: "linux", Build: "v0.1.0"}, key)
			switch {
			case tt.wantErr == errShown:
				if err == nil || !strings.Contains(err.Error(), "can't be shown") {
					t.Fatalf("RequestCode = %v, want a refusal", err)
				}
			case !errors.Is(err, tt.wantErr):
				t.Fatalf("RequestCode = %v, want %v", err, tt.wantErr)
			}
			if closed := code.SignupsOpen != nil && !*code.SignupsOpen; closed != tt.wantClosed {
				t.Errorf("the code says sign-ups are closed: %v, want %v", closed, tt.wantClosed)
			}
			form, err := url.ParseQuery(fake.bodies[0])
			if err != nil {
				t.Fatal(err)
			}
			for k, want := range map[string]string{"client_id": "pairtime", "device_id": "d_testdevice00000000000000a", "os": "linux", "client_build": "v0.1.0", "public_key": encodeKey(key.Public().(ed25519.PublicKey))} {
				if form.Get(k) != want {
					t.Errorf("the request's %s is %q, want %q", k, form.Get(k), want)
				}
			}
			sig, _ := base64.StdEncoding.DecodeString(form.Get("key_signature"))
			if !ed25519.Verify(key.Public().(ed25519.PublicKey), KeyMessage("d_testdevice00000000000000a", form.Get("public_key")), sig) {
				t.Errorf("key_signature %q isn't the key's signature of the key message for this device", form.Get("key_signature"))
			}
		})
	}
}

// memSecrets is the keystore in memory.
type memSecrets struct {
	token, seed []byte
	setErr      error
	forgot      int
}

func (m *memSecrets) SetSignKey(seed []byte) error {
	if m.setErr != nil {
		return m.setErr
	}
	m.token, m.seed = nil, bytes.Clone(seed)
	return nil
}

func (m *memSecrets) SetAccount(token, seed []byte) error {
	if m.setErr != nil {
		return m.setErr
	}
	m.token, m.seed = bytes.Clone(token), bytes.Clone(seed)
	return nil
}

func (m *memSecrets) Account() ([]byte, []byte, error) {
	if m.token == nil {
		return nil, nil, fmt.Errorf("%w: none", keys.ErrNotFound)
	}
	return bytes.Clone(m.token), bytes.Clone(m.seed), nil
}

func (m *memSecrets) ForgetAccount() error {
	m.token, m.seed = nil, nil
	m.forgot++
	return nil
}

// loginServer scripts a server that gives a code, then the token after one
// pending poll, and removes the device when asked.
func loginServer() *fakeServer {
	return &fakeServer{replies: map[string][]reply{
		"POST " + codePath:  {{status: http.StatusOK, body: `{"device_code":"dc","user_code":"WDJB-MJHT","verification_uri":"https://pairtime.example.org/device","verification_uri_complete":"https://pairtime.example.org/device?user_code=WDJB-MJHT","expires_in":900,"interval":5}`}},
		"POST " + tokenPath: {oauthErr("authorization_pending"), tokenReply(testToken)},
		"DELETE " + mePath:  {{status: http.StatusNoContent}},
	}}
}

func testOptions(t *testing.T, fake http.RoundTripper, sec Secrets) Options {
	t.Helper()
	dir := t.TempDir()
	var waits []time.Duration
	return Options{
		Server:     testServer,
		DataDir:    dir,
		ConfigFile: filepath.Join(dir, "config.toml"),
		Secrets:    sec,
		HTTP:       &http.Client{Transport: fake},
		UserAgent:  "pairtime-cli/test",
		Wait:       recordWaits(&waits),
	}
}

var testDevice = Device{ID: "d_testdevice00000000000000a", OS: "linux", Build: "v0.1.0"}

func TestLogin(t *testing.T) {
	tests := []struct {
		name string
		run  func(t *testing.T)
	}{
		{name: "a login keeps its key before it asks for a code that names it, then keeps the token, writes account.json and switches config.toml online", run: func(t *testing.T) {
			fake := loginServer()
			sec := &memSecrets{}
			var keptFirst bool
			fake.before = func(r *http.Request, _ string) {
				if r.URL.Path == codePath {
					keptFirst = sec.seed != nil && sec.token == nil
				}
			}
			o := testOptions(t, fake, sec)
			var shown Code
			r, err := Login(context.Background(), o, testDevice, func(c Code) { shown = c })
			if err != nil {
				t.Fatal(err)
			}
			if shown.UserCode != "WDJB-MJHT" {
				t.Errorf("show got %+v", shown)
			}
			if !keptFirst {
				t.Error("the keystore didn't hold the key alone when the code was asked for")
			}
			if string(sec.token) != testToken || len(sec.seed) != ed25519.SeedSize {
				t.Errorf("the keystore got token %q and a %d-byte seed", sec.token, len(sec.seed))
			}
			pub := ed25519.NewKeyFromSeed(sec.seed).Public().(ed25519.PublicKey)
			form, err := url.ParseQuery(fake.body("POST " + codePath))
			if err != nil {
				t.Fatal(err)
			}
			sig, _ := base64.StdEncoding.DecodeString(form.Get("key_signature"))
			if form.Get("public_key") != encodeKey(pub) || !ed25519.Verify(pub, KeyMessage(testDevice.ID, form.Get("public_key")), sig) {
				t.Errorf("the code request %v doesn't name the keystore's key, signed for this device", form)
			}
			if n := len(fake.seen); n != 3 {
				t.Errorf("the login sent %d requests, want 3: the code and two polls", n)
			}
			l, err := Load(o.DataDir)
			if err != nil || l.Server != testServer || l.DeviceID != testDevice.ID || l.PublicKey != encodeKey(pub) ||
				r.Linked.DeviceID != l.DeviceID || r.Linked.PublicKey != l.PublicKey || !r.Linked.LinkedAt.Equal(l.LinkedAt) {
				t.Errorf("account.json = %+v, %v; Login returned %+v", l, err, r.Linked)
			}
			cfg, _, err := config.Load(o.ConfigFile)
			if err != nil || cfg.Mode != config.Online || cfg.Server.URL != testServer || r.ConfigErr != nil {
				t.Errorf("config.toml = %+v, %v (%v)", cfg, err, r.ConfigErr)
			}
			creds, err := Open(o.DataDir, sec)
			if err != nil || creds.Token != testToken || !creds.Key.Equal(ed25519.NewKeyFromSeed(sec.seed)) {
				t.Errorf("Open = %v", err)
			}
			if s := fmt.Sprintf("%v %+v %#v %s", creds, creds, creds, creds); strings.Contains(s, testToken) {
				t.Errorf("printing Credentials shows the token: %s", s)
			}
		}},
		{name: "a token for another key than the one sent undoes the login: the keystore forgets it and the server removes the device", run: func(t *testing.T) {
			fake := loginServer()
			other := encodeKey(ed25519.NewKeyFromSeed(bytes.Repeat([]byte{4}, ed25519.SeedSize)).Public().(ed25519.PublicKey))
			fake.replies["POST "+tokenPath] = []reply{{status: http.StatusOK, body: `{"access_token":"` + testToken + `","token_type":"Bearer","device_id":"` + testDevice.ID + `","public_key":"` + other + `"}`}}
			sec := &memSecrets{}
			o := testOptions(t, fake, sec)
			if _, err := Login(context.Background(), o, testDevice, func(Code) {}); err == nil || !strings.Contains(err.Error(), "another device key") {
				t.Fatalf("Login = %v, want a refusal of the other key", err)
			}
			if sec.token != nil || sec.seed != nil || sec.forgot != 1 {
				t.Errorf("the keystore still has %q and a seed %v (forgot %d times)", sec.token, sec.seed != nil, sec.forgot)
			}
			if fake.count("DELETE "+mePath) != 1 {
				t.Error("the server wasn't asked to remove the device")
			}
			if _, err := Load(o.DataDir); !errors.Is(err, ErrNotLinked) {
				t.Errorf("account.json after a failed login: %v", err)
			}
			if _, err := os.Stat(o.ConfigFile); err == nil {
				t.Error("a failed login wrote config.toml")
			}
		}},
		{name: "a code nobody approves leaves no key behind, and nothing to remove on the server", run: func(t *testing.T) {
			for _, code := range []string{"access_denied", "expired_token", "device_taken"} {
				fake := loginServer()
				fake.replies["POST "+tokenPath] = []reply{oauthErr(code)}
				sec := &memSecrets{}
				o := testOptions(t, fake, sec)
				_, err := Login(context.Background(), o, testDevice, func(Code) {})
				if !errors.Is(err, ErrDenied) && !errors.Is(err, ErrExpired) && !errors.Is(err, ErrTaken) {
					t.Errorf("%s: Login = %v", code, err)
				}
				if sec.seed != nil || sec.forgot != 1 || fake.count("DELETE "+mePath) != 0 {
					t.Errorf("%s: the keystore still has a seed %v (forgot %d times), and %d removals were sent", code, sec.seed != nil, sec.forgot, fake.count("DELETE "+mePath))
				}
			}
		}},
		{name: "device_taken comes back as ErrTaken, for the new-device rule", run: func(t *testing.T) {
			fake := loginServer()
			fake.replies["POST "+tokenPath] = []reply{oauthErr("device_taken")}
			o := testOptions(t, fake, &memSecrets{})
			if _, err := Login(context.Background(), o, testDevice, func(Code) {}); !errors.Is(err, ErrTaken) {
				t.Errorf("Login = %v, want ErrTaken", err)
			}
		}},
		{name: "a keystore that can't keep the key stops the login before anything is sent", run: func(t *testing.T) {
			fake := loginServer()
			o := testOptions(t, fake, &memSecrets{setErr: errors.New("the keyring is locked")})
			if _, err := Login(context.Background(), o, testDevice, func(Code) { t.Error("a code was shown") }); err == nil {
				t.Fatal("Login succeeded without a key in the keystore")
			}
			if len(fake.seen) != 0 {
				t.Errorf("the login sent %d requests", len(fake.seen))
			}
		}},
		{name: "an install that is linked already isn't linked again", run: func(t *testing.T) {
			fake := loginServer()
			o := testOptions(t, fake, &memSecrets{})
			if _, err := Login(context.Background(), o, testDevice, func(Code) {}); err != nil {
				t.Fatal(err)
			}
			if _, err := Login(context.Background(), o, testDevice, func(Code) {}); !errors.Is(err, ErrAlreadyLinked) {
				t.Errorf("a second Login = %v, want ErrAlreadyLinked", err)
			}
			if fake.count("POST "+codePath) != 1 {
				t.Errorf("the second Login asked for a code")
			}
		}},
		{name: "a server that isn't taking sign-ins ends the login at once", run: func(t *testing.T) {
			fake := &fakeServer{replies: map[string][]reply{"POST " + codePath: {{status: http.StatusServiceUnavailable, body: `{"error":"temporarily_unavailable"}`}}}}
			sec := &memSecrets{}
			o := testOptions(t, fake, sec)
			if _, err := Login(context.Background(), o, testDevice, func(Code) { t.Error("a code was shown") }); !errors.Is(err, ErrClosed) {
				t.Errorf("Login = %v, want ErrClosed", err)
			}
			if sec.seed != nil {
				t.Error("the keystore kept the key of a login that didn't happen")
			}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, tt.run)
	}
}

func TestLogout(t *testing.T) {
	tests := []struct {
		name          string
		deleteReply   reply
		wantServerErr bool
	}{
		{name: "logout removes the device on the server, forgets the sign-in, deletes account.json and switches config.toml to local", deleteReply: reply{status: http.StatusNoContent}},
		{name: "a device the server removed already logs out without an error", deleteReply: reply{status: http.StatusUnauthorized, body: `{"error":"invalid_token"}`}},
		{name: "a server that can't be reached still leaves nothing here, and says so", deleteReply: reply{err: errors.New("no route to host")}, wantServerErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := loginServer()
			sec := &memSecrets{}
			o := testOptions(t, fake, sec)
			if _, err := Login(context.Background(), o, testDevice, func(Code) {}); err != nil {
				t.Fatal(err)
			}
			fake.mu.Lock()
			fake.replies["DELETE "+mePath] = []reply{tt.deleteReply}
			fake.mu.Unlock()
			r, err := Logout(context.Background(), Options{DataDir: o.DataDir, ConfigFile: o.ConfigFile, Secrets: sec, HTTP: o.HTTP, UserAgent: o.UserAgent})
			if err != nil {
				t.Fatal(err)
			}
			if (r.ServerErr != nil) != tt.wantServerErr {
				t.Errorf("ServerErr = %v, want an error %v", r.ServerErr, tt.wantServerErr)
			}
			if fake.count("DELETE "+mePath) != 1 {
				t.Errorf("logout sent %d removals", fake.count("DELETE "+mePath))
			}
			if sec.token != nil {
				t.Error("the keystore still has the token")
			}
			if _, err := Load(o.DataDir); !errors.Is(err, ErrNotLinked) {
				t.Errorf("account.json after logout: %v", err)
			}
			if cfg, _, _ := config.Load(o.ConfigFile); cfg.Mode != config.Local || cfg.Server.URL != testServer {
				t.Errorf("config.toml after logout = %+v; want mode local, keeping the server", cfg)
			}
			if _, err := Logout(context.Background(), Options{DataDir: o.DataDir, ConfigFile: o.ConfigFile, Secrets: sec}); !errors.Is(err, ErrNotLinked) {
				t.Errorf("a second Logout = %v, want ErrNotLinked", err)
			}
		})
	}
}

// errShown stands for RequestCode's refusal of codes it can't show.
var errShown = errors.New("can't be shown")

// errServerError stands for any *ServerError.
var errServerError = errors.New("a *ServerError")

func TestPrintable(t *testing.T) {
	tests := []struct{ name, in, want string }{
		{name: "plain text is kept", in: "Nobody has approved this code yet.", want: "Nobody has approved this code yet."},
		{name: "an escape sequence loses its control bytes", in: "a\x1b[2Jb\x07c", want: "a[2Jbc"},
		{name: "a newline is dropped", in: "a\nb", want: "ab"},
		{name: "text past 300 bytes is cut", in: strings.Repeat("x", 400), want: strings.Repeat("x", 300)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := printable(tt.in); got != tt.want {
				t.Errorf("printable(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
	t.Run("a server's error description reaches the error printable", func(t *testing.T) {
		fake := &fakeServer{replies: map[string][]reply{"POST " + tokenPath: {{status: http.StatusBadRequest, body: `{"error":"invalid_grant","error_description":"gone\u001b[31m"}`}}}}
		c := &Client{Server: testServer, HTTP: &http.Client{Transport: fake}, Wait: func(context.Context, time.Duration) error { return nil }}
		_, err := c.Poll(context.Background(), Code{Interval: 5, ExpiresIn: 900})
		if err == nil || strings.ContainsRune(err.Error(), 0x1b) || !strings.Contains(err.Error(), "invalid_grant: gone[31m") {
			t.Errorf("Poll = %q", err)
		}
	})
}

func TestLogoutDamagedFile(t *testing.T) {
	fake := &fakeServer{replies: map[string][]reply{}}
	sec := &memSecrets{token: []byte(testToken), seed: make([]byte, ed25519.SeedSize)}
	o := testOptions(t, fake, sec)
	if err := os.WriteFile(File(o.DataDir), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Login(context.Background(), o, testDevice, func(Code) {}); err == nil || !strings.Contains(err.Error(), "pairtime logout clears it") {
		t.Errorf("Login over a damaged account.json = %v, want a pointer to pairtime logout", err)
	}
	r, err := Logout(context.Background(), Options{DataDir: o.DataDir, ConfigFile: o.ConfigFile, Secrets: sec, HTTP: o.HTTP})
	tests := []struct {
		name string
		ok   bool
	}{
		{name: "logout over a damaged account.json succeeds", ok: err == nil},
		{name: "it says the server wasn't told", ok: r.ServerErr != nil && strings.Contains(r.ServerErr.Error(), "doesn't know which server")},
		{name: "it sends nothing, since it doesn't know where", ok: len(fake.seen) == 0},
		{name: "it deletes the file", ok: func() bool { _, err := os.Stat(File(o.DataDir)); return errors.Is(err, os.ErrNotExist) }()},
		{name: "it forgets the sign-in", ok: sec.token == nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !tt.ok {
				t.Errorf("it didn't: Logout = %+v, %v", r, err)
			}
		})
	}
}

func TestLogoutWithoutKeystore(t *testing.T) {
	fake := loginServer()
	sec := &memSecrets{}
	o := testOptions(t, fake, sec)
	if _, err := Login(context.Background(), o, testDevice, func(Code) {}); err != nil {
		t.Fatal(err)
	}
	// The keystore can't be opened, so logout gets no Secrets.
	_, first := Logout(context.Background(), Options{DataDir: o.DataDir, ConfigFile: o.ConfigFile})
	cfg, _, _ := config.Load(o.ConfigFile)
	_, loadErr := Load(o.DataDir)
	sentFirst := fake.count("DELETE " + mePath)
	// Once the keystore opens again, logout finishes.
	_, second := Logout(context.Background(), Options{DataDir: o.DataDir, ConfigFile: o.ConfigFile, Secrets: sec, HTTP: o.HTTP})
	_, loadAfter := Load(o.DataDir)
	tests := []struct {
		name string
		ok   bool
	}{
		{name: "a logout whose keystore can't be opened says so", ok: errors.Is(first, ErrNoKeystore)},
		{name: "it switches config.toml to local, so nothing uploads", ok: cfg.Mode == config.Local},
		{name: "it keeps account.json, the record of which server to tell", ok: loadErr == nil},
		{name: "it sends nothing to the server without the token", ok: sentFirst == 0},
		{name: "the next logout, with the keystore, finishes without an error", ok: second == nil},
		{name: "the next logout removes the device on the server", ok: fake.count("DELETE "+mePath) == 1},
		{name: "the next logout deletes account.json and forgets the token", ok: errors.Is(loadAfter, ErrNotLinked) && sec.token == nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !tt.ok {
				t.Errorf("it didn't: first logout %v, second %v", first, second)
			}
		})
	}
}

func TestLogoutChecks(t *testing.T) {
	tests := []struct {
		name string
		run  func(t *testing.T)
	}{
		{name: "an install that isn't linked, with a keystore that can't be opened, is reported as not linked", run: func(t *testing.T) {
			o := testOptions(t, &fakeServer{replies: map[string][]reply{}}, nil)
			if _, err := Logout(context.Background(), Options{DataDir: o.DataDir, ConfigFile: o.ConfigFile}); !errors.Is(err, ErrNotLinked) {
				t.Errorf("Logout = %v, want ErrNotLinked", err)
			}
		}},
		{name: "a keystore whose key isn't the registered one keeps its token from the server", run: func(t *testing.T) {
			fake := loginServer()
			sec := &memSecrets{}
			o := testOptions(t, fake, sec)
			if _, err := Login(context.Background(), o, testDevice, func(Code) {}); err != nil {
				t.Fatal(err)
			}
			sec.seed = bytes.Repeat([]byte{1}, ed25519.SeedSize) // another login's key
			r, err := Logout(context.Background(), Options{DataDir: o.DataDir, ConfigFile: o.ConfigFile, Secrets: sec, HTTP: o.HTTP})
			if err != nil || r.ServerErr == nil || fake.count("DELETE "+mePath) != 0 {
				t.Errorf("Logout = %+v, %v, with %d removals sent; want the token kept from the server", r, err, fake.count("DELETE "+mePath))
			}
		}},
		{name: "a logout whose context has ended changes nothing", run: func(t *testing.T) {
			fake := loginServer()
			sec := &memSecrets{}
			o := testOptions(t, fake, sec)
			if _, err := Login(context.Background(), o, testDevice, func(Code) {}); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			if _, err := Logout(ctx, Options{DataDir: o.DataDir, ConfigFile: o.ConfigFile, Secrets: sec, HTTP: o.HTTP}); !errors.Is(err, context.Canceled) {
				t.Errorf("Logout = %v, want context.Canceled", err)
			}
			if _, err := Load(o.DataDir); err != nil || sec.token == nil {
				t.Errorf("a cancelled logout changed things: account.json %v, token %q", err, sec.token)
			}
		}},
		{name: "a login while another holds the lock is refused at once", run: func(t *testing.T) {
			o := testOptions(t, loginServer(), &memSecrets{})
			unlock, err := lock(o.DataDir)
			if err != nil {
				t.Fatal(err)
			}
			defer unlock()
			if _, err := Login(context.Background(), o, testDevice, func(Code) { t.Error("a code was shown") }); !errors.Is(err, ErrBusy) {
				t.Errorf("Login = %v, want ErrBusy", err)
			}
			if _, err := Logout(context.Background(), Options{DataDir: o.DataDir, ConfigFile: o.ConfigFile, Secrets: &memSecrets{}}); !errors.Is(err, ErrBusy) {
				t.Errorf("Logout = %v, want ErrBusy", err)
			}
		}},
		{name: "the account that approved comes back with the login", run: func(t *testing.T) {
			fake := loginServer()
			fake.replies["POST "+tokenPath] = []reply{{status: http.StatusOK, body: `{"access_token":"` + testToken + `","token_type":"Bearer","device_id":"d_testdevice00000000000000a","account":"U0TEEN1\u001b[2J"}`}}
			o := testOptions(t, fake, &memSecrets{})
			r, err := Login(context.Background(), o, testDevice, func(Code) {})
			if err != nil || r.Account != "U0TEEN1[2J" {
				t.Errorf("Login = %+v, %v; want the account, with its control bytes dropped", r, err)
			}
		}},
		{name: "a redirect from the server is an error, not followed with the token", run: func(t *testing.T) {
			fake := &fakeServer{replies: map[string][]reply{"DELETE " + mePath: {{status: http.StatusFound, body: ``}}}}
			c := &Client{Server: testServer, HTTP: &http.Client{Transport: redirecting{fake}}}
			err := c.RemoveDevice(context.Background(), testToken)
			var se *ServerError
			if !errors.As(err, &se) || se.Status != http.StatusFound || fake.count("DELETE "+mePath) != 1 {
				t.Errorf("RemoveDevice = %v after %d requests; want the 302 as an error and one request", err, fake.count("DELETE "+mePath))
			}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, tt.run)
	}
}

// redirecting adds a Location to every 3xx the fake answers, sending the
// client to plain http on the same host.
type redirecting struct{ f *fakeServer }

func (r redirecting) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := r.f.RoundTrip(req)
	if err == nil && resp.StatusCode/100 == 3 {
		resp.Header.Set("Location", "http://pairtime.example.org"+req.URL.Path)
	}
	return resp, err
}

func TestBrowserURL(t *testing.T) {
	complete := Code{VerificationURI: testServer + "/device", VerificationURIComplete: testServer + "/device?user_code=WDJB-MJHT"}
	tests := []struct {
		name     string
		code     Code
		withCode bool
		want     string // "" means none
	}{
		{name: "the complete URI on the server is opened where other accounts can't read the browser's command line", code: complete, withCode: true, want: testServer + "/device?user_code=WDJB-MJHT"},
		{name: "elsewhere the plain URI is opened, so the code stays out of the command line", code: complete, want: testServer + "/device"},
		{name: "without a complete URI the plain one is opened", code: Code{VerificationURI: testServer + "/device"}, withCode: true, want: testServer + "/device"},
		{name: "a page on another host isn't opened", code: Code{VerificationURI: "https://evil.example/device", VerificationURIComplete: "https://evil.example/device?user_code=X"}, withCode: true},
		{name: "a file URL isn't opened", code: Code{VerificationURIComplete: "file:///C:/Windows/System32/calc.exe"}, withCode: true},
		{name: "a bare path isn't opened, since Windows would run it", code: Code{VerificationURIComplete: `C:\Windows\System32\calc.exe`}, withCode: true},
		{name: "the same host over http isn't opened", code: Code{VerificationURIComplete: "http://pairtime.example.org/device"}, withCode: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := browserURL(tt.code, testServer, tt.withCode)
			if ok != (tt.want != "") || got != tt.want {
				t.Errorf("browserURL = %q, %v; want %q", got, ok, tt.want)
			}
		})
	}
	t.Run("on Linux and the BSDs, where /proc or ps shows every account's command lines, the code isn't opened", func(t *testing.T) {
		if want := runtime.GOOS == "windows" || runtime.GOOS == "darwin"; openWithCode != want {
			t.Errorf("openWithCode is %v on %s, want %v", openWithCode, runtime.GOOS, want)
		}
		if got, _ := BrowserURL(complete, testServer); strings.Contains(got, "user_code") != openWithCode {
			t.Errorf("BrowserURL = %q with openWithCode %v", got, openWithCode)
		}
	})
}

func TestBuild(t *testing.T) {
	tests := []struct{ name, version, want string }{
		{name: "a release version is kept", version: "v0.1.0", want: "v0.1.0"},
		{name: "a pseudo-version is kept", version: "v0.0.0-20261120195600-e100a1dabcde", want: "v0.0.0-20261120195600-e100a1dabcde"},
		{name: "dev is kept", version: "dev", want: "dev"},
		{name: "a version with a space becomes unknown", version: "(devel) x", want: "unknown"},
		{name: "an empty version becomes unknown", version: "", want: "unknown"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Build(tt.version); got != tt.want {
				t.Errorf("Build(%q) = %q, want %q", tt.version, got, tt.want)
			}
		})
	}
}
