// SPDX-License-Identifier: AGPL-3.0-or-later

package sigv4

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// suiteDir holds AWS's SigV4 test suite as awslabs/aws-c-auth v1.1.0
// ships it, one directory per case (testdata/aws-signing-test-suite/
// PROVENANCE.md). The files are the oracle: a test that disagrees with
// them is wrong, never the files.
var suiteDir = filepath.Join("testdata", "aws-signing-test-suite", "v4")

// suiteCases is how many cases the suite has at v1.1.0, so a directory
// lost in a copy fails the test instead of shrinking it.
const suiteCases = 38

// suiteContext is a case's context.json. Unknown fields are an error, so
// a re-pinned suite that adds a setting gets noticed.
type suiteContext struct {
	Credentials struct {
		AccessKeyID     string `json:"access_key_id"`
		SecretAccessKey string `json:"secret_access_key"`
		Token           string `json:"token"`
	} `json:"credentials"`
	ExpirationInSeconds int    `json:"expiration_in_seconds"`
	Normalize           bool   `json:"normalize"`
	Region              string `json:"region"`
	Service             string `json:"service"`
	SignBody            bool   `json:"sign_body"`
	Timestamp           string `json:"timestamp"`
	// OmitSessionToken means the token is added to the request after
	// signing, outside the signature.
	OmitSessionToken bool `json:"omit_session_token"`
}

// rawRequest is a request file of the suite: a request line whose target
// may hold spaces and raw UTF-8, header lines in order with folded
// continuation lines kept, and a body.
type rawRequest struct {
	method, target string
	headers        []pair
	body           string
}

func parseRawRequest(text string) (rawRequest, error) {
	head, body, _ := strings.Cut(text, "\n\n")
	lines := strings.Split(head, "\n")
	first := lines[0]
	sp, last := strings.IndexByte(first, ' '), strings.LastIndexByte(first, ' ')
	if sp < 0 || last <= sp || first[last+1:] != "HTTP/1.1" {
		return rawRequest{}, fmt.Errorf("request line %q isn't METHOD TARGET HTTP/1.1", first)
	}
	r := rawRequest{method: first[:sp], target: first[sp+1 : last], body: body}
	for _, l := range lines[1:] {
		switch {
		case l == "":
		case l[0] == ' ' || l[0] == '\t':
			if len(r.headers) == 0 {
				return rawRequest{}, errors.New("a continuation line comes before any header")
			}
			r.headers[len(r.headers)-1].value += "\n" + l
		default:
			name, value, ok := strings.Cut(l, ":")
			if !ok {
				return rawRequest{}, fmt.Errorf("header line %q has no colon", l)
			}
			r.headers = append(r.headers, pair{name, value})
		}
	}
	return r, nil
}

// httpRequest builds the request as Go's client would hold it before
// sending: the decoded path, the raw query, Host apart from the headers.
func (r rawRequest) httpRequest(t *testing.T) *http.Request {
	t.Helper()
	rawPath, rawQuery, _ := strings.Cut(r.target, "?")
	path, err := url.PathUnescape(rawPath)
	if err != nil {
		t.Fatal(err)
	}
	req := &http.Request{
		Method: r.method,
		URL:    &url.URL{Scheme: "https", Path: path, RawQuery: rawQuery},
		Header: make(http.Header),
	}
	for _, h := range r.headers {
		if strings.EqualFold(h.name, "host") {
			req.Host, req.URL.Host = h.value, h.value
			continue
		}
		req.Header.Add(h.name, h.value)
	}
	return req
}

// headerSet is a request's headers as sorted "name:value" strings with
// lowercase names, so two requests compare without regard to order.
func headerSet(hostValue string, h http.Header) []string {
	set := []string{"host:" + hostValue}
	for k, vs := range h {
		for _, v := range vs {
			set = append(set, strings.ToLower(k)+":"+v)
		}
	}
	slices.Sort(set)
	return set
}

func (r rawRequest) headerSet() []string {
	var set []string
	for _, h := range r.headers {
		set = append(set, strings.ToLower(h.name)+":"+h.value)
	}
	slices.Sort(set)
	return set
}

// querySet is a raw query's parameters, decoded, as sorted "name=value".
func querySet(t *testing.T, raw string) []string {
	t.Helper()
	ps, err := parseQuery(raw)
	if err != nil {
		t.Fatal(err)
	}
	set := make([]string, len(ps))
	for i, p := range ps {
		set[i] = p.name + "=" + p.value
	}
	slices.Sort(set)
	return set
}

type suiteCase struct {
	ctx     suiteContext
	request rawRequest
	at      time.Time
	files   map[string]string
}

func loadCase(t *testing.T, name string) suiteCase {
	t.Helper()
	dir := filepath.Join(suiteDir, name)
	read := func(file string) string {
		b, err := os.ReadFile(filepath.Join(dir, file))
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	var c suiteCase
	dec := json.NewDecoder(strings.NewReader(read("context.json")))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&c.ctx); err != nil {
		t.Fatalf("context.json: %v", err)
	}
	var err error
	if c.at, err = time.Parse(time.RFC3339, c.ctx.Timestamp); err != nil {
		t.Fatalf("context.json timestamp: %v", err)
	}
	if c.request, err = parseRawRequest(read("request.txt")); err != nil {
		t.Fatalf("request.txt: %v", err)
	}
	c.files = make(map[string]string)
	for _, mode := range []string{"header", "query"} {
		for _, part := range []string{"canonical-request", "string-to-sign", "signature", "signed-request"} {
			f := mode + "-" + part + ".txt"
			c.files[f] = read(f)
		}
	}
	return c
}

// signer is the case's signer. With omit_session_token set, the token is
// left out of the signature and the test adds it afterwards.
func (c suiteCase) signer() *Signer {
	s := &Signer{
		Credentials: Credentials{
			AccessKeyID:     c.ctx.Credentials.AccessKeyID,
			SecretAccessKey: c.ctx.Credentials.SecretAccessKey,
			SessionToken:    c.ctx.Credentials.Token,
		},
		Region:        c.ctx.Region,
		Service:       c.ctx.Service,
		NormalizePath: c.ctx.Normalize,
	}
	if c.ctx.OmitSessionToken {
		s.Credentials.SessionToken = ""
	}
	return s
}

func (c suiteCase) compare(t *testing.T, mode string, got signed) {
	t.Helper()
	for _, part := range []struct{ file, got string }{
		{mode + "-canonical-request.txt", got.canonicalRequest},
		{mode + "-string-to-sign.txt", got.stringToSign},
		{mode + "-signature.txt", got.signature},
	} {
		if want := c.files[part.file]; part.got != want {
			t.Errorf("%s differs\n got: %q\nwant: %q", part.file, part.got, want)
		}
	}
}

// TestAWSSigningTestSuite runs every case of AWS's SigV4 test suite. Each
// case name is the rule it checks, in AWS's words: header value trimming,
// duplicate and multi-line headers, path normalization, query ordering
// and encoding, session tokens, signed bodies. For each case it checks the
// canonical request, the string to sign, the signature and the signed
// request, once with the signature in the Authorization header and once in
// a presigned query string. Signed requests are compared as sets of
// headers and query parameters, because the order they are written in is
// aws-c-auth's choice, not part of SigV4.
func TestAWSSigningTestSuite(t *testing.T) {
	entries, err := os.ReadDir(suiteDir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			names = append(names, e.Name())
		}
	}
	if len(names) != suiteCases {
		t.Fatalf("%s has %d cases, want the %d of aws-c-auth v1.1.0", suiteDir, len(names), suiteCases)
	}
	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			c := loadCase(t, name)
			payloadHash := PayloadHash([]byte(c.request.body))

			t.Run("signed in the Authorization header", func(t *testing.T) {
				req := c.request.httpRequest(t)
				if c.ctx.SignBody {
					req.Header.Set("X-Amz-Content-Sha256", payloadHash)
				}
				got, err := c.signer().sign(req, payloadHash, c.at)
				if err != nil {
					t.Fatal(err)
				}
				c.compare(t, "header", got)
				if c.ctx.OmitSessionToken {
					req.Header.Set("X-Amz-Security-Token", c.ctx.Credentials.Token)
				}
				want, err := parseRawRequest(c.files["header-signed-request.txt"])
				if err != nil {
					t.Fatalf("header-signed-request.txt: %v", err)
				}
				if want.method != c.request.method || want.target != c.request.target || want.body != c.request.body {
					t.Errorf("signing changed the request line or body; want %s %s", want.method, want.target)
				}
				if got, want := headerSet(req.Host, req.Header), want.headerSet(); !slices.Equal(got, want) {
					t.Errorf("signed headers differ\n got: %q\nwant: %q", got, want)
				}
			})

			t.Run("presigned in the query string", func(t *testing.T) {
				req := c.request.httpRequest(t)
				before := headerSet(req.Host, req.Header)
				u, got, err := c.signer().presign(req, payloadHash, c.at, time.Duration(c.ctx.ExpirationInSeconds)*time.Second)
				if err != nil {
					t.Fatal(err)
				}
				c.compare(t, "query", got)
				if c.ctx.OmitSessionToken {
					u.RawQuery += "&X-Amz-Security-Token=" + uriEncode(c.ctx.Credentials.Token, true)
				}
				want, err := parseRawRequest(c.files["query-signed-request.txt"])
				if err != nil {
					t.Fatalf("query-signed-request.txt: %v", err)
				}
				wantPath, wantQuery, _ := strings.Cut(want.target, "?")
				gotPath, _, _ := strings.Cut(c.request.target, "?")
				if want.method != c.request.method || wantPath != gotPath || want.body != c.request.body {
					t.Errorf("presigning changed the method, path or body; want %s %s", want.method, wantPath)
				}
				if got, want := querySet(t, u.RawQuery), querySet(t, wantQuery); !slices.Equal(got, want) {
					t.Errorf("query parameters differ\n got: %q\nwant: %q", got, want)
				}
				if after := headerSet(req.Host, req.Header); !slices.Equal(after, before) {
					t.Errorf("Presign changed the request's headers: %q, was %q", after, before)
				}
				if got, want := headerSet(req.Host, req.Header), want.headerSet(); !slices.Equal(got, want) {
					t.Errorf("headers differ\n got: %q\nwant: %q", got, want)
				}
			})
		})
	}
}

// testSigner and testRequest are a signer and request for the tests
// below, which aren't from the suite.
func testSigner() *Signer {
	return &Signer{
		Credentials: Credentials{AccessKeyID: "AKIDTEST", SecretAccessKey: "secret-test-value", SessionToken: "token-test-value"},
		Region:      "auto",
		Service:     "s3",
	}
}

func testRequest(t *testing.T) *http.Request {
	t.Helper()
	req, err := http.NewRequest(http.MethodPut, "https://acct.r2.example/bucket/evidence/ab%20cd?x-id=PutObject", strings.NewReader("blob"))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-Amz-Content-Sha256", PayloadHash([]byte("blob")))
	req.Header.Set("If-None-Match", "*")
	req.Header.Set("User-Agent", "pairtimed-test")
	return req
}

var testTime = time.Date(2026, 11, 19, 3, 0, 0, 0, time.UTC)

func TestSignRefusesWhatItCantSign(t *testing.T) {
	tests := []struct {
		name    string
		edit    func(*Signer, *http.Request) string
		wantErr string
	}{
		{
			name:    "an empty secret access key is refused",
			edit:    func(s *Signer, _ *http.Request) string { s.Credentials.SecretAccessKey = ""; return "" },
			wantErr: "are both required",
		},
		{
			name:    "an access key id with a slash would break the scope and is refused",
			edit:    func(s *Signer, _ *http.Request) string { s.Credentials.AccessKeyID = "AKID/X"; return "" },
			wantErr: "credential scope",
		},
		{
			name:    "an empty region is refused",
			edit:    func(s *Signer, _ *http.Request) string { s.Region = ""; return "" },
			wantErr: "region",
		},
		{
			name:    "a region with a slash is refused",
			edit:    func(s *Signer, _ *http.Request) string { s.Region = "us/east"; return "" },
			wantErr: "region",
		},
		{
			name:    "a payload hash that isn't lowercase hex SHA-256 is refused",
			edit:    func(*Signer, *http.Request) string { return strings.ToUpper(PayloadHash(nil)) },
			wantErr: "payload hash",
		},
		{
			name: "an X-Amz-Content-Sha256 header that disagrees with the payload hash is refused",
			edit: func(_ *Signer, r *http.Request) string {
				r.Header.Set("X-Amz-Content-Sha256", EmptyPayloadHash)
				return ""
			},
			wantErr: "differ",
		},
		{
			name:    "a request without a host is refused",
			edit:    func(_ *Signer, r *http.Request) string { r.Host, r.URL.Host = "", ""; return "" },
			wantErr: "no host",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, req := testSigner(), testRequest(t)
			hash := tt.edit(s, req)
			if hash == "" {
				hash = PayloadHash([]byte("blob"))
			}
			err := s.Sign(req, hash, testTime)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("Sign error = %v, want one containing %q", err, tt.wantErr)
			}
			if req.Header.Get("Authorization") != "" {
				t.Error("a refused request still got an Authorization header")
			}
		})
	}
}

func TestSignLeavesUnsignedHeadersOut(t *testing.T) {
	tests := []struct {
		name, header string
		signed       bool
	}{
		{name: "User-Agent isn't signed, because proxies rewrite it", header: "user-agent", signed: false},
		{name: "If-None-Match is signed, so a write-once put can't lose it in transit", header: "if-none-match", signed: true},
		{name: "X-Amz-Content-Sha256 is signed", header: "x-amz-content-sha256", signed: true},
		{name: "the session token is signed", header: "x-amz-security-token", signed: true},
		{name: "the host is signed", header: "host", signed: true},
	}
	req := testRequest(t)
	got, err := testSigner().sign(req, PayloadHash([]byte("blob")), testTime)
	if err != nil {
		t.Fatal(err)
	}
	names := strings.Split(got.signedHeaders, ";")
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if slices.Contains(names, tt.header) != tt.signed {
				t.Errorf("SignedHeaders = %q; %s signed = %v, want %v", got.signedHeaders, tt.header, !tt.signed, tt.signed)
			}
		})
	}
}

func TestPresignExpiry(t *testing.T) {
	tests := []struct {
		name    string
		expires time.Duration
		ok      bool
	}{
		{name: "one second is the shortest expiry", expires: time.Second, ok: true},
		{name: "seven days is the longest expiry", expires: MaxPresignExpiry, ok: true},
		{name: "zero is refused", expires: 0},
		{name: "more than seven days is refused", expires: MaxPresignExpiry + time.Second},
		{name: "a fraction of a second is refused, since X-Amz-Expires counts seconds", expires: 1500 * time.Millisecond},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, _ := http.NewRequest(http.MethodGet, "https://acct.r2.example/bucket/key", nil)
			u, err := testSigner().Presign(req, UnsignedPayload, testTime, tt.expires)
			if (err == nil) != tt.ok {
				t.Fatalf("Presign(%s) error = %v, want ok = %v", tt.expires, err, tt.ok)
			}
			if tt.ok && !strings.Contains(u.RawQuery, "X-Amz-Signature=") {
				t.Errorf("presigned query %q has no signature", u.RawQuery)
			}
		})
	}
}

func TestVerify(t *testing.T) {
	creds := testSigner().Credentials
	tests := []struct {
		name string
		// edit changes the signed request before Verify sees it.
		edit    func(*http.Request)
		creds   Credentials
		now     time.Time
		wantErr bool
	}{
		{name: "a request signed with the credentials verifies", edit: func(*http.Request) {}},
		{name: "an unsigned header may change in transit", edit: func(r *http.Request) { r.Header.Set("User-Agent", "proxy/1") }},
		{name: "a changed signed header fails", edit: func(r *http.Request) { r.Header.Del("If-None-Match") }, wantErr: true},
		{name: "a changed path fails", edit: func(r *http.Request) { r.URL.Path = "/bucket/evidence/other" }, wantErr: true},
		{name: "a changed query fails", edit: func(r *http.Request) { r.URL.RawQuery = "x-id=GetObject" }, wantErr: true},
		{name: "a changed host fails", edit: func(r *http.Request) { r.Host = "other.example" }, wantErr: true},
		{name: "another secret fails", edit: func(*http.Request) {}, creds: Credentials{AccessKeyID: "AKIDTEST", SecretAccessKey: "other"}, wantErr: true},
		{name: "another access key id fails", edit: func(*http.Request) {}, creds: Credentials{AccessKeyID: "AKIDOTHER", SecretAccessKey: "secret-test-value"}, wantErr: true},
		{name: "a request older than the allowed skew fails", edit: func(*http.Request) {}, now: testTime.Add(16 * time.Minute), wantErr: true},
		{name: "a request from the future beyond the skew fails", edit: func(*http.Request) {}, now: testTime.Add(-16 * time.Minute), wantErr: true},
		{name: "a missing Authorization header fails", edit: func(r *http.Request) { r.Header.Del("Authorization") }, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := testRequest(t)
			if err := testSigner().Sign(req, PayloadHash([]byte("blob")), testTime); err != nil {
				t.Fatal(err)
			}
			tt.edit(req)
			c := creds
			if tt.creds.AccessKeyID != "" {
				c = tt.creds
			}
			now := tt.now
			if now.IsZero() {
				now = testTime.Add(time.Minute)
			}
			err := Verify(req, PayloadHash([]byte("blob")), c, "auto", "s3", now, 15*time.Minute)
			if (err != nil) != tt.wantErr {
				t.Fatalf("Verify error = %v, want an error: %v", err, tt.wantErr)
			}
			if err != nil && !errors.Is(err, ErrSignature) {
				t.Errorf("Verify error %v doesn't wrap ErrSignature", err)
			}
		})
	}
}

func TestCredentialsNeverPrintSecrets(t *testing.T) {
	c := testSigner().Credentials
	var logged bytes.Buffer
	slog.New(slog.NewTextHandler(&logged, nil)).Info("signing", "creds", c)
	outputs := []struct{ name, text string }{
		{name: "%v prints no secret", text: fmt.Sprintf("%v", c)},
		{name: "%+v prints no secret", text: fmt.Sprintf("%+v", c)},
		{name: "%#v prints no secret", text: fmt.Sprintf("%#v", c)},
		{name: "%s of a pointer prints no secret", text: fmt.Sprintf("%s", &c)},
		{name: "slog prints no secret", text: logged.String()},
	}
	for _, o := range outputs {
		t.Run(o.name, func(t *testing.T) {
			for _, secret := range []string{c.SecretAccessKey, c.SessionToken} {
				if strings.Contains(o.text, secret) {
					t.Errorf("output %q contains a secret", o.text)
				}
			}
			if !strings.Contains(o.text, "secret set") {
				t.Errorf("output %q doesn't say the secret is set", o.text)
			}
		})
	}
}
