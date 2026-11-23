// SPDX-License-Identifier: AGPL-3.0-or-later

package wakatime

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/internal/agent/peer"
	"github.com/some-du6e/pairtime/internal/jsonschema"
)

// captures is wakatime-cli v2.28.4's requests, captured in CI, and
// responses the shapes of Hackatime's answers, copied from its source.
const (
	captures  = "../../../testdata/fixtures/wakatime/2.28.4/linux"
	responses = "../../../testdata/upstream/hackatime/responses"
)

// captureKeys are the two keys the capture's config held.
type captureKeys struct{ Pairtime, Hackatime string }

func loadKeys(t *testing.T) captureKeys {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(captures, "local-only", "capture.json"))
	if err != nil {
		t.Fatal(err)
	}
	var c struct {
		Keys captureKeys `json:"keys"`
	}
	if err := json.Unmarshal(b, &c); err != nil || c.Keys.Pairtime == "" {
		t.Fatalf("capture.json has no keys: %v", err)
	}
	return c.Keys
}

// capturedRequest is one request the capture's stand-in pairtime took.
type capturedRequest struct {
	name string
	meta map[string]string
	body []byte
}

func loadRun(t *testing.T, run string) []capturedRequest {
	t.Helper()
	metas, err := filepath.Glob(filepath.Join(captures, run, "requests", "*.meta"))
	if err != nil || len(metas) == 0 {
		t.Fatalf("no requests for %s: %v", run, err)
	}
	var out []capturedRequest
	for _, m := range metas {
		b, err := os.ReadFile(m)
		if err != nil {
			t.Fatal(err)
		}
		meta := map[string]string{}
		for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
			k, v, _ := strings.Cut(line, "=")
			meta[k] = v
		}
		if meta["server"] != "pairtime" {
			continue
		}
		c := capturedRequest{name: filepath.Base(m), meta: meta}
		if meta["method"] == http.MethodPost {
			if c.body, err = os.ReadFile(strings.TrimSuffix(m, ".meta") + ".json"); err != nil {
				t.Fatal(err)
			}
		}
		out = append(out, c)
	}
	return out
}

// httpRequest rebuilds a captured request: its method, path, body and the
// headers whose values the capture kept, with the Basic credential
// wakatime-cli sends for key.
func (c capturedRequest) httpRequest(key string) *http.Request {
	r := httptest.NewRequest(c.meta["method"], c.meta["path"], bytes.NewReader(c.body))
	for _, h := range []string{"Content-Type", "Accept", "User-Agent", "Timezone", "X-Machine-Name"} {
		if v := c.meta[strings.ToLower(strings.ReplaceAll(h, "-", "_"))]; v != "" {
			r.Header.Set(h, v)
		}
	}
	r.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(key)))
	return r
}

func loadSchema(t *testing.T, name string) *jsonschema.Schema {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(responses, name+".schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	s, err := jsonschema.Parse(b)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return s
}

func mustValidate(t *testing.T, s *jsonschema.Schema, what string, data []byte) {
	t.Helper()
	errs, err := s.ValidateJSON(data)
	if err != nil {
		t.Fatalf("%s: %v\n%s", what, err, data)
	}
	if len(errs) > 0 {
		t.Fatalf("%s doesn't match Hackatime's shape: %s\n%s", what, strings.Join(errs, "; "), data)
	}
}

// recorder is an Accept that keeps every batch, and fails while fail is
// above 0.
type recorder struct {
	batches []Batch
	fail    int
}

func (rec *recorder) accept(b Batch) error {
	if rec.fail > 0 {
		rec.fail--
		return errors.New("the ledger is closed")
	}
	rec.batches = append(rec.batches, b)
	return nil
}

func (rec *recorder) heartbeats() int {
	n := 0
	for _, b := range rec.batches {
		n += len(b.Heartbeats)
	}
	return n
}

// capturedNow is just after the capture ran, so no captured heartbeat is
// older than Horizon.
var capturedNow = time.Date(2026, 11, 23, 15, 0, 0, 0, time.UTC)

func newEndpoint(t *testing.T, rec *recorder, opts Options) *Endpoint {
	t.Helper()
	if opts.Key == "" {
		opts.Key = loadKeys(t).Pairtime
	}
	if rec != nil {
		opts.Accept = rec.accept
	}
	if opts.Now == nil {
		opts.Now = func() time.Time { return capturedNow }
	}
	e, err := New(opts)
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func serve(e *Endpoint, r *http.Request) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	e.ServeHTTP(w, r)
	return w
}

// bulkAnswer is a heartbeats.bulk answer, read by position.
type bulkAnswer struct {
	Responses [][2]json.RawMessage `json:"responses"`
}

// checkBulk checks a bulk answer against Hackatime's shapes, the entry
// bodies included, and returns each entry's status.
func checkBulk(t *testing.T, body []byte) []int {
	t.Helper()
	mustValidate(t, loadSchema(t, "heartbeats_bulk"), "the bulk answer", body)
	var a bulkAnswer
	if err := json.Unmarshal(body, &a); err != nil {
		t.Fatal(err)
	}
	rowSchema, refusalSchema := loadSchema(t, "heartbeat"), loadSchema(t, "heartbeat_error")
	statuses := make([]int, len(a.Responses))
	for i, entry := range a.Responses {
		if err := json.Unmarshal(entry[1], &statuses[i]); err != nil {
			t.Fatalf("entry %d's status: %v", i, err)
		}
		switch statuses[i] {
		case http.StatusCreated:
			mustValidate(t, rowSchema, fmt.Sprintf("entry %d's row", i), entry[0])
		case http.StatusBadRequest:
			mustValidate(t, refusalSchema, fmt.Sprintf("entry %d's refusal", i), entry[0])
		default:
			t.Fatalf("entry %d has status %d, want 201 or 400", i, statuses[i])
		}
	}
	return statuses
}

func TestCapturedRequests(t *testing.T) {
	keys := loadKeys(t)
	for _, run := range []string{"dual-send", "replay", "ai-sync", "local-only"} {
		t.Run("every request of the "+run+" run gets Hackatime's answer", func(t *testing.T) {
			rec := &recorder{}
			e := newEndpoint(t, rec, Options{Key: keys.Pairtime})
			sent := 0
			for _, c := range loadRun(t, run) {
				w := serve(e, c.httpRequest(keys.Pairtime))
				switch c.meta["path"] {
				case Prefix + "/users/current/heartbeats.bulk":
					if w.Code != http.StatusCreated {
						t.Fatalf("%s: status %d, want 201: %s", c.name, w.Code, w.Body)
					}
					var items []json.RawMessage
					if err := json.Unmarshal(c.body, &items); err != nil {
						t.Fatal(err)
					}
					sent += len(items)
					statuses := checkBulk(t, w.Body.Bytes())
					if len(statuses) != len(items) {
						t.Fatalf("%s: %d entries for %d heartbeats", c.name, len(statuses), len(items))
					}
					for i, s := range statuses {
						if s != http.StatusCreated {
							t.Errorf("%s: heartbeat %d got %d, want 201", c.name, i, s)
						}
					}
				case Prefix + "/users/current/statusbar/today":
					if w.Code != http.StatusOK {
						t.Fatalf("%s: status %d, want 200: %s", c.name, w.Code, w.Body)
					}
					mustValidate(t, loadSchema(t, "statusbar_today"), c.name, w.Body.Bytes())
				default:
					t.Fatalf("%s: the capture holds a request to %s", c.name, c.meta["path"])
				}
			}
			if run == "replay" {
				// The third request is the first again.
				sent--
			}
			if got := rec.heartbeats(); got != sent {
				t.Errorf("Accept took %d heartbeats, want %d", got, sent)
			}
		})
	}
}

func heartbeatJSON(entity string, t float64) string {
	return fmt.Sprintf(`{"entity":%q,"type":"file","time":%v,"user_agent":"wakatime/v2.28.4 (linux) go1.27.3 vscode/1.106.3 vscode-wakatime/25.5.1"}`, entity, t)
}

func bulkRequest(key, body string) *http.Request {
	r := httptest.NewRequest(http.MethodPost, Prefix+"/users/current/heartbeats.bulk", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(key)))
	return r
}

func TestReplays(t *testing.T) {
	keys := loadKeys(t)
	replay := loadRun(t, "replay")
	if len(replay) != 3 || !bytes.Equal(replay[0].body, replay[2].body) {
		t.Fatalf("the replay run's third request isn't its first again")
	}
	recent := float64(capturedNow.Add(-time.Hour).Unix())
	cases := []struct {
		name   string
		fail   int
		bodies []string
		// codes are each request's status, and taken the heartbeats
		// Accept was given in all.
		codes []int
		taken int
		// same says the answers to these two requests are equal.
		same [2]int
	}{
		{
			name:   "a heartbeat wakatime-cli replayed after a failed send is answered as before and passed on once",
			bodies: []string{string(replay[0].body), string(replay[1].body), string(replay[2].body)},
			codes:  []int{201, 201, 201}, taken: 2, same: [2]int{0, 2},
		},
		{
			name:   "two copies of a heartbeat in one request are both answered and passed on once",
			bodies: []string{"[" + heartbeatJSON("a.go", recent) + "," + heartbeatJSON("a.go", recent) + "]"},
			codes:  []int{201}, taken: 1,
		},
		{
			name:   "a request answered 503 because Accept failed is passed on when it comes again",
			fail:   1,
			bodies: []string{"[" + heartbeatJSON("a.go", recent) + "]", "[" + heartbeatJSON("a.go", recent) + "]"},
			codes:  []int{503, 201}, taken: 1,
		},
		{
			name:   "a heartbeat older than the horizon is answered as taken and passed on nowhere",
			bodies: []string{"[" + heartbeatJSON("a.go", float64(capturedNow.Add(-Horizon-time.Hour).Unix())) + "]"},
			codes:  []int{201}, taken: 0,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rec := &recorder{fail: c.fail}
			e := newEndpoint(t, rec, Options{Key: keys.Pairtime})
			var answers [][]byte
			for i, body := range c.bodies {
				w := serve(e, bulkRequest(keys.Pairtime, body))
				if w.Code != c.codes[i] {
					t.Fatalf("request %d: status %d, want %d: %s", i, w.Code, c.codes[i], w.Body)
				}
				if w.Code == http.StatusCreated {
					checkBulk(t, w.Body.Bytes())
				}
				answers = append(answers, w.Body.Bytes())
			}
			if got := rec.heartbeats(); got != c.taken {
				t.Errorf("Accept took %d heartbeats, want %d", got, c.taken)
			}
			if c.same != [2]int{} && !bytes.Equal(answers[c.same[0]], answers[c.same[1]]) {
				t.Errorf("answers differ:\n%s\n%s", answers[c.same[0]], answers[c.same[1]])
			}
		})
	}
}

func TestAuth(t *testing.T) {
	keys := loadKeys(t)
	basic := func(s string) string { return "Basic " + base64.StdEncoding.EncodeToString([]byte(s)) }
	body := "[" + heartbeatJSON("a.go", float64(capturedNow.Unix())) + "]"
	cases := []struct {
		name, method, path, auth string
		want                     int
	}{
		{"Basic with the key alone, as wakatime-cli sends it, is taken", "POST", "/users/current/heartbeats.bulk", basic(keys.Pairtime), 201},
		{"Bearer with the key is taken", "POST", "/users/current/heartbeats.bulk", "Bearer " + keys.Pairtime, 201},
		{"the scheme's case doesn't matter", "POST", "/users/current/heartbeats.bulk", "bearer " + keys.Pairtime, 201},
		{"the key in api_key is taken", "POST", "/users/current/heartbeats.bulk?api_key=" + keys.Pairtime, "", 201},
		{"the user in the path isn't read", "POST", "/users/someone/heartbeats.bulk", basic(keys.Pairtime), 201},
		{"no credential is refused", "POST", "/users/current/heartbeats.bulk", "", 401},
		{"another key is refused", "POST", "/users/current/heartbeats.bulk", basic(keys.Hackatime), 401},
		{"Bearer with another key is refused, and api_key isn't read then", "POST", "/users/current/heartbeats.bulk?api_key=" + keys.Pairtime, "Bearer " + keys.Hackatime, 401},
		{"Basic of the key and a colon names no key", "POST", "/users/current/heartbeats.bulk", basic(keys.Pairtime + ":"), 401},
		{"Basic that isn't base64 is refused", "POST", "/users/current/heartbeats.bulk", "Basic " + keys.Pairtime, 401},
		{"the single heartbeat route needs the key too", "POST", "/users/current/heartbeats", "", 401},
		{"the status bar needs the key too", "GET", "/users/current/statusbar/today", "", 401},
		{"the status bar takes it", "GET", "/users/current/statusbar/today", basic(keys.Pairtime), 200},
	}
	errSchema := loadSchema(t, "error")
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := newEndpoint(t, &recorder{}, Options{Key: keys.Pairtime})
			var r *http.Request
			if c.method == http.MethodGet {
				r = httptest.NewRequest(c.method, Prefix+c.path, nil)
			} else {
				b := body
				if strings.Contains(c.path, "heartbeats?") || strings.HasSuffix(c.path, "/heartbeats") {
					b = heartbeatJSON("a.go", float64(capturedNow.Unix()))
				}
				r = httptest.NewRequest(c.method, Prefix+c.path, strings.NewReader(b))
			}
			if c.auth != "" {
				r.Header.Set("Authorization", c.auth)
			}
			w := serve(e, r)
			if w.Code != c.want {
				t.Fatalf("status %d, want %d: %s", w.Code, c.want, w.Body)
			}
			if c.want == http.StatusUnauthorized {
				mustValidate(t, errSchema, "the refusal", w.Body.Bytes())
				if !strings.Contains(w.Body.String(), `"Unauthorized"`) {
					t.Errorf("body %s, want Hackatime's Unauthorized", w.Body)
				}
			}
		})
	}
}

func TestBodies(t *testing.T) {
	keys := loadKeys(t)
	now := float64(capturedNow.Unix())
	good := heartbeatJSON("a.go", now)
	many := make([]string, MaxBulk+1)
	for i := range many {
		many[i] = heartbeatJSON(fmt.Sprintf("f%d.go", i), now)
	}
	cases := []struct {
		name, path, contentType, body string
		want                          int
		// entries are a bulk answer's statuses, and contains a string its
		// body must hold.
		entries  []int
		contains string
	}{
		{name: "a JSON array of heartbeats is taken", path: "heartbeats.bulk", body: "[" + good + "]", want: 201, entries: []int{201}},
		{name: "an object holding the array under heartbeats is taken", path: "heartbeats.bulk", body: `{"heartbeats":[` + good + `]}`, want: 201, entries: []int{201}},
		{name: "the array under hackatime's heartbeats is taken, as Hackatime takes it", path: "heartbeats.bulk", body: `{"hackatime":{"heartbeats":[` + good + `]}}`, want: 201, entries: []int{201}},
		{name: "JSON sent as text/plain is taken", path: "heartbeats.bulk", contentType: "text/plain", body: "[" + good + "]", want: 201, entries: []int{201}},
		{name: "a bad heartbeat gets a refusal in its place, with 400, and the rest are taken", path: "heartbeats.bulk",
			body: "[" + good + `,{"entity":"b.go","time":42},"x",` + heartbeatJSON("c.go", now) + "]", want: 201, entries: []int{201, 400, 400, 201}, contains: "InvalidHeartbeatTime"},
		{name: "MaxBulk heartbeats are taken", path: "heartbeats.bulk", body: "[" + strings.Join(many[:MaxBulk], ",") + "]", want: 201},
		{name: "more than MaxBulk heartbeats are refused whole, as Hackatime refuses them", path: "heartbeats.bulk", body: "[" + strings.Join(many, ",") + "]", want: 400, contains: msgTooMany},
		{name: "an empty array is refused", path: "heartbeats.bulk", body: "[]", want: 400, contains: msgNoData},
		{name: "null is refused", path: "heartbeats.bulk", body: "null", want: 400, contains: msgNoData},
		{name: "a body that isn't JSON is refused", path: "heartbeats.bulk", body: "entity=a.go", want: 400, contains: msgNoData},
		{name: "a body over a mebibyte is refused", path: "heartbeats.bulk", body: "[" + strings.Repeat(" ", maxBody) + good + "]", want: 413, contains: msgTooLarge},
		{name: "one heartbeat is answered 202 with its row", path: "heartbeats", body: good, want: 202, contains: `"entity":"a.go"`},
		{name: "an array's first heartbeat is the one taken", path: "heartbeats", body: "[" + good + "," + heartbeatJSON("b.go", now) + "]", want: 202, contains: `"entity":"a.go"`},
		{name: "a heartbeat under hackatime is taken", path: "heartbeats", body: `{"hackatime":` + good + `}`, want: 202, contains: `"entity":"a.go"`},
		{name: "one refused heartbeat is answered 400 with Hackatime's refusal body", path: "heartbeats", body: `{"time":1795442779}`, want: 400, contains: `"type":"InvalidHeartbeat"`},
		{name: "an empty object holds no heartbeat", path: "heartbeats", body: `{}`, want: 400, contains: msgNoData},
	}
	schemas := map[int]string{202: "heartbeat", 413: "error"}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := newEndpoint(t, &recorder{}, Options{Key: keys.Pairtime})
			r := httptest.NewRequest(http.MethodPost, Prefix+"/users/current/"+c.path, strings.NewReader(c.body))
			if c.contentType != "" {
				r.Header.Set("Content-Type", c.contentType)
			}
			r.Header.Set("Authorization", "Bearer "+keys.Pairtime)
			w := serve(e, r)
			if w.Code != c.want {
				t.Fatalf("status %d, want %d: %s", w.Code, c.want, w.Body)
			}
			switch {
			case w.Code == 201:
				statuses := checkBulk(t, w.Body.Bytes())
				if c.entries != nil && fmt.Sprint(statuses) != fmt.Sprint(c.entries) {
					t.Errorf("entries %v, want %v", statuses, c.entries)
				}
			case w.Code == 400 && c.path == "heartbeats" && !strings.Contains(c.contains, msgNoData):
				mustValidate(t, loadSchema(t, "heartbeat_error"), "the refusal", w.Body.Bytes())
			case w.Code == 400:
				mustValidate(t, loadSchema(t, "error"), "the refusal", w.Body.Bytes())
			default:
				mustValidate(t, loadSchema(t, schemas[w.Code]), "the answer", w.Body.Bytes())
			}
			if !strings.Contains(w.Body.String(), c.contains) {
				t.Errorf("body %s doesn't hold %s", w.Body, c.contains)
			}
		})
	}
}

func TestBatch(t *testing.T) {
	keys := loadKeys(t)
	passed := peer.Result{Reason: peer.Passed, PID: 4242, Editor: 1}
	cases := []struct {
		name      string
		peer      func(*http.Request) peer.Result
		body      string
		userAgent string
		wantPass  bool
		wantUA    string
	}{
		{
			name: "a batch carries the peer check's result for the connection",
			peer: func(*http.Request) peer.Result { return passed },
			body: heartbeatJSON("a.go", float64(capturedNow.Unix())), wantPass: true,
			wantUA: "wakatime/v2.28.4 (linux) go1.27.3 vscode/1.106.3 vscode-wakatime/25.5.1",
		},
		{
			name:   "with no peer check every connection fails it",
			body:   heartbeatJSON("a.go", float64(capturedNow.Unix())),
			wantUA: "wakatime/v2.28.4 (linux) go1.27.3 vscode/1.106.3 vscode-wakatime/25.5.1",
		},
		{
			name: "a heartbeat with no user agent takes the request's",
			peer: func(*http.Request) peer.Result { return passed },
			body: fmt.Sprintf(`{"entity":"a.go","time":%d}`, capturedNow.Unix()), userAgent: "curl/8.11.1",
			wantPass: true, wantUA: "curl/8.11.1",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rec := &recorder{}
			e := newEndpoint(t, rec, Options{Key: keys.Pairtime, Peer: c.peer})
			r := httptest.NewRequest(http.MethodPost, Prefix+"/users/current/heartbeats", strings.NewReader(c.body))
			r.Header.Set("Authorization", "Bearer "+keys.Pairtime)
			if c.userAgent != "" {
				r.Header.Set("User-Agent", c.userAgent)
			}
			if w := serve(e, r); w.Code != http.StatusAccepted {
				t.Fatalf("status %d: %s", w.Code, w.Body)
			}
			if len(rec.batches) != 1 || len(rec.batches[0].Heartbeats) != 1 {
				t.Fatalf("batches %+v, want one heartbeat", rec.batches)
			}
			b := rec.batches[0]
			if b.Peer.Pass() != c.wantPass {
				t.Errorf("Peer %+v: Pass() = %v, want %v", b.Peer, b.Peer.Pass(), c.wantPass)
			}
			if !c.wantPass && b.Peer.Reason != peer.NoOwner {
				t.Errorf("Peer.Reason = %s, want %s", b.Peer.Reason, peer.NoOwner)
			}
			if got := b.Heartbeats[0].UserAgent; got != c.wantUA {
				t.Errorf("user agent %q, want %q", got, c.wantUA)
			}
			if !b.Received.Equal(capturedNow) {
				t.Errorf("Received %v, want %v", b.Received, capturedNow)
			}
		})
	}
}

func TestToday(t *testing.T) {
	keys := loadKeys(t)
	cases := []struct {
		name  string
		today func(time.Time) Today
		want  string
	}{
		{"statusbar/today answers Today's seconds and text", func(time.Time) Today { return Today{Seconds: 3900, Text: "you 1h 5m"} },
			`{"data":{"grand_total":{"text":"you 1h 5m","total_seconds":3900}}}`},
		{"with no Today it answers 0 seconds and 0m", nil, `{"data":{"grand_total":{"text":"0m","total_seconds":0}}}`},
		{"an empty text reads 0m and negative seconds 0", func(time.Time) Today { return Today{Seconds: -5} },
			`{"data":{"grand_total":{"text":"0m","total_seconds":0}}}`},
	}
	schema := loadSchema(t, "statusbar_today")
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := newEndpoint(t, nil, Options{Key: keys.Pairtime, Today: c.today})
			r := httptest.NewRequest(http.MethodGet, Prefix+"/users/current/statusbar/today", nil)
			r.Header.Set("Authorization", "Bearer "+keys.Pairtime)
			w := serve(e, r)
			if w.Code != http.StatusOK || w.Body.String() != c.want {
				t.Fatalf("got %d %s, want 200 %s", w.Code, w.Body, c.want)
			}
			mustValidate(t, schema, "the answer", w.Body.Bytes())
		})
	}
}

func TestRoutes(t *testing.T) {
	keys := loadKeys(t)
	cases := []struct {
		name, method, path string
		want               int
	}{
		{"a GET of the heartbeat route isn't allowed", http.MethodGet, Prefix + "/users/current/heartbeats.bulk", http.StatusMethodNotAllowed},
		{"a POST to the status bar isn't allowed", http.MethodPost, Prefix + "/users/current/statusbar/today", http.StatusMethodNotAllowed},
		{"Hackatime's own path isn't the endpoint's", http.MethodPost, "/api/hackatime/v1/users/current/heartbeats.bulk", http.StatusNotFound},
		{"another WakaTime route is unknown", http.MethodGet, Prefix + "/users/current/summaries", http.StatusNotFound},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := newEndpoint(t, &recorder{}, Options{Key: keys.Pairtime})
			r := httptest.NewRequest(c.method, c.path, strings.NewReader("[]"))
			r.Header.Set("Authorization", "Bearer "+keys.Pairtime)
			if w := serve(e, r); w.Code != c.want {
				t.Errorf("status %d, want %d", w.Code, c.want)
			}
		})
	}
}

func TestNew(t *testing.T) {
	cases := []struct {
		name string
		key  string
		ok   bool
	}{
		{"an endpoint needs a key", "", false},
		{"an endpoint with a key is made", "00000000-0000-4000-8000-00000000a9e7", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := New(Options{Key: c.key})
			if (err == nil) != c.ok {
				t.Errorf("New: %v, want ok %v", err, c.ok)
			}
		})
	}
}

func TestCounts(t *testing.T) {
	keys := loadKeys(t)
	now := float64(capturedNow.Unix())
	cases := []struct {
		name   string
		bodies []string
		want   Counts
	}{
		{
			name: "the counts tell taken, duplicate, stale and refused heartbeats apart",
			bodies: []string{
				"[" + heartbeatJSON("a.go", now) + "," + heartbeatJSON("a.go", now) + `,{"entity":"b.go"}]`,
				"[" + heartbeatJSON("a.go", now) + "," + heartbeatJSON("old.go", float64(capturedNow.Add(-Horizon-time.Hour).Unix())) + "]",
			},
			want: Counts{Taken: 1, Duplicate: 2, Stale: 1, Refused: 1},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := newEndpoint(t, &recorder{}, Options{Key: keys.Pairtime})
			for _, b := range c.bodies {
				if w := serve(e, bulkRequest(keys.Pairtime, b)); w.Code != http.StatusCreated {
					t.Fatalf("status %d: %s", w.Code, w.Body)
				}
			}
			if got := e.Counts(); got != c.want {
				t.Errorf("Counts = %+v, want %+v", got, c.want)
			}
		})
	}
}
