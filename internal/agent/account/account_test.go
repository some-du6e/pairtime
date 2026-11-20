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
	"os"
	"path/filepath"
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
}

func (f *fakeServer) RoundTrip(r *http.Request) (*http.Response, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var body string
	if r.Body != nil {
		b, _ := io.ReadAll(r.Body)
		body = string(b)
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
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := &fakeServer{replies: map[string][]reply{"POST " + tokenPath: tt.replies}}
			var waits []time.Duration
			c := &Client{Server: testServer, HTTP: &http.Client{Transport: fake}, UserAgent: "pairtime-cli/test", Wait: recordWaits(&waits)}
			tok, err := c.Poll(context.Background(), tt.code)
			switch {
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
	tests := []struct {
		name    string
		reply   reply
		wantErr error
	}{
		{name: "a code comes back", reply: reply{status: http.StatusOK, body: `{"device_code":"dc","user_code":"WDJB-MJHT","verification_uri":"https://pairtime.example.org/device","expires_in":900,"interval":5}`}},
		{name: "temporarily_unavailable means the server isn't taking sign-ins", reply: reply{status: http.StatusServiceUnavailable, body: `{"error":"temporarily_unavailable"}`}, wantErr: ErrClosed},
		{name: "a code with a terminal escape in it is refused, since the terminal shows it", reply: reply{status: http.StatusOK, body: `{"device_code":"dc","user_code":"WDJB-MJHT\u001b]0;pwned\u0007","verification_uri":"https://pairtime.example.org/device","expires_in":900,"interval":5}`}, wantErr: errShown},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := &fakeServer{replies: map[string][]reply{"POST " + codePath: {tt.reply}}}
			c := &Client{Server: testServer, HTTP: &http.Client{Transport: fake}, UserAgent: "pairtime-cli/test"}
			_, err := c.RequestCode(context.Background(), Device{ID: "d_testdevice00000000000000a", OS: "linux", Build: "v0.1.0"})
			switch {
			case tt.wantErr == errShown:
				if err == nil || !strings.Contains(err.Error(), "can't be shown") {
					t.Fatalf("RequestCode = %v, want a refusal", err)
				}
			case !errors.Is(err, tt.wantErr):
				t.Fatalf("RequestCode = %v, want %v", err, tt.wantErr)
			}
			body := fake.bodies[0]
			for _, want := range []string{"client_id=pairtime", "device_id=d_testdevice00000000000000a", "os=linux", "client_build=v0.1.0"} {
				if !strings.Contains(body, want) {
					t.Errorf("the request %q lacks %q", body, want)
				}
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
// pending poll, and answers the key registration with keyStatus.
func loginServer(keyStatus int) *fakeServer {
	return &fakeServer{replies: map[string][]reply{
		"POST " + codePath:  {{status: http.StatusOK, body: `{"device_code":"dc","user_code":"WDJB-MJHT","verification_uri":"https://pairtime.example.org/device","verification_uri_complete":"https://pairtime.example.org/device?user_code=WDJB-MJHT","expires_in":900,"interval":5}`}},
		"POST " + tokenPath: {oauthErr("authorization_pending"), tokenReply(testToken)},
		"POST " + keyPath:   {{status: keyStatus, body: `{"error":"key_already_registered"}`}},
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
		{name: "a login keeps the token and key, registers the key, writes account.json and switches config.toml online", run: func(t *testing.T) {
			fake := loginServer(http.StatusCreated)
			sec := &memSecrets{}
			o := testOptions(t, fake, sec)
			var shown Code
			r, err := Login(context.Background(), o, testDevice, func(c Code) { shown = c })
			if err != nil {
				t.Fatal(err)
			}
			if shown.UserCode != "WDJB-MJHT" {
				t.Errorf("show got %+v", shown)
			}
			if string(sec.token) != testToken || len(sec.seed) != ed25519.SeedSize {
				t.Errorf("the keystore got token %q and a %d-byte seed", sec.token, len(sec.seed))
			}
			pub := ed25519.NewKeyFromSeed(sec.seed).Public().(ed25519.PublicKey)
			var body struct {
				PublicKey string `json:"public_key"`
				Signature string `json:"signature"`
			}
			if err := json.Unmarshal([]byte(fake.bodies[len(fake.bodies)-1]), &body); err != nil {
				t.Fatal(err)
			}
			sig, _ := base64.StdEncoding.DecodeString(body.Signature)
			if body.PublicKey != encodeKey(pub) || !ed25519.Verify(pub, KeyMessage(testDevice.ID, body.PublicKey), sig) {
				t.Errorf("the key registration %+v isn't the keystore's key, signed for this device", body)
			}
			if auth := fake.seen[len(fake.seen)-1].Header.Get("Authorization"); auth != "Bearer "+testToken {
				t.Errorf("the key registration's Authorization is %q", auth)
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
		{name: "a key the server refuses undoes the login: the keystore forgets it and the server removes the device", run: func(t *testing.T) {
			fake := loginServer(http.StatusConflict)
			sec := &memSecrets{}
			o := testOptions(t, fake, sec)
			if _, err := Login(context.Background(), o, testDevice, func(Code) {}); err == nil {
				t.Fatal("Login succeeded with the key refused")
			}
			if sec.token != nil || sec.forgot != 1 {
				t.Errorf("the keystore still has %q (forgot %d times)", sec.token, sec.forgot)
			}
			if fake.count("DELETE "+mePath) != 1 {
				t.Error("the server wasn't asked to remove the device")
			}
			if fake.count("POST "+keyPath) != 1 {
				t.Errorf("a refused key was sent %d times", fake.count("POST "+keyPath))
			}
			if _, err := Load(o.DataDir); !errors.Is(err, ErrNotLinked) {
				t.Errorf("account.json after a failed login: %v", err)
			}
			if _, err := os.Stat(o.ConfigFile); err == nil {
				t.Error("a failed login wrote config.toml")
			}
		}},
		{name: "an install that is linked already isn't linked again", run: func(t *testing.T) {
			fake := loginServer(http.StatusCreated)
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
			o := testOptions(t, fake, &memSecrets{})
			if _, err := Login(context.Background(), o, testDevice, func(Code) { t.Error("a code was shown") }); !errors.Is(err, ErrClosed) {
				t.Errorf("Login = %v, want ErrClosed", err)
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
			fake := loginServer(http.StatusCreated)
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

func TestLogoutWithoutKeystore(t *testing.T) {
	fake := loginServer(http.StatusCreated)
	sec := &memSecrets{}
	o := testOptions(t, fake, sec)
	if _, err := Login(context.Background(), o, testDevice, func(Code) {}); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		ok   func() bool
	}{
		{name: "a logout whose keystore can't be opened says so", ok: func() bool {
			_, err := Logout(context.Background(), Options{DataDir: o.DataDir, ConfigFile: o.ConfigFile})
			return errors.Is(err, ErrNoKeystore)
		}},
		{name: "it still deletes account.json, so nothing uploads", ok: func() bool {
			_, err := Load(o.DataDir)
			return errors.Is(err, ErrNotLinked)
		}},
		{name: "it still switches config.toml to local", ok: func() bool {
			cfg, _, _ := config.Load(o.ConfigFile)
			return cfg.Mode == config.Local
		}},
		{name: "it sends nothing to the server without the token", ok: func() bool {
			return fake.count("DELETE "+mePath) == 0
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !tt.ok() {
				t.Error("it didn't")
			}
		})
	}
}

func TestBrowserURL(t *testing.T) {
	tests := []struct {
		name string
		code Code
		want string // "" means none
	}{
		{name: "the complete URI on the server is opened", code: Code{VerificationURI: testServer + "/device", VerificationURIComplete: testServer + "/device?user_code=WDJB-MJHT"}, want: testServer + "/device?user_code=WDJB-MJHT"},
		{name: "without a complete URI the plain one is opened", code: Code{VerificationURI: testServer + "/device"}, want: testServer + "/device"},
		{name: "a page on another host isn't opened", code: Code{VerificationURI: "https://evil.example/device", VerificationURIComplete: "https://evil.example/device?user_code=X"}},
		{name: "a file URL isn't opened", code: Code{VerificationURIComplete: "file:///C:/Windows/System32/calc.exe"}},
		{name: "a bare path isn't opened, since Windows would run it", code: Code{VerificationURIComplete: `C:\Windows\System32\calc.exe`}},
		{name: "the same host over http isn't opened", code: Code{VerificationURIComplete: "http://pairtime.example.org/device"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := BrowserURL(tt.code, testServer)
			if ok != (tt.want != "") || got != tt.want {
				t.Errorf("BrowserURL = %q, %v; want %q", got, ok, tt.want)
			}
		})
	}
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
