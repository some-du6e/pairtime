// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/internal/server/auth"
	"github.com/some-du6e/pairtime/internal/server/auth/hcafake"
	"github.com/some-du6e/pairtime/internal/server/backup"
	"github.com/some-du6e/pairtime/internal/server/device"
	"github.com/some-du6e/pairtime/internal/server/ingest"
	"github.com/some-du6e/pairtime/pkg/event"
)

// liveEnv names the loopback port of TestLiveServer's control server,
// which rehearsal.yml's live dashboard step sets for Playwright.
const liveEnv = "PAIRTIME_LIVE_CONTROL"

// TestLiveServer is the server rehearsal.yml's Playwright check of WP-SV8
// runs against (web/tests/live/stream.live.ts): pairtimed serve as main
// runs it, on SQLite, with sign-in through the fake HCA, behind a TLS
// proxy that stands where Cloudflare's edge does, so the browser keeps
// pairtimed's Secure cookies as it does in production. The browser signs
// itself in. The harness links a computer through the device grant, signed
// in as the same person, and sends that computer's signed uploads when
// the spec asks on the control server. It runs only when liveEnv names the
// control port, and serves until it is told to stop.
func TestLiveServer(t *testing.T) {
	port := os.Getenv(liveEnv)
	if port == "" {
		t.Skipf("%s isn't set; rehearsal.yml's live dashboard step sets it for Playwright", liveEnv)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	hca := hcafake.New(t)
	// The edge: TLS in front of pairtimed's plain listener, which passes
	// the browser's Host on, as cloudflared does.
	var origin atomic.Pointer[url.URL]
	edge := httptest.NewUnstartedServer(&httputil.ReverseProxy{
		Rewrite: func(r *httputil.ProxyRequest) {
			r.SetURL(origin.Load())
			r.Out.Host = r.In.Host
		},
		FlushInterval: -1,
	})
	edge.EnableHTTP2 = true
	edge.StartTLS()
	t.Cleanup(edge.Close)
	hca.AddRedirectURI(edge.URL + auth.CallbackPath)

	configPath, dbPath := writeSignInConfig(t, hca.URL, "signups_open = true\n")
	setBaseURL(t, configPath, edge.URL)
	if _, err := backup.Generate(backup.KeysDir(filepath.Dir(dbPath)), rand.Reader); err != nil {
		t.Fatal(err)
	}
	public, _ := startServe(t, configPath,
		"PAIRTIME_HCA_CLIENT_SECRET="+hcafake.ClientSecret,
		"PAIRTIME_EMAIL_KEY="+strings.Repeat("5a", 32))
	u, err := url.Parse(public)
	if err != nil {
		t.Fatal(err)
	}
	origin.Store(u)

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	browser := &http.Client{Transport: edge.Client().Transport, Jar: jar, Timeout: 30 * time.Second}
	dev, err := linkDevice(edge.URL, browser)
	if err != nil {
		t.Fatal(err)
	}

	ln, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", port))
	if err != nil {
		t.Fatal(err)
	}
	control := &http.Server{Handler: dev.control(edge.URL, browser), ReadHeaderTimeout: 10 * time.Second}
	go control.Serve(ln)
	t.Cleanup(func() { control.Close() })
	t.Logf("pairtimed is at %s behind the edge at %s; the control server is on %s", public, edge.URL, ln.Addr())
	<-ctx.Done()
}

// liveDevice is the computer the harness linked: its id, key, token, the
// request counter its uploads count up, one upload at a time under mu, and
// the minute its first upload counted from and which way (liveUpload).
type liveDevice struct {
	id    string
	key   ed25519.PrivateKey
	token string
	mu    sync.Mutex
	req   uint64
	first time.Time
	step  time.Duration
}

// linkDevice links a new computer to the account of the fake HCA's
// identity through the device grant (RFC 8628), as pairtime login does:
// browser signs in and approves the code on /device.
func linkDevice(base string, browser *http.Client) (*liveDevice, error) {
	pub, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	d := &liveDevice{id: "d_live_dashboard_e2e", key: key}
	pubText := base64.StdEncoding.EncodeToString(pub)
	var code struct {
		DeviceCode string `json:"device_code"`
		UserCode   string `json:"user_code"`
	}
	if err := postForm(browser, base+device.CodePath, url.Values{
		"client_id": {device.ClientID}, "device_id": {d.id}, "os": {device.OSLinux}, "client_build": {"v0.0.0-live"},
		"public_key": {pubText}, "key_signature": {base64.StdEncoding.EncodeToString(ed25519.Sign(key, device.KeyMessage(d.id, pubText)))},
	}, &code); err != nil {
		return nil, fmt.Errorf("asking for a device code: %w", err)
	}

	// Sign in, which ends on /device, then open the code's page and
	// approve it, as a person does in a browser.
	resp, err := browser.Get(base + auth.LoginPath + "?return_to=" + url.QueryEscape(device.PagePath))
	if err != nil {
		return nil, err
	}
	page, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || resp.Request.URL.Path != device.PagePath {
		return nil, fmt.Errorf("signing in ended at %s with %d:\n%s", resp.Request.URL, resp.StatusCode, page)
	}
	resp, err = browser.Get(base + device.PagePath + "?user_code=" + url.QueryEscape(code.UserCode))
	if err != nil {
		return nil, err
	}
	page, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	m := liveCSRF.FindStringSubmatch(string(page))
	if resp.StatusCode != http.StatusOK || m == nil {
		return nil, fmt.Errorf("the page of code %s answered %d:\n%s", code.UserCode, resp.StatusCode, page)
	}
	req, err := http.NewRequest(http.MethodPost, base+device.DecisionPath, strings.NewReader(url.Values{
		"csrf": {m[1]}, "user_code": {code.UserCode}, "decision": {"approve"},
	}.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", base)
	resp, err = browser.Do(req)
	if err != nil {
		return nil, err
	}
	page, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(page), "<h1>Linked</h1>") {
		return nil, fmt.Errorf("approving the code answered %d:\n%s", resp.StatusCode, page)
	}

	var tok struct {
		AccessToken string `json:"access_token"`
		DeviceID    string `json:"device_id"`
	}
	if err := postForm(browser, base+device.TokenPath, url.Values{
		"grant_type": {device.GrantType}, "client_id": {device.ClientID}, "device_code": {code.DeviceCode},
	}, &tok); err != nil {
		return nil, fmt.Errorf("asking for the device token: %w", err)
	}
	if tok.AccessToken == "" || tok.DeviceID != d.id {
		return nil, fmt.Errorf("the token answer has token %t and device %q", tok.AccessToken != "", tok.DeviceID)
	}
	d.token = tok.AccessToken
	return d, nil
}

// liveCSRF finds the CSRF token in a page's form.
var liveCSRF = regexp.MustCompile(`name="csrf" value="([^"]+)"`)

// postForm posts form to u and decodes a 200 answer's JSON into v.
func postForm(c *http.Client, u string, form url.Values, v any) error {
	resp, err := c.PostForm(u, form)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s answered %d: %s", u, resp.StatusCode, b)
	}
	return json.Unmarshal(b, v)
}

// liveUpload is what the spec asks the control server to upload: one
// minute's revision, Minute minutes before the minute of the run's first
// upload (after it near midnight, so every minute is that day in UTC, the
// account's zone), crediting seconds, a multiple of 15, to one project as
// prompting at level A. Counting from the first upload, not from each
// one's own time, keeps the spec's minutes apart however long its run
// takes.
type liveUpload struct {
	Minute  int     `json:"minute"`
	Rev     uint32  `json:"rev"`
	Seconds float64 `json:"seconds"`
	State   string  `json:"state"`
}

// control serves the spec: GET /ready says where the edge is once all is
// up, and POST /upload sends one upload and answers with ingest's status
// and body.
func (d *liveDevice) control(base string, c *http.Client) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /ready", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"base": base})
	})
	mux.HandleFunc("POST /upload", func(w http.ResponseWriter, r *http.Request) {
		var up liveUpload
		if err := json.NewDecoder(r.Body).Decode(&up); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		status, body, err := d.upload(c, base, up, time.Now())
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"status": status, "body": json.RawMessage(body)})
	})
	return mux
}

// upload sends up as a signed ingest-v1 upload (DESIGN 4.5) and returns
// the server's status and body.
func (d *liveDevice) upload(c *http.Client, base string, up liveUpload, now time.Time) (int, []byte, error) {
	if up.Seconds < 0 || up.Seconds > 60 || float64(int(up.Seconds/15))*15 != up.Seconds {
		return 0, nil, errors.New("seconds must be 0, 15, 30, 45 or 60")
	}
	if up.Minute < 0 || up.Minute > 3 {
		return 0, nil, errors.New("minute must be 0 to 3")
	}
	// One upload at a time, from taking its counter to reading its answer:
	// ingest refuses a counter at or below one it has already taken
	// (counter_stale), so two that overlapped could land out of order.
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.first.IsZero() {
		d.first, d.step = now.UTC().Truncate(time.Minute), -time.Minute
		if d.first.Add(-3*time.Minute).Day() != d.first.Day() {
			// Near midnight, minutes after the first, which ingest takes
			// up to 300 s ahead.
			d.step = time.Minute
		}
	}
	minute := d.first.Add(time.Duration(up.Minute) * d.step)
	mb := event.MinuteBucket{
		V:         "1.0.0",
		Params:    "sha256:" + strings.Repeat("ab", 32),
		Minute:    minute,
		Device:    d.id,
		Slots:     make([]event.BucketSlot, 4),
		CreditedS: up.Seconds,
		State:     event.BucketState(up.State),
		Witness:   event.Witnessed,
		Rev:       up.Rev,
	}
	for i := range int(up.Seconds / 15) {
		mb.Slots[i] = event.BucketSlot{Project: "p_live_project", Credited: 15, Why: event.ReasonPrompt, Kind: event.ActivityPrompting, Ev: event.EvidenceA}
	}
	if up.Seconds < 60 {
		mb.Uncredited = map[event.Reason]float64{event.ReasonAbsent: 60 - up.Seconds}
	}
	data, err := json.Marshal(map[string]any{"buckets": []any{mb.Shareable()}})
	if err != nil {
		return 0, nil, err
	}
	var gz bytes.Buffer
	zw := gzip.NewWriter(&gz)
	zw.Write(data)
	if err := zw.Close(); err != nil {
		return 0, nil, err
	}
	raw := gz.Bytes()
	sum := sha256.Sum256(raw)
	bodySHA := hex.EncodeToString(sum[:])
	d.req++
	reqText := strconv.FormatUint(d.req, 10)
	timeText := strconv.FormatInt(now.UnixMilli(), 10)
	req, err := http.NewRequest(http.MethodPost, base+ingest.BatchesPath, bytes.NewReader(raw))
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Authorization", "Bearer "+d.token)
	req.Header.Set(ingest.HeaderIdempotencyKey, uuidV7(now))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Content-Encoding", "gzip")
	req.Header.Set(ingest.HeaderDevice, d.id)
	req.Header.Set(ingest.HeaderReq, reqText)
	req.Header.Set(ingest.HeaderTime, timeText)
	req.Header.Set(ingest.HeaderBodySHA256, bodySHA)
	req.Header.Set(ingest.HeaderSignature, "ed25519="+base64.StdEncoding.EncodeToString(ed25519.Sign(d.key, ingest.Message(d.id, reqText, timeText, bodySHA))))
	resp, err := c.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	return resp.StatusCode, body, err
}

// uuidV7 returns a UUIDv7 for t (RFC 9562 5.7), the form ingest-v1 asks of
// an Idempotency-Key.
func uuidV7(t time.Time) string {
	var b [16]byte
	rand.Read(b[:])
	ms := uint64(t.UnixMilli())
	for i := range 6 {
		b[i] = byte(ms >> (40 - 8*i))
	}
	b[6] = b[6]&0x0f | 0x70
	b[8] = b[8]&0x3f | 0x80
	h := hex.EncodeToString(b[:])
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32]
}
