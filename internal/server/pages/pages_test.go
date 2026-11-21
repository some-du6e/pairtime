// SPDX-License-Identifier: AGPL-3.0-or-later

package pages

import (
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"html"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/internal/server/retention"
	"github.com/some-du6e/pairtime/pkg/params"
)

// newSite builds a Site from o, with the embedded retention schedule and a
// base URL unless o sets them.
func newSite(t *testing.T, o Options) *Site {
	t.Helper()
	if o.Retention == nil {
		p, err := retention.Default()
		if err != nil {
			t.Fatal(err)
		}
		o.Retention = p
	}
	if o.BaseURL == "" {
		o.BaseURL = "https://pairtime.example.org"
	}
	if o.Version == "" {
		o.Version = "v0.0.0-test"
	}
	s, err := New(o)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return s
}

var (
	rowRe  = regexp.MustCompile(`(?s)<tr>(.*?)</tr>`)
	cellRe = regexp.MustCompile(`(?s)<td>(.*?)</td>`)
	tagRe  = regexp.MustCompile(`<[^>]*>`)
)

// tableRows returns the text of each body row of each table in page.
func tableRows(page string) [][]string {
	var rows [][]string
	for _, m := range rowRe.FindAllStringSubmatch(page, -1) {
		cells := cellRe.FindAllStringSubmatch(m[1], -1)
		if len(cells) == 0 {
			continue // the header row
		}
		var r []string
		for _, c := range cells {
			r = append(r, html.UnescapeString(tagRe.ReplaceAllString(c[1], "")))
		}
		rows = append(rows, r)
	}
	return rows
}

// TestRetentionTable checks that the notice's retention tables are the
// schedule the retention jobs run, by editing the schedule and seeing both
// follow.
func TestRetentionTable(t *testing.T) {
	tests := []struct {
		name string
		edit func(p *retention.Policy)
	}{
		{name: "the notice's tables are docs/retention.json's schedule, in its order", edit: func(*retention.Policy) {}},
		{name: "a changed period shows on the page and in the job alike", edit: func(p *retention.Policy) {
			for i := range p.Entries {
				if p.Entries[i].ID == "server.ip_ja4" {
					p.Entries[i].Days = 45
				}
			}
		}},
		{name: "a changed spot-check window shows on the page and in the hold alike", edit: func(p *retention.Policy) { p.SpotCheckWindow.Months = 18 }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, err := retention.Default()
			if err != nil {
				t.Fatal(err)
			}
			tt.edit(p)
			page := string(newSite(t, Options{Retention: p}).privacy)
			got := tableRows(page)
			var want [][]string
			for _, where := range retention.Places {
				for _, e := range p.In(where) {
					want = append(want, []string{e.Data, p.Kept(e)})
				}
			}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("the notice's retention rows are\n%q\nwant\n%q", got, want)
			}
			kept := make(map[string]string)
			for _, r := range got {
				if len(r) == 2 {
					kept[r[0]] = r[1]
				}
			}
			for _, j := range p.AllJobs() {
				e, _ := p.Find(j.Entry)
				text, ok := kept[e.Data]
				if !ok {
					t.Errorf("%s: the notice has no row for %q", j.ID, e.Data)
					continue
				}
				if j.WindowMonths > 0 {
					if want := fmt.Sprintf("%d months after the decision", j.WindowMonths); !strings.Contains(text, want) {
						t.Errorf("%s deletes %d months after the decision, but the notice says %q", j.ID, j.WindowMonths, text)
					}
				} else if want := retention.Days(int(j.MaxAge / (24 * time.Hour))); !strings.Contains(text, want) {
					t.Errorf("%s deletes after %v, but the notice says %q", j.ID, j.MaxAge, text)
				}
				if j.Hold != nil {
					if want := fmt.Sprintf("%d months after the decision", j.Hold.WindowMonths); !strings.Contains(text, want) {
						t.Errorf("%s holds rows for %d months, but the notice says %q", j.ID, j.Hold.WindowMonths, text)
					}
				}
			}
			if want := fmt.Sprintf("keeps it open for %d months after the decision", p.SpotCheckWindow.Months); !strings.Contains(page, want) {
				t.Errorf("the notice doesn't say %q", want)
			}
		})
	}
}

func TestPrivacyNotice(t *testing.T) {
	tests := []struct {
		name    string
		opts    Options
		want    []string // substrings of the page
		notWant []string
	}{
		{
			name: "without a controller the notice says it isn't final and marks the placeholders",
			want: []string{"This notice isn&#39;t final.", "takes no sign-ups except test accounts for the people who run it", "The controller here is <strong>[not named yet]</strong>.", "contact <strong>[not named yet]</strong>."},
		},
		{
			name:    "a named controller and contact replace their placeholders and the warning",
			opts:    Options{Controller: Controller{Name: "Hack Club", Contact: "privacy@example.org"}},
			want:    []string{"The controller here is Hack Club.", `contact <a href="mailto:privacy@example.org">privacy@example.org</a>.`},
			notWant: []string{"isn&#39;t final", "The controller here is <strong>", "contact <strong>[not named yet]"},
		},
		{
			name: "with a named controller the representative and the transfers' safeguards stay marked, since no setting fills them yet (DESIGN 13.2 item 6)",
			opts: Options{Controller: Controller{Name: "Hack Club", Contact: "privacy@example.org"}},
			want: []string{
				"may require it to name a representative in each, whom you could also contact. The representative is <strong>[not named yet]</strong>.",
				"The safeguards the controller relies on are <strong>[not named yet]</strong>.",
			},
		},
		{
			name:    "a name without a contact keeps the notice marked not final",
			opts:    Options{Controller: Controller{Name: "Hack Club"}},
			want:    []string{"This notice isn&#39;t final.", "The controller here is Hack Club."},
			notWant: []string{"The controller here is <strong>"},
		},
		{
			name: "an https contact becomes a link",
			opts: Options{Controller: Controller{Name: "Karim", Contact: "https://example.org/privacy"}},
			want: []string{`contact <a href="https://example.org/privacy">https://example.org/privacy</a>.`},
		},
		{
			name: "the Data Protection Officer appears when set",
			opts: Options{Controller: Controller{Name: "Hack Club", Contact: "privacy@example.org", DPO: "dpo@example.org"}},
			want: []string{`Data Protection Officer at <a href="mailto:dpo@example.org">dpo@example.org</a>.`},
		},
		{
			name:    "without a Data Protection Officer the notice names none",
			notWant: []string{"Data Protection Officer"},
		},
		{
			name: "markup in a controller's name is shown as text",
			opts: Options{Controller: Controller{Name: "<b>Evil</b> *Co* [x](https://e.org)", Contact: "a@example.org"}},
			want: []string{"The controller here is &lt;b&gt;Evil&lt;/b&gt; *Co* [x](https://e.org)."},
		},
		{
			name:    "in ask mode the private choice is the prominent button",
			opts:    Options{ProfileDefault: "ask"},
			want:    []string{"&#34;Private profile&#34; is the more prominent button"},
			notWant: []string{"public by default"},
		},
		{
			name:    "in public mode profiles are public by default, with equal buttons",
			opts:    Options{ProfileDefault: "public"},
			want:    []string{"Profiles on this server are public by default", "as equal buttons"},
			notWant: []string{"more prominent button. Either"},
		},
		{
			name:    "the privacy hub is promised only once its aud is set",
			notWant: []string{"privacy hub"},
		},
		{
			name: "with the privacy hub set the notice says Hack Club can reach this server",
			opts: Options{PrivacyHub: true},
			want: []string{"through the same privacy hub it uses for Hackatime"},
		},
		{
			name:    "the notice says this server can read what you reveal, and where the sealing stops (DESIGN 7.6)",
			want:    []string{"So whoever runs this server could read what you reveal.", "doesn&#39;t protect against someone at Cloudflare who actively replaces the keys"},
			notWant: []string{"end to end", "anything else in between can&#39;t read it"},
		},
		{
			name: "the notice says the vault's encryption doesn't stop malware running as you (DESIGN 3.5)",
			want: []string{"It doesn&#39;t protect them from malware running as you."},
		},
		{
			name:    "the notice limits never receiving prompts to outside a submission and backups",
			want:    []string{"Apart from a submission, and the encrypted backups if you turn them on, this server never receives your prompts"},
			notWant: []string{"<p>This server never receives"},
		},
		{
			name: "the notice says what a connected program can read, until you unlink it (DESIGN 3.10, 5.10)",
			want: []string{
				"From then until you unlink the program, it can read, for the projects you linked: your credited time, with the start and end time of each stretch",
				"your email address, if you allow it.",
			},
			notWant: []string{"get your submissions and their outcomes."},
		},
		{
			name: "the notice says each 15-second slot records the kind of work and why it was credited (DESIGN 4.3)",
			want: []string{
				"what kind of work it was, and why the rest weren&#39;t.",
				"what kind of work they counted as (prompting, reading the agent&#39;s output,",
				"how long an AI agent was running",
				"<code>pairtime privacy show</code> prints every field of the next upload",
			},
		},
		{
			name: "the notice says what the optional GitHub App keeps of a private repository's pushes (DESIGN 3.10)",
			want: []string{
				"For a private one it drops the lists of files each commit changed and keeps the rest: the repository&#39;s name, the branch, each commit&#39;s ID, message and times, and the names and email addresses",
				"branch names or commit messages of private repositories (unless you install pairtime&#39;s GitHub App on one)",
			},
		},
		{
			name:    "the notice says presence samples record the time since the last key press, and the typing module counts per 15 seconds (DESIGN 7.2)",
			want:    []string{"how long ago you last pressed a key", "how many keys you pressed in each 15 seconds", "These numbers go to reviewers with your other records when you submit hours."},
			notWant: []string{"or when you press them", "per-minute counts"},
		},
		{
			name: "the short version names the exception for submissions and what identifies you (UK Children's Code Std 4)",
			want: []string{
				"stay on your computer, unless you submit hours: then a few prompts, and the agent replies they answered, go to the program&#39;s reviewers.",
				"This server gets your Hack Club identity and email address, the IP address each upload comes from, and numbers",
			},
		},
		{
			name: "each purpose has its legal basis, and fraud protection names its legitimate interest (GDPR Art. 13(1)(c) and (d))",
			want: []string{
				"The legal basis is contract, because pairtime needs the data to give you the service you signed up for.",
				"The legal basis is legitimate interests. Hack Club and its programs need to know that the hours they reward were really spent building",
				"The legal basis is your consent.",
			},
		},
		{
			name: "the right to object stands apart, under a heading of its own (GDPR Art. 21(4))",
			want: []string{
				`<h2 id="your-right-to-object">Your right to object</h2>`,
				"Fraud protection rests on legitimate interests, not on your consent, so you have the right to object to it.",
				`as <a href="#your-right-to-object">Your right to object</a> explains`,
			},
		},
		{
			name: "the notice lists every right and the regulators to complain to (GDPR Art. 13(2)(b) to (d))",
			want: []string{
				"get a copy of your data, in a file you can take to another service",
				"have the controller stop using it, apart from keeping it, while a question about it is sorted out",
				"withdraw any consent you gave, which stops that use from then on",
				"In the UK that&#39;s the Information Commission, and in the EU it&#39;s the data protection authority of the country you live in.",
			},
		},
		{
			name: "the notice says Cloudflare runs bot detection on requests to this server, and that pairtime's pages block the script it adds for that",
			want: []string{"It also runs automated bot detection on requests to this server, and pairtime&#39;s pages block the script it adds to them for that."},
		},
		{
			name: "the notice says where data goes outside the UK and the EU (GDPR Art. 13(1)(f))",
			want: []string{"So some of your data is handled outside the UK and the EU, in the United States and elsewhere."},
		},
		{
			name:    "the consent cards' promise is the one the cards make, that saying no doesn't stop you using pairtime (#72)",
			want:    []string{"Saying no doesn&#39;t stop you from using pairtime, and pairtime won&#39;t ask again for a year."},
			notWant: []string{"takes no feature away"},
		},
		{
			name: "removing something published says how, and warns that copies may remain (California Bus. and Prof. Code 22581)",
			want: []string{
				"To remove one thing, use the pairtime dashboard on your computer: delete the devlog, remove the badge, or set the project back to private.",
				"You can also ask the controller to remove anything you published.",
				"That doesn&#39;t ensure it&#39;s gone completely, though.",
			},
			notWant: []string{"disappears at once"},
		},
		{
			name: "the IP period in the prose is the schedule's",
			want: []string{"the server deletes them after 30 days."},
		},
		{
			name: "the prose gives the backups' period wherever it promises a deletion (DESIGN 10.3)",
			want: []string{
				"Copies stay in the server&#39;s backups until those expire, at most 30 days later",
				"Copies in the server&#39;s backups go as the backups expire, within 30 days.",
				"Deleted data is gone from the server&#39;s backups too within 30 days.",
			},
		},
		{
			name: "the notice says the continuous backup isn't encrypted with the operator's key (DESIGN 10.3)",
			want: []string{"The continuous backup isn&#39;t encrypted with a key of the operator&#39;s own, so the storage provider could read it."},
		},
		{
			name: "a removed device keeps its link count with its ID and upload progress, and its build and the other labels its uploads carry go 90 days after it last sent them (D-device-account-binding)",
			want: []string{
				"Then its device ID, when you removed it, how many times it has been linked and how far its uploads have got stay 90 days more",
				"Everything else about the device goes at once, except its pairtime build and the other labels its uploads carry, which stay until 90 days after the device last sent them.",
			},
			notWant: []string{"removal time and upload progress stay", "and the rest goes at once"},
		},
		{
			name: "the notice names this server and its version",
			opts: Options{BaseURL: "https://pairtime.example.net", Version: "v1.2.3"},
			want: []string{"the pairtime server at https://pairtime.example.net", "built by pairtime v1.2.3", "pairtime v1.2.3 is free software"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			page := string(newSite(t, tt.opts).privacy)
			for _, w := range tt.want {
				if !strings.Contains(page, w) {
					t.Errorf("the notice doesn't contain %q", w)
				}
			}
			for _, w := range tt.notWant {
				if strings.Contains(page, w) {
					t.Errorf("the notice contains %q", w)
				}
			}
		})
	}
}

func TestNewRefuses(t *testing.T) {
	tests := []struct {
		name    string
		opts    Options
		wantErr string
	}{
		{name: "a contact that is neither an email address nor an https URL", opts: Options{Controller: Controller{Name: "X", Contact: "call me"}}, wantErr: "must be an email address or an https URL"},
		{name: "a javascript: contact", opts: Options{Controller: Controller{Name: "X", Contact: "javascript:alert(1)"}}, wantErr: "must be an email address or an https URL"},
		{name: "an http contact", opts: Options{Controller: Controller{Name: "X", Contact: "http://example.org"}}, wantErr: "must be an email address or an https URL"},
		{name: "a scoring MAJOR without its own evidence definition", opts: Options{ScoringVersion: "1.0.0"}, wantErr: "no evidence definition covers scoring 1.0.0"},
		{name: "a scoring version that isn't X.Y.Z", opts: Options{ScoringVersion: "1.0"}, wantErr: "isn't of the form X.Y.Z"},
		{name: "a commit that is a short hash, which a link can't be pinned to", opts: Options{Commit: "1a2b3c4"}, wantErr: "isn't a full git commit hash"},
		{name: "a commit carrying Markdown", opts: Options{Commit: strings.Repeat("a", 39) + ")"}, wantErr: "isn't a full git commit hash"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, err := retention.Default()
			if err != nil {
				t.Fatal(err)
			}
			tt.opts.Retention = p
			_, err = New(tt.opts)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("New error = %v, want one containing %q", err, tt.wantErr)
			}
		})
	}
}

func TestRoutes(t *testing.T) {
	mux := http.NewServeMux()
	newSite(t, Options{ScoringVersion: "0.4.2"}).Register(mux)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	tests := []struct {
		name     string
		method   string
		path     string
		wantCode int
		wantBody string
	}{
		{name: "/privacy serves the notice", path: "/privacy", wantCode: 200, wantBody: "<h1 id=\"pairtime-privacy-notice\">pairtime privacy notice</h1>"},
		{name: "HEAD /privacy answers too", method: http.MethodHead, path: "/privacy", wantCode: 200},
		{name: "POST /privacy is refused", method: http.MethodPost, path: "/privacy", wantCode: 405},
		{name: "/docs/evidence serves the definition for the scoring version this server runs", path: "/docs/evidence", wantCode: 200, wantBody: "This server runs scoring 0.4.2."},
		{name: "/docs/evidence says which versions its definition covers", path: "/docs/evidence", wantCode: 200, wantBody: "for scoring 0.1.0 and the later 0.x versions."},
		{name: "/docs/evidence/<version> serves the definition in force at that version", path: "/docs/evidence/0.1.0", wantCode: 200, wantBody: "Evidence levels and bands"},
		{name: "a version before the first definition has none", path: "/docs/evidence/0.0.9", wantCode: 404},
		{name: "a MAJOR without a definition of its own has none", path: "/docs/evidence/1.0.0", wantCode: 404},
		{name: "a malformed version is not found", path: "/docs/evidence/latest", wantCode: 404},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			method := tt.method
			if method == "" {
				method = http.MethodGet
			}
			req, err := http.NewRequest(method, srv.URL+tt.path, nil)
			if err != nil {
				t.Fatal(err)
			}
			resp, err := srv.Client().Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			body, err := io.ReadAll(resp.Body)
			if err != nil {
				t.Fatal(err)
			}
			if resp.StatusCode != tt.wantCode {
				t.Fatalf("%s %s = %d, want %d", method, tt.path, resp.StatusCode, tt.wantCode)
			}
			if tt.wantBody != "" && !strings.Contains(string(body), tt.wantBody) {
				t.Errorf("%s %s doesn't contain %q", method, tt.path, tt.wantBody)
			}
			if tt.wantCode == 200 {
				if ct := resp.Header.Get("Content-Type"); ct != "text/html; charset=utf-8" {
					t.Errorf("Content-Type = %q", ct)
				}
				if csp := resp.Header.Get("Content-Security-Policy"); !strings.HasPrefix(csp, "default-src 'none'; style-src 'sha256-") {
					t.Errorf("Content-Security-Policy = %q", csp)
				}
			}
		})
	}
}

// TestStyleHash checks that the CSP allows exactly the inline stylesheet
// each page carries, so browsers don't drop it.
func TestStyleHash(t *testing.T) {
	s := newSite(t, Options{})
	_, home := getHome(t, Options{}, signInOn("U0FAKEVERIFIED"))
	_, demo := getDemo(t, Options{}, signInOn(""))
	pages := []struct {
		name string
		page []byte
	}{
		{name: "the privacy notice's style matches its CSP hash", page: s.privacy},
		{name: "the evidence page's style matches its CSP hash", page: s.current},
		{name: "the home page's style matches its CSP hash, with a form or without", page: []byte(home)},
		{name: "the sample dashboard's style matches its CSP hash", page: []byte(demo)},
	}
	for _, p := range pages {
		t.Run(p.name, func(t *testing.T) {
			_, rest, ok := strings.Cut(string(p.page), "<style>")
			css, _, ok2 := strings.Cut(rest, "</style>")
			if !ok || !ok2 {
				t.Fatal("the page has no <style> element")
			}
			sum := sha256.Sum256([]byte(css))
			want := "style-src 'sha256-" + base64.StdEncoding.EncodeToString(sum[:]) + "'"
			for _, csp := range []string{s.csp, s.cspForm} {
				if !strings.Contains(csp, want) {
					t.Errorf("CSP %q doesn't allow the page's style; want %s", csp, want)
				}
			}
		})
	}
}

// TestSourceLinks checks that a page that says it is the same file this
// server reads, or a section of the design word for word, links that file
// at the commit this server was built from, and links no file at all when
// the commit isn't known, since the file's latest version may differ.
func TestSourceLinks(t *testing.T) {
	commit := "1a2b3c4d5e6f7a8b9c0d1e2f3a4b5c6d7e8f9a0b"
	blob := "https://github.com/some-du6e/pairtime/blob/" + commit + "/"
	tree := "https://github.com/some-du6e/pairtime/tree/" + commit
	privacy := func(s *Site) []byte { return s.privacy }
	evidence := func(s *Site) []byte { return s.current }
	tests := []struct {
		name    string
		commit  string
		page    func(*Site) []byte
		want    []string
		notWant []string
	}{
		{
			name:   "the notice links docs/retention.json at the commit this server was built from",
			commit: commit,
			page:   privacy,
			want:   []string{`read, <a href="` + blob + `docs/retention.json"><code>docs/retention.json</code></a>, as it was when this server was built.`},
		},
		{
			name:   "the notice and the footer link the code at that commit",
			commit: commit,
			page:   privacy,
			want:   []string{`<a href="` + tree + `">the code this server runs</a>`, `<a href="` + tree + `">Read the code this server runs</a>`},
		},
		{
			name:   "the evidence page links the design it copies word for word at that commit",
			commit: commit,
			page:   evidence,
			want:   []string{`section 6.4 of <a href="` + blob + `docs/DESIGN.md">pairtime&#39;s design</a>, word for word.`},
		},
		{
			name:    "without a commit the notice names docs/retention.json and links no version of it",
			page:    privacy,
			want:    []string{"read, <code>docs/retention.json</code>, as it was when this server was built."},
			notWant: []string{"/blob/", "/tree/"},
		},
		{
			name:    "without a commit the evidence page links no version of the design",
			page:    evidence,
			want:    []string{"section 6.4 of pairtime&#39;s design, word for word."},
			notWant: []string{"/blob/", "/tree/"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			page := string(tt.page(newSite(t, Options{Commit: tt.commit})))
			for _, w := range tt.want {
				if !strings.Contains(page, w) {
					t.Errorf("the page doesn't contain %q", w)
				}
			}
			for _, w := range tt.notWant {
				if strings.Contains(page, w) {
					t.Errorf("the page contains %q", w)
				}
			}
		})
	}
}

// TestEvidenceIsDesign64 checks the evidence definition for the built-in
// scoring version against the oracle: every line of DESIGN 6.4, after its
// first paragraph, which says where the page is served, must appear in the
// definition word for word.
func TestEvidenceIsDesign64(t *testing.T) {
	data, err := os.ReadFile("../../../docs/DESIGN.md")
	if err != nil {
		t.Fatal(err)
	}
	_, sec, ok := strings.Cut(string(data), "\n### 6.4 Evidence levels and bands\n")
	if !ok {
		t.Fatal("docs/DESIGN.md has no section 6.4")
	}
	sec, _, _ = strings.Cut(sec, "\n### ")
	var lines []string
	for _, l := range strings.Split(sec, "\n") {
		if strings.TrimSpace(l) != "" {
			lines = append(lines, l)
		}
	}
	if len(lines) < 10 || !strings.HasPrefix(lines[0], "This is the one published definition") {
		t.Fatalf("DESIGN 6.4 doesn't start as expected: %q", lines[:min(1, len(lines))])
	}

	// The definition in force at the built-in version is the newest file of
	// its MAJOR that starts at or before it.
	builtin, _ := parseVersion(BuiltinScoringVersion)
	names, err := fs.Glob(files, "evidence/*.md")
	if err != nil {
		t.Fatal(err)
	}
	var file string
	var from version
	for _, n := range names {
		v, ok := parseVersion(strings.TrimSuffix(path.Base(n), ".md"))
		if ok && v[0] == builtin[0] && v.compare(builtin) <= 0 && (file == "" || v.compare(from) > 0) {
			file, from = n, v
		}
	}
	if file == "" {
		t.Fatalf("no evidence definition covers scoring %s", BuiltinScoringVersion)
	}
	src, err := fs.ReadFile(files, file)
	if err != nil {
		t.Fatal(err)
	}
	for i, l := range lines[1:] {
		t.Run(fmt.Sprintf("DESIGN 6.4 line %d appears word for word", i+2), func(t *testing.T) {
			if !strings.Contains(string(src), l) {
				t.Errorf("%s lacks DESIGN 6.4's line\n%s", file, l)
			}
		})
	}
}

// TestEvidenceValues checks every value an evidence definition states, as
// `NAME` = N s, against the parameter table of each scoring version it
// covers. A MINOR version is new parameter values only (DESIGN 6.6), so
// one that changes a stated value must add a definition file of its own.
func TestEvidenceValues(t *testing.T) {
	stated := regexp.MustCompile(`<code>([A-Z][A-Z0-9_]*)</code> = ([0-9][0-9,]*) s\b`)
	for _, table := range params.Tables() {
		t.Run("the definition in force at scoring "+table.Version()+" states that table's values", func(t *testing.T) {
			page := string(newSite(t, Options{ScoringVersion: table.Version()}).current)
			matches := stated.FindAllStringSubmatch(page, -1)
			if len(matches) == 0 {
				t.Fatal("the definition states no value as `NAME` = N s, so there is nothing to check; has the page's markup changed?")
			}
			for _, m := range matches {
				name, value := m[1], strings.ReplaceAll(m[2], ",", "")
				want, ok := table.Text(name)
				switch {
				case !ok:
					t.Errorf("the definition states %s = %s s, which table %s doesn't have", name, m[2], table.Version())
				case value != want:
					t.Errorf("the definition states %s = %s s, but table %s has %s; add internal/server/pages/evidence/%s.md", name, m[2], table.Version(), want, table.Version())
				}
			}
		})
	}
}

// TestMayCreate checks the sign-up gate's rule. Whether pairtimed hands it
// to the real HCA callback is TestServeSignups' job, in cmd/pairtimed.
func TestMayCreate(t *testing.T) {
	tests := []struct {
		name      string
		open      bool
		operators []string
		sub       string
		want      bool
	}{
		{name: "while sign-ups are closed a new identity may not get an account", sub: "ident!new"},
		{name: "while sign-ups are closed an identity on the operators list may", operators: []string{"ident!op"}, sub: "ident!op", want: true},
		{name: "the operators list admits only the subs on it", operators: []string{"ident!op"}, sub: "ident!other"},
		{name: "with sign-ups open any new identity may", open: true, sub: "ident!new", want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gate := newSite(t, Options{SignupsOpen: tt.open, Operators: tt.operators}).Signups
			if got := gate.MayCreate(tt.sub); got != tt.want {
				t.Errorf("MayCreate(%q) = %v, want %v", tt.sub, got, tt.want)
			}
		})
	}
}
