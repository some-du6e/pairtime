// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

const healthcheckSummary = "Exit 0 if the running server's /readyz answers 200; the container's HEALTHCHECK"

func init() {
	register(&command{name: "healthcheck", summary: healthcheckSummary, run: runHealthcheck})
}

// runHealthcheck is what compose's healthcheck runs inside the distroless
// image, which has no curl (DESIGN 10.1). It exits 0 for healthy and 1 for
// unhealthy, as Docker expects, and never 2.
func runHealthcheck(ctx context.Context, e *env, args []string) int {
	fs := newFlagSet("healthcheck", healthcheckSummary, e)
	load := configFlags(fs, e)
	target := fs.String("url", "", "check this URL instead of /readyz on ops_listen")
	live := fs.Bool("live", false, "check /healthz, which says only that the process is up, instead of /readyz")
	timeout := fs.Duration("timeout", 5*time.Second, "how long to wait for an answer")
	if code, done := parse(fs, args); done {
		return min(code, 1)
	}
	unhealthy := func(format string, a ...any) int {
		fmt.Fprintf(e.stderr, "unhealthy: "+format+"\n", a...)
		return 1
	}

	u := *target
	if u == "" {
		cfg, err := load()
		if err != nil {
			return unhealthy("%v", err)
		}
		path := "/readyz"
		if *live {
			path = "/healthz"
		}
		u = "http://" + dialAddr(cfg.OpsListen) + path
	}
	ctx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return unhealthy("%v", err)
	}
	// No proxy: the ops listener is on this host.
	client := &http.Client{Transport: &http.Transport{Proxy: nil, DisableKeepAlives: true}}
	resp, err := client.Do(req)
	if err != nil {
		return unhealthy("%v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if resp.StatusCode != http.StatusOK {
		return unhealthy("%s answered %s: %s", u, resp.Status, strings.TrimSpace(string(body)))
	}
	fmt.Fprintf(e.stdout, "healthy: %s answered %s\n", u, resp.Status)
	return 0
}

// dialAddr turns a listen address into one to connect to: an empty or
// unspecified host becomes the loopback address.
func dialAddr(listen string) string {
	host, port, err := net.SplitHostPort(listen)
	if err != nil {
		return listen
	}
	switch host {
	case "", "0.0.0.0":
		host = "127.0.0.1"
	case "::":
		host = "::1"
	}
	return net.JoinHostPort(host, port)
}
