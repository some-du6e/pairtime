// SPDX-License-Identifier: AGPL-3.0-or-later

// Command pagespreview writes pairtimed's public pages to a directory as
// the server sends them, so a person can look at a change to the pages in
// a browser without building pairtimed: pages-preview.yml runs it and
// uploads the directory.
//
// Each page is NAME.html. headers.json maps each file to the
// Content-Security-Policy the server sends with it, so a static server can
// send the same one and show whether the page still works under it.
//
// Usage:
//
//	go run ./tools/pagespreview -out DIR
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"time"

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

// previews are the pages written, each in the states a visitor can see
// it in.
var previews = []struct {
	file, path string
	signIn     pages.SignIn
	signupsOn  bool
}{
	{file: "home.html", path: "/"},
	{file: "home-signin.html", path: "/", signIn: pages.SignIn{Enabled: true, LoginPath: "/auth/hca/login", LogoutPath: "/auth/logout"}},
	{file: "demo.html", path: "/demo"},
	{file: "demo-signups-open.html", path: "/demo", signIn: pages.SignIn{Enabled: true, LoginPath: "/auth/hca/login", LogoutPath: "/auth/logout"}, signupsOn: true},
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
