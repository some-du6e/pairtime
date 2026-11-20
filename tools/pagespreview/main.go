// SPDX-License-Identifier: AGPL-3.0-or-later

// Command pagespreview writes pairtimed's public pages to a directory as
// the server sends them, so a person can look at a change to the pages in
// a browser without building pairtimed: pages-preview.yml runs it and
// uploads the directory.
//
// Each page is NAME.html, the owner's /dashboard drawn from the sample's
// numbers as a signed-in account's. headers.json maps each file to the
// Content-Security-Policy the server sends with it, so a static server can
// send the same one and show whether the page still works under it.
//
// Usage:
//
//	go run ./tools/pagespreview -out DIR
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"time"

	"github.com/some-du6e/pairtime/internal/server/dashboard"
	"github.com/some-du6e/pairtime/internal/server/pages"
	"github.com/some-du6e/pairtime/internal/server/retention"
)

func main() {
	out := flag.String("out", "", "the directory to write the pages to")
	flag.Parse()
	if *out == "" {
		fmt.Fprintln(os.Stderr, "pagespreview: -out is required")
		os.Exit(2)
	}
	if err := write(*out); err != nil {
		fmt.Fprintln(os.Stderr, "pagespreview:", err)
		os.Exit(1)
	}
}

// signedIn is sign-in as pairtimed wires it, with the preview signed in.
var signedIn = pages.SignIn{
	Enabled: true, LoginPath: "/auth/hca/login", LogoutPath: "/auth/logout",
	Visitor: func(*http.Request) (string, bool) { return "U0PREVIEW", true },
}

// previews are the pages written, each in the states a visitor can see
// it in. owner, when set, is the signed-in account's view for /dashboard.
var previews = []struct {
	file, path string
	signIn     pages.SignIn
	signupsOn  bool
	owner      func(now time.Time) (dashboard.View, error)
}{
	{file: "home.html", path: "/"},
	{file: "home-signin.html", path: "/", signIn: pages.SignIn{Enabled: true, LoginPath: "/auth/hca/login", LogoutPath: "/auth/logout"}},
	{file: "demo.html", path: "/demo"},
	{file: "demo-signups-open.html", path: "/demo", signIn: pages.SignIn{Enabled: true, LoginPath: "/auth/hca/login", LogoutPath: "/auth/logout"}, signupsOn: true},
	{file: "dashboard.html", path: "/dashboard", signIn: signedIn, owner: ownerView},
	{file: "dashboard-empty.html", path: "/dashboard", signIn: signedIn, owner: emptyView},
	{file: "dashboard-off.html", path: "/dashboard"},
	{file: "privacy.html", path: "/privacy"},
	{file: "evidence.html", path: "/docs/evidence"},
}

func write(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	policy, err := retention.Default()
	if err != nil {
		return err
	}
	headers := make(map[string]string)
	for _, p := range previews {
		site, err := pages.New(pages.Options{
			BaseURL:     "https://pairtime.example.org",
			Version:     "preview",
			SignupsOpen: p.signupsOn,
			Retention:   policy,
		})
		if err != nil {
			return err
		}
		mux := http.NewServeMux()
		site.Register(mux)
		site.RegisterHome(mux, p.signIn)
		site.RegisterDemo(mux, p.signIn, time.Now)
		d := pages.Dashboard{Now: time.Now, Logger: slog.Default()}
		if p.owner != nil {
			d.Source = source(p.owner)
			d.Account = func(*http.Request) (string, bool) { return "u1", true }
		}
		site.RegisterDashboard(mux, p.signIn, d)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, p.path, nil))
		if rec.Code != http.StatusOK {
			return fmt.Errorf("GET %s = %d", p.path, rec.Code)
		}
		if err := os.WriteFile(filepath.Join(dir, p.file), rec.Body.Bytes(), 0o644); err != nil {
			return err
		}
		headers[p.file] = rec.Header().Get("Content-Security-Policy")
	}
	b, err := json.MarshalIndent(headers, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "headers.json"), append(b, '\n'), 0o644)
}

// source is a dashboard.Source that answers every account with one view.
type source func(now time.Time) (dashboard.View, error)

func (s source) View(_ context.Context, _ string, now time.Time) (dashboard.View, error) {
	return s(now)
}

// ownerView is the sample's numbers as a signed-in owner's own view, as
// dashboard.Rollups names the agent.
func ownerView(now time.Time) (dashboard.View, error) {
	v, err := dashboard.Demo{}.View(context.Background(), "", now)
	v.Sample, v.Agent, v.TZ = false, dashboard.AgentName, "Europe/London"
	return v, err
}

// emptyView is a new account's view.
func emptyView(now time.Time) (dashboard.View, error) {
	return dashboard.Build(dashboard.Meta{AsOf: now, TZ: "UTC", Today: now.UTC().Format(dashboard.DateLayout), Agent: dashboard.AgentName}, nil)
}
