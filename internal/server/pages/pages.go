// SPDX-License-Identifier: AGPL-3.0-or-later

// Package pages serves pairtimed's static pages (WP-SV14): the privacy
// notice at /privacy (DESIGN 4.4, 8.6), the definition of evidence levels
// and bands at /docs/evidence (DESIGN 6.4), and the page Hack Club Auth
// sign-in shows while sign-ups are closed (DESIGN 8.6, 10.2).
//
// Each page is a Markdown template in this directory: privacy.md,
// evidence/<version>.md and signups-closed.md. New renders all of them
// once, from the configuration and the retention schedule, so serving a
// page only copies bytes, and a mistake in a template stops pairtimed at
// start instead of reaching a reader.
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
// MAJOR needs a file of its own before a server can run it.
// /docs/evidence shows the definition for the scoring version this server
// runs, and /docs/evidence/<version> the one for any other version.
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
	"io/fs"
	"net/http"
	"path"
	"slices"
	"strconv"
	"strings"
	texttemplate "text/template"

	"github.com/some-du6e/pairtime/internal/server/retention"
)

//go:embed layout.html style.css privacy.md signups-closed.md evidence/*.md
var files embed.FS

// SourceURL is where the code this server runs is published, which AGPL-3.0
// section 13 asks a network service to offer its users.
const SourceURL = "https://github.com/some-du6e/pairtime"

// BuiltinScoringVersion is the scoring version /docs/evidence describes
// when pairtime.toml leaves scoring_version empty. It is the first scoring
// version, the one pkg/params' first table and pkg/score/VERSION carry
// (WP-PA1, WP-SC1). Once pkg/score is part of the server, its version
// replaces this constant.
const BuiltinScoringVersion = "0.1.0"

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
	// Retention is the retention schedule the notice publishes.
	Retention *retention.Policy
}

// Site holds the rendered pages.
type Site struct {
	privacy []byte
	// current is the evidence definition for the scoring version this
	// server runs.
	current  []byte
	evidence []evidencePage // oldest first
	csp      string
	// Signups is the sign-up gate. HCA sign-in (WP-SV2) calls its Admit
	// before it creates an account.
	Signups *Signups
}

type evidencePage struct {
	from version
	page []byte
}

// New renders every page. It fails on a mistake in a template or in its
// Markdown, on a controller contact that can't be a link, and on a scoring
// version that no evidence definition covers.
func New(o Options) (*Site, error) {
	if o.Retention == nil {
		return nil, errors.New("pages: no retention schedule")
	}
	running := o.ScoringVersion
	if running == "" {
		running = BuiltinScoringVersion
	}
	rv, ok := parseVersion(running)
	if !ok {
		return nil, fmt.Errorf("pages: scoring version %q isn't of the form X.Y.Z", running)
	}
	l, err := newLayout(o.Version)
	if err != nil {
		return nil, err
	}
	data, err := newPrivacyData(o)
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
	s := &Site{csp: l.csp}
	if s.privacy, err = l.page("Privacy notice", "privacy.md", data, funcs); err != nil {
		return nil, err
	}
	if s.evidence, err = l.evidencePages(rv); err != nil {
		return nil, err
	}
	if s.current, ok = s.evidenceFor(rv); !ok {
		return nil, fmt.Errorf("pages: no evidence definition covers scoring %s; add internal/server/pages/evidence/%s.md (DESIGN 6.4, 6.6)", rv, rv)
	}
	refusal, err := l.page("Sign-ups are closed", "signups-closed.md", data, nil)
	if err != nil {
		return nil, err
	}
	s.Signups = &Signups{open: o.SignupsOpen, operators: make(map[string]bool), refusal: refusal, csp: l.csp}
	for _, sub := range o.Operators {
		s.Signups.operators[sub] = true
	}
	return s, nil
}

// Register adds the pages to mux.
func (s *Site) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /privacy", func(w http.ResponseWriter, _ *http.Request) {
		writePage(w, http.StatusOK, s.privacy, s.csp, "no-cache")
	})
	mux.HandleFunc("GET /docs/evidence", func(w http.ResponseWriter, _ *http.Request) {
		writePage(w, http.StatusOK, s.current, s.csp, "no-cache")
	})
	mux.HandleFunc("GET /docs/evidence/{version}", func(w http.ResponseWriter, r *http.Request) {
		if v, ok := parseVersion(r.PathValue("version")); ok {
			if page, found := s.evidenceFor(v); found {
				writePage(w, http.StatusOK, page, s.csp, "no-cache")
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
// own stylesheet.
func writePage(w http.ResponseWriter, code int, page []byte, csp, cache string) {
	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("Content-Security-Policy", csp)
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("Cache-Control", cache)
	w.WriteHeader(code)
	w.Write(page)
}

// layout wraps rendered Markdown in layout.html.
type layout struct {
	tmpl    *template.Template
	css     string
	csp     string
	version string
}

type layoutData struct {
	Title   string
	CSS     template.CSS
	Body    template.HTML
	Version string
	Source  string
}

func newLayout(version string) (*layout, error) {
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
	csp := "default-src 'none'; style-src 'sha256-" + base64.StdEncoding.EncodeToString(sum[:]) +
		"'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'"
	return &layout{tmpl: t, css: string(css), csp: csp, version: version}, nil
}

// page executes the Markdown template name with data, renders it and wraps
// it in the layout.
func (l *layout) page(title, name string, data any, funcs texttemplate.FuncMap) ([]byte, error) {
	src, err := fs.ReadFile(files, name)
	if err != nil {
		return nil, fmt.Errorf("pages: %w", err)
	}
	t, err := texttemplate.New(path.Base(name)).Funcs(funcs).Parse(string(src))
	if err != nil {
		return nil, fmt.Errorf("pages: %w", err)
	}
	var md strings.Builder
	if err := t.Execute(&md, data); err != nil {
		return nil, fmt.Errorf("pages: %w", err)
	}
	body, err := render(md.String())
	if err != nil {
		return nil, fmt.Errorf("pages: %s: %w", name, err)
	}
	var out bytes.Buffer
	err = l.tmpl.Execute(&out, layoutData{
		Title:   title,
		CSS:     template.CSS(l.css),
		Body:    template.HTML(body),
		Version: l.version,
		Source:  SourceURL,
	})
	if err != nil {
		return nil, fmt.Errorf("pages: %w", err)
	}
	return out.Bytes(), nil
}

// evidenceData is what an evidence definition's template reads.
type evidenceData struct {
	// Covers says which scoring versions the definition is for.
	Covers string
	// Running is the scoring version this server runs.
	Running string
	// Others links every definition, when there is more than one.
	Others string
}

// evidencePages renders every evidence definition, oldest first.
func (l *layout) evidencePages(running version) ([]evidencePage, error) {
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
	pages := make([]evidencePage, 0, len(froms))
	for i, v := range froms {
		d := evidenceData{
			Covers:  fmt.Sprintf("scoring %s and the later %d.x versions", v, v[0]),
			Running: running.String(),
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

// privacyData is what privacy.md and signups-closed.md read. Every string
// is Markdown, with values from the configuration escaped.
type privacyData struct {
	// Named says whether the controller's name and contact are both set.
	Named      bool
	Controller string
	Contact    string
	DPO        string
	BaseURL    string
	SourceURL  string
	Version    string
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

func newPrivacyData(o Options) (*privacyData, error) {
	c := o.Controller
	d := &privacyData{
		Named:             c.Name != "" && c.Contact != "",
		Controller:        notNamed,
		Contact:           notNamed,
		BaseURL:           escape(o.BaseURL),
		SourceURL:         SourceURL,
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
