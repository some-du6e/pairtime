// SPDX-License-Identifier: AGPL-3.0-or-later

// Package pages serves pairtimed's pages (WP-SV14): the home page at /,
// the privacy notice at /privacy (DESIGN 4.4, 8.6), the definition of
// evidence levels and bands at /docs/evidence (DESIGN 6.4), and the
// dashboard page, which /demo draws from a fixed sample (dashboard.go). It
// also holds the sign-up gate that Hack Club Auth sign-in asks before it
// creates an account (DESIGN 8.6, 10.2).
//
// Each page is a Markdown template in this directory: home.md, privacy.md
// and evidence/<version>.md. New renders all of them once, from the
// configuration and the retention schedule, so serving a static page only
// copies bytes, and a mistake in a template stops pairtimed at start
// instead of reaching a reader. The home page's header shows the visitor's
// sign-in, so that one page goes through the layout per request (home.go).
//
// The privacy notice's retention tables come from the same
// retention.Policy the retention jobs run. The controller's name and
// contact come from pairtime.toml's [controller] table, and until both are
// set the notice marks them "not named yet" and says it isn't final.
//
// Evidence definitions are versioned with the scoring version (DESIGN
// 6.6). Each file is named for the first scoring version it applies to,
// and applies until the next file of the same MAJOR. A scoring version that
// changes a rule or a value the definition states adds a file, and a new
// MAJOR needs a file of its own before a server can run it. For values,
// TestEvidenceValues checks this against every table in pkg/params.
// /docs/evidence shows the definition for the scoring version this server
// runs, and /docs/evidence/<version> the one for any other version.
//
// The pages link the code this server runs, and the docs/ files they
// quote, at the commit it was built from (Options.Commit), because a page
// that says it is the same file or word for word must link the version it
// matches, not one that has moved on since. When the commit isn't known
// they link no such file.
package pages

import (
	"bytes"
	"cmp"
	"crypto/sha256"
	"embed"
	"encoding/base64"
	"errors"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"net/http"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"
	texttemplate "text/template"

	"github.com/some-du6e/pairtime/internal/server/retention"
)

//go:embed layout.html style.css home.md privacy.md evidence/*.md dashboard.html
var files embed.FS

// SourceURL is where the code this server runs is published, which AGPL-3.0
// section 13 asks a network service to offer its users.
const SourceURL = "https://github.com/some-du6e/pairtime"

// BuiltinScoringVersion is the scoring version /docs/evidence describes
// when pairtime.toml leaves scoring_version empty: the one pkg/score/VERSION
// carries, which TestBuiltinScoringVersion holds it to, so a PR that bumps
// the scorer's version changes it too. Once pkg/score is part of the
// server, its version replaces this constant.
const BuiltinScoringVersion = "0.5.0"

// Controller says who is responsible for the data on this server
// (DESIGN 8.6). Fields left empty show as placeholders.
type Controller struct {
	// Name is the person or organization.
	Name string
	// Contact is an email address or an https URL.
	Contact string
	// DPO is how to reach Hack Club's Data Protection Officer, when Hack
	// Club is the controller: an email address or an https URL.
	DPO string
}

// Options are what the pages say. pairtimed fills them from pairtime.toml.
type Options struct {
	// BaseURL is the server's origin, such as "https://<host>".
	BaseURL    string
	Controller Controller
	// ProfileDefault is pairtime.toml's profile_default: "public", or
	// "ask" (DESIGN 8.3). Empty means "public", the code default.
	ProfileDefault string
	// PrivacyHub says whether privacy_hub.aud is set, so Hack Club's
	// privacy hub can reach this server (DESIGN 8.9).
	PrivacyHub bool
	// SignupsOpen and Operators configure the sign-up gate.
	SignupsOpen bool
	Operators   []string
	// ScoringVersion is the scoring version this server runs, or "" for
	// BuiltinScoringVersion.
	ScoringVersion string
	// Version is pairtimed's version, for the footer.
	Version string
	// Commit is the full hash of the git commit this server was built
	// from, or "" when that isn't known or the build had local changes.
	Commit string
	// Retention is the retention schedule the notice publishes.
	Retention *retention.Policy
	// Running says which of the services the notice describes this server
	// runs.
	Running Running
}

// Running says which of the services the privacy notice describes this
// server runs. The notice describes each as it works once it runs, so its
// first lines name the ones that don't yet, and the work package that
// builds each one sets it in pairtimed (#119).
type Running struct {
	// SignIn is Hack Club Auth sign-in, which runs once hca.client_id is
	// set (WP-SV2).
	SignIn bool
	// Uploads are the pairtime program's uploads: minute buckets, batch
	// roots and commit records (WP-SV4).
	Uploads bool
	// Submissions are hours submitted to programs, and the evidence store
	// that keeps what they reveal (WP-SB1, WP-SB4).
	Submissions bool
	// DayRoots are the day roots published to the public witness
	// repository (WP-SV17).
	DayRoots bool
}

// notRunning names, as one clause the notice completes, the services this
// server doesn't run yet, or "" when it runs them all.
func (r Running) notRunning() string {
	var parts []string
	if !r.SignIn {
		parts = append(parts, "let anyone sign in")
	}
	if !r.Uploads {
		parts = append(parts, "receive uploads from the pairtime program")
	}
	if !r.Submissions {
		parts = append(parts, "take hours you submit to programs", "keep an evidence store")
	}
	if !r.DayRoots {
		parts = append(parts, "publish day roots")
	}
	switch len(parts) {
	case 0:
		return ""
	case 1:
		return parts[0]
	case 2:
		return parts[0] + " or " + parts[1]
	}
	return strings.Join(parts[:len(parts)-1], ", ") + ", or " + parts[len(parts)-1]
}

// commitHash is a full git commit hash, SHA-1 or SHA-256.
var commitHash = regexp.MustCompile(`^([0-9a-f]{40}|[0-9a-f]{64})$`)

// sources links the code this server runs, and the docs/ files the pages
// quote, at the commit it was built from.
type sources struct{ commit string }

// code is where to read the code this server runs: its commit's tree, or
// the repository when the commit isn't known.
func (s sources) code() string {
	if s.commit == "" {
		return SourceURL
	}
	return SourceURL + "/tree/" + s.commit
}

// file returns text, which is Markdown, as a link to the repository's
// file name at the commit. When the commit isn't known it returns text
// alone, since the file's latest version may say something this server's
// copy doesn't.
func (s sources) file(text, name string) string {
	if s.commit == "" {
		return text
	}
	return "[" + text + "](" + SourceURL + "/blob/" + s.commit + "/" + name + ")"
}

// Site holds the rendered pages.
type Site struct {
	privacy []byte
	// current is the evidence definition for the scoring version this
	// server runs.
	current  []byte
	evidence []evidencePage // oldest first
	// homeSignIn and homeNoSignIn are home.md rendered, for a server with
	// sign-in and one without.
	homeSignIn, homeNoSignIn template.HTML
	layout                   *layout
	// csp allows no form, and cspForm lets a form post to this server.
	csp, cspForm string
	// Signups is the sign-up gate. pairtimed passes its MayCreate to HCA
	// sign-in (WP-SV2), which asks it before it creates an account.
	Signups *Signups
	// dash is dashboard.html, the dashboard page's body (dashboard.go),
	// and signupsOpen what its sample's banner says about sign-ups.
	dash        *template.Template
	signupsOpen bool
}

type evidencePage struct {
	from version
	page []byte
}

// New renders every page. It fails on a mistake in a template or in its
// Markdown, on a controller contact that can't be a link, on a commit that
// isn't a full hash, and on a scoring version that no evidence definition
// covers.
func New(o Options) (*Site, error) {
	if o.Retention == nil {
		return nil, errors.New("pages: no retention schedule")
	}
	if o.Commit != "" && !commitHash.MatchString(o.Commit) {
		return nil, fmt.Errorf("pages: commit %q isn't a full git commit hash", o.Commit)
	}
	src := sources{commit: o.Commit}
	running := o.ScoringVersion
	if running == "" {
		running = BuiltinScoringVersion
	}
	rv, ok := parseVersion(running)
	if !ok {
		return nil, fmt.Errorf("pages: scoring version %q isn't of the form X.Y.Z", running)
	}
	l, err := newLayout(o.Version, src.code())
	if err != nil {
		return nil, err
	}
	data, err := newPrivacyData(o, src)
	if err != nil {
		return nil, err
	}
	funcs := texttemplate.FuncMap{
		// days says an entry's period, so the notice's prose never
		// writes a number the schedule holds.
		"days": func(id string) (string, error) {
			e, ok := o.Retention.Find(id)
			if !ok || e.Days == 0 {
				return "", fmt.Errorf("the retention schedule has no entry %s with days", id)
			}
			return retention.Days(e.Days), nil
		},
	}
	s := &Site{csp: l.csp, cspForm: l.cspForm, layout: l}
	if s.privacy, err = l.page("Privacy notice", "privacy.md", data, funcs); err != nil {
		return nil, err
	}
	if s.evidence, err = l.evidencePages(rv, src); err != nil {
		return nil, err
	}
	if s.current, ok = s.evidenceFor(rv); !ok {
		return nil, fmt.Errorf("pages: no evidence definition covers scoring %s; add internal/server/pages/evidence/%s.md (DESIGN 6.4, 6.6)", rv, rv)
	}
	if err := s.renderHome(o); err != nil {
		return nil, err
	}
	s.Signups = &Signups{open: o.SignupsOpen, operators: make(map[string]bool)}
	for _, sub := range o.Operators {
		s.Signups.operators[sub] = true
	}
	if s.dash, err = template.ParseFS(files, "dashboard.html"); err != nil {
		return nil, fmt.Errorf("pages: %w", err)
	}
	s.signupsOpen = o.SignupsOpen
	if err := s.checkDashboard(); err != nil {
		return nil, err
	}
	return s, nil
}

// Register adds the pages to mux.
func (s *Site) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /privacy", func(w http.ResponseWriter, _ *http.Request) {
		writePage(w, http.StatusOK, s.privacy, s.csp, "no-cache", "no-referrer")
	})
	mux.HandleFunc("GET /docs/evidence", func(w http.ResponseWriter, _ *http.Request) {
		writePage(w, http.StatusOK, s.current, s.csp, "no-cache", "no-referrer")
	})
	mux.HandleFunc("GET /docs/evidence/{version}", func(w http.ResponseWriter, r *http.Request) {
		if v, ok := parseVersion(r.PathValue("version")); ok {
			if page, found := s.evidenceFor(v); found {
				writePage(w, http.StatusOK, page, s.csp, "no-cache", "no-referrer")
				return
			}
		}
		http.NotFound(w, r)
	})
}

// evidenceFor returns the definition in force at scoring version v: the
// newest one of v's MAJOR that starts at or before v.
func (s *Site) evidenceFor(v version) ([]byte, bool) {
	var page []byte
	for _, p := range s.evidence {
		if p.from[0] == v[0] && p.from.compare(v) <= 0 {
			page = p.page
		}
	}
	return page, page != nil
}

// writePage writes a rendered page. The CSP allows nothing but the page's
// own stylesheet, and a form only where csp says so.
func writePage(w http.ResponseWriter, code int, page []byte, csp, cache, referrer string) {
	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("Content-Security-Policy", csp)
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Referrer-Policy", referrer)
	h.Set("Cache-Control", cache)
	w.WriteHeader(code)
	w.Write(page)
}

// layout wraps rendered Markdown in layout.html.
type layout struct {
	tmpl *template.Template
	css  string
	// csp allows no form, and cspForm lets a form post to this server.
	csp, cspForm string
	version      string
	// code is where the footer links the code this server runs.
	code string
}

type layoutData struct {
	// Title heads the browser tab, before "pairtime". The home page has
	// none, so its tab says "pairtime" alone.
	Title string
	// Description is the page's meta description, or "" for none.
	Description string
	// Class is the body's class, or "" for none.
	Class   string
	CSS     template.CSS
	Body    template.HTML
	Version string
	Source  string
	// Account is the sign-in area in the header, which only the home page
	// has.
	Account *account
}

func newLayout(version, code string) (*layout, error) {
	t, err := template.ParseFS(files, "layout.html")
	if err != nil {
		return nil, fmt.Errorf("pages: %w", err)
	}
	css, err := fs.ReadFile(files, "style.css")
	if err != nil {
		return nil, fmt.Errorf("pages: %w", err)
	}
	// The stylesheet is inline, so the CSP names it by its hash.
	sum := sha256.Sum256(css)
	policy := func(formAction string) string {
		return "default-src 'none'; style-src 'sha256-" + base64.StdEncoding.EncodeToString(sum[:]) +
			"'; base-uri 'none'; form-action " + formAction + "; frame-ancestors 'none'"
	}
	return &layout{tmpl: t, css: string(css), csp: policy("'none'"), cspForm: policy("'self'"), version: version, code: code}, nil
}

// page executes the Markdown template name with data, renders it and wraps
// it in the layout.
func (l *layout) page(title, name string, data any, funcs texttemplate.FuncMap) ([]byte, error) {
	body, err := l.body(name, data, funcs)
	if err != nil {
		return nil, err
	}
	var out bytes.Buffer
	if err := l.wrap(&out, layoutData{Title: title, Body: body}); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// body executes the Markdown template name with data and renders it.
func (l *layout) body(name string, data any, funcs texttemplate.FuncMap) (template.HTML, error) {
	src, err := fs.ReadFile(files, name)
	if err != nil {
		return "", fmt.Errorf("pages: %w", err)
	}
	t, err := texttemplate.New(path.Base(name)).Funcs(funcs).Parse(string(src))
	if err != nil {
		return "", fmt.Errorf("pages: %w", err)
	}
	var md strings.Builder
	if err := t.Execute(&md, data); err != nil {
		return "", fmt.Errorf("pages: %w", err)
	}
	body, err := render(md.String())
	if err != nil {
		return "", fmt.Errorf("pages: %s: %w", name, err)
	}
	return template.HTML(body), nil
}

// wrap writes d to w through layout.html, with the stylesheet, the version
// and the code link filled in.
func (l *layout) wrap(w io.Writer, d layoutData) error {
	d.CSS = template.CSS(l.css)
	d.Version = l.version
	d.Source = l.code
	if err := l.tmpl.Execute(w, d); err != nil {
		return fmt.Errorf("pages: %w", err)
	}
	return nil
}

// evidenceData is what an evidence definition's template reads.
type evidenceData struct {
	// Covers says which scoring versions the definition is for.
	Covers string
	// Running is the scoring version this server runs.
	Running string
	// Others links every definition, when there is more than one.
	Others string
	// From is the first scoring version the definition applies to.
	From string
	// Builtin says whether this is the definition in force at
	// BuiltinScoringVersion, the one TestEvidenceIsDesign64 checks against
	// DESIGN 6.4 at the commit this server was built from. Only that one
	// can say it is the design's 6.4 word for word and link the design.
	Builtin bool
	// Design is "pairtime's design" in Markdown, linked to DESIGN.md at
	// the commit this server was built from when that is known.
	Design string
}

// evidencePages renders every evidence definition, oldest first.
func (l *layout) evidencePages(running version, src sources) ([]evidencePage, error) {
	names, err := fs.Glob(files, "evidence/*.md")
	if err != nil {
		return nil, fmt.Errorf("pages: %w", err)
	}
	var froms []version
	for _, n := range names {
		v, ok := parseVersion(strings.TrimSuffix(path.Base(n), ".md"))
		if !ok {
			return nil, fmt.Errorf("pages: %s isn't named for the scoring version it starts at, such as 0.1.0.md", n)
		}
		froms = append(froms, v)
	}
	if len(froms) == 0 {
		return nil, errors.New("pages: there is no evidence definition")
	}
	slices.SortFunc(froms, version.compare)
	links := make([]string, len(froms))
	for i, v := range froms {
		links[i] = "[" + v.String() + "](/docs/evidence/" + v.String() + ")"
	}
	// The definition in force at the built-in version is the newest one of
	// its MAJOR that starts at or before it.
	builtin, _ := parseVersion(BuiltinScoringVersion)
	var builtinFrom version
	hasBuiltin := false
	for _, v := range froms {
		if v[0] == builtin[0] && v.compare(builtin) <= 0 {
			builtinFrom, hasBuiltin = v, true
		}
	}
	pages := make([]evidencePage, 0, len(froms))
	for i, v := range froms {
		d := evidenceData{
			Covers:  fmt.Sprintf("scoring %s and the later %d.x versions", v, v[0]),
			Running: running.String(),
			From:    v.String(),
			Builtin: hasBuiltin && v == builtinFrom,
			Design:  src.file("pairtime's design", "docs/DESIGN.md"),
		}
		if i+1 < len(froms) && froms[i+1][0] == v[0] {
			d.Covers = fmt.Sprintf("scoring %s and the later versions before %s", v, froms[i+1])
		}
		if len(froms) > 1 {
			d.Others = strings.Join(links, ", ")
		}
		page, err := l.page("Evidence levels and bands", "evidence/"+v.String()+".md", d, nil)
		if err != nil {
			return nil, err
		}
		pages = append(pages, evidencePage{from: v, page: page})
	}
	return pages, nil
}

// version is a scoring version, MAJOR.MINOR.PATCH (DESIGN 6.6).
type version [3]int

func parseVersion(s string) (version, bool) {
	parts := strings.Split(s, ".")
	if len(parts) != 3 {
		return version{}, false
	}
	var v version
	for i, p := range parts {
		if p == "" || len(p) > 6 || strings.Trim(p, "0123456789") != "" || (len(p) > 1 && p[0] == '0') {
			return version{}, false
		}
		v[i], _ = strconv.Atoi(p)
	}
	return v, true
}

func (v version) compare(w version) int {
	for i := range v {
		if c := cmp.Compare(v[i], w[i]); c != 0 {
			return c
		}
	}
	return 0
}

func (v version) String() string {
	return fmt.Sprintf("%d.%d.%d", v[0], v[1], v[2])
}

// privacyData is what privacy.md reads. Every string is Markdown, with
// values from the configuration escaped.
type privacyData struct {
	// Named says whether the controller's name and contact are both set.
	Named bool
	// NotRunning names the services this server doesn't run yet, "" when
	// it runs every one the notice describes.
	NotRunning string
	Controller string
	Contact    string
	DPO        string
	BaseURL    string
	// SourceURL is where to read the code this server runs.
	SourceURL string
	// RetentionFile names docs/retention.json, linked at the commit this
	// server was built from when that is known.
	RetentionFile string
	Version       string
	// PublicDefault is profile_default = "public".
	PublicDefault bool
	PrivacyHub    bool
	// WindowMonths is the spot-check window, and WindowProvisional says
	// whether it is the design's stand-in until HQ sets one.
	WindowMonths      int
	WindowProvisional bool
	// Sections are the retention tables, one per place.
	Sections []section
}

type section struct {
	Title string
	Rows  []row
}

type row struct {
	Data, Kept string
}

// placeTitles head the notice's retention tables.
var placeTitles = map[string]string{
	retention.Device:   "On your computer",
	retention.Server:   "On this server",
	retention.Evidence: "In the evidence store, only if you submit hours",
	retention.Public:   "In public",
}

// notNamed stands in for the controller's name and contact until
// pairtime.toml sets them (DESIGN 8.6).
const notNamed = `**\[not named yet\]**`

func newPrivacyData(o Options, src sources) (*privacyData, error) {
	c := o.Controller
	d := &privacyData{
		Named:             c.Name != "" && c.Contact != "",
		NotRunning:        o.Running.notRunning(),
		Controller:        notNamed,
		Contact:           notNamed,
		BaseURL:           escape(o.BaseURL),
		SourceURL:         src.code(),
		RetentionFile:     src.file("`docs/retention.json`", "docs/retention.json"),
		Version:           escape(o.Version),
		PublicDefault:     o.ProfileDefault != "ask",
		PrivacyHub:        o.PrivacyHub,
		WindowMonths:      o.Retention.SpotCheckWindow.Months,
		WindowProvisional: o.Retention.SpotCheckWindow.Provisional != "",
	}
	if c.Name != "" {
		d.Controller = escape(c.Name)
	}
	var err error
	if c.Contact != "" {
		if d.Contact, err = contactLink(c.Contact); err != nil {
			return nil, fmt.Errorf("pages: the controller's contact: %w", err)
		}
	}
	if c.DPO != "" {
		if d.DPO, err = contactLink(c.DPO); err != nil {
			return nil, fmt.Errorf("pages: the Data Protection Officer's contact: %w", err)
		}
	}
	for _, where := range retention.Places {
		sec := section{Title: placeTitles[where]}
		for _, e := range o.Retention.In(where) {
			sec.Rows = append(sec.Rows, row{Data: escape(e.Data), Kept: escape(o.Retention.Kept(e))})
		}
		if len(sec.Rows) > 0 {
			d.Sections = append(d.Sections, sec)
		}
	}
	return d, nil
}

// contactLink makes an email address or an https URL into a Markdown link.
func contactLink(c string) (string, error) {
	href := c
	if !strings.HasPrefix(c, "https://") {
		href = "mailto:" + c
	}
	if !allowedLink.MatchString(href) || (!strings.HasPrefix(c, "https://") && !strings.Contains(c, "@")) {
		return "", fmt.Errorf("%q must be an email address or an https URL", c)
	}
	return "[" + escape(c) + "](" + href + ")", nil
}
