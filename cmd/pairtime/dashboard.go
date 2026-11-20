// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/some-du6e/pairtime/internal/agent/home"
	"github.com/some-du6e/pairtime/internal/agent/localhttp"
)

const dashboardUsage = `Usage: pairtime dashboard [--no-open]

Opens your pairtime dashboard in your browser, signed in: your time today
and this week, project by project. It runs on this computer only, served by
the pairtime agent on 127.0.0.1, and it starts the agent first if it isn't
running. The link it opens works once, for two minutes, so nobody else can
use it. The browser stays signed in until the agent stops, or until it goes
8 hours without opening the dashboard.

  --no-open  Print the link instead of opening a browser.

Exit status:
  0  the browser was asked to open the dashboard, or --no-open printed its link
  1  the agent couldn't be started or didn't answer
  2  usage error
`

func init() {
	register(command{
		name:    "dashboard",
		summary: "Open your dashboard in your browser",
		run:     runDashboard,
	})
}

// dashboardDeps is what `pairtime dashboard` reaches outside this process,
// so tests can stand in for it.
type dashboardDeps struct {
	dirs       func() (home.Dirs, error)
	startAgent func() error
	openURL    func(string) error
	// wait is how long to wait for an agent this command started.
	wait   time.Duration
	client *http.Client
}

func runDashboard(args []string, stdout, stderr io.Writer) int {
	return dashboard(args, stdout, stderr, dashboardDeps{
		dirs:       home.Default,
		startAgent: startAgent,
		openURL:    openBrowser,
		wait:       15 * time.Second,
		client:     &http.Client{Timeout: 5 * time.Second},
	})
}

func dashboard(args []string, stdout, stderr io.Writer, deps dashboardDeps) int {
	flags := flag.NewFlagSet("dashboard", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.Usage = func() { fmt.Fprint(flags.Output(), dashboardUsage) }
	noOpen := flags.Bool("no-open", false, "print the link instead of opening a browser")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if flags.NArg() > 0 {
		fmt.Fprintf(stderr, "pairtime dashboard: takes no arguments, got %q\n", flags.Args())
		return 2
	}
	dirs, err := deps.dirs()
	if err != nil {
		fmt.Fprintf(stderr, "pairtime dashboard: %v\n", err)
		return 1
	}
	link, err := askLink(deps.client, dirs.PortFile())
	if errors.Is(err, errAgentDown) {
		if err := deps.startAgent(); err != nil {
			fmt.Fprintf(stderr, "pairtime dashboard: the pairtime agent isn't running, and starting it failed: %v\n", err)
			return 1
		}
		link, err = waitForLink(deps, dirs.PortFile())
	}
	if err != nil {
		fmt.Fprintf(stderr, "pairtime dashboard: %v\n", err)
		return 1
	}
	if *noOpen {
		fmt.Fprintln(stdout, link.URL)
		return 0
	}
	ttl := words(time.Duration(link.ExpiresInS) * time.Second)
	if err := deps.openURL(link.URL); err != nil {
		fmt.Fprintf(stdout, "pairtime couldn't open a browser (%v).\nOpen this link in your browser. It works once, for the next %s:\n  %s\n", err, ttl, link.URL)
		return 0
	}
	fmt.Fprintf(stdout, "Opening your dashboard in your browser.\nIf nothing opens, paste this link into it. It works once, for the next %s:\n  %s\n", ttl, link.URL)
	return 0
}

// errAgentDown means no agent answers for the data directory: port.json is
// missing, says the agent stopped, or names a port nothing answers on.
var errAgentDown = errors.New("the pairtime agent isn't running")

// dashboardLink is the agent's answer to POST /v1/dashboard/link.
type dashboardLink struct {
	URL        string `json:"url"`
	ExpiresInS int    `json:"expires_in_s"`
}

// askLink asks the agent port.json names for a one-time sign-in link. It
// returns only a link to that agent's own /signin on 127.0.0.1, so nothing
// else is ever handed to the browser.
func askLink(client *http.Client, portFile string) (dashboardLink, error) {
	pf, err := localhttp.ReadPortFile(portFile)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return dashboardLink{}, errAgentDown
	case err != nil:
		return dashboardLink{}, err
	case pf.PID == 0 || pf.Port == 0:
		return dashboardLink{}, errAgentDown
	}
	origin := "http://127.0.0.1:" + strconv.Itoa(pf.Port)
	req, err := http.NewRequest(http.MethodPost, origin+"/v1/dashboard/link", nil)
	if err != nil {
		return dashboardLink{}, err
	}
	req.Header.Set("Authorization", "Bearer "+pf.Token)
	resp, err := client.Do(req)
	if err != nil {
		return dashboardLink{}, fmt.Errorf("%w: %v", errAgentDown, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return dashboardLink{}, fmt.Errorf("reading the agent's answer: %w", err)
	}
	switch resp.StatusCode {
	case http.StatusCreated:
	case http.StatusNotFound:
		return dashboardLink{}, fmt.Errorf("the running agent is from an older pairtime, which can't open the dashboard; log out and back in to restart it")
	default:
		return dashboardLink{}, fmt.Errorf("the agent answered %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	var link dashboardLink
	if err := json.Unmarshal(body, &link); err != nil {
		return dashboardLink{}, fmt.Errorf("the agent's answer isn't a link: %w", err)
	}
	if !strings.HasPrefix(link.URL, origin+"/signin?") {
		return dashboardLink{}, fmt.Errorf("the agent's link %q isn't its own sign-in page on %s, so pairtime won't open it", link.URL, origin)
	}
	return link, nil
}

// waitForLink asks for a link until an agent that was just started
// answers, for up to deps.wait. It reads port.json each time, because the
// new agent writes it with the port it got.
func waitForLink(deps dashboardDeps, portFile string) (dashboardLink, error) {
	deadline := time.Now().Add(deps.wait)
	for {
		link, err := askLink(deps.client, portFile)
		if !errors.Is(err, errAgentDown) {
			return link, err
		}
		if time.Now().After(deadline) {
			return dashboardLink{}, fmt.Errorf("the pairtime agent didn't start within %s: %v. Run `pairtime agent` to see why", words(deps.wait), err)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// words says d in whole minutes or seconds: "2 minutes", "15 seconds".
func words(d time.Duration) string {
	n, unit := int(d/time.Second), "second"
	if n >= 60 && n%60 == 0 {
		n, unit = n/60, "minute"
	}
	if n != 1 {
		unit += "s"
	}
	return strconv.Itoa(n) + " " + unit
}

// startAgent starts `pairtime agent` in a process of its own, as `agent
// --detach` does, and returns without waiting for it.
func startAgent() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	cmd := exec.Command(exe, "agent")
	cmd.SysProcAttr = detachedAttr()
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}

// openBrowser asks the OS to open u in the default browser. A launcher
// that is still running after a few seconds has handed u to a browser
// that is starting, which counts as opening it.
func openBrowser(u string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		// rundll32 takes the URL as one argument; cmd's start would read
		// the & of a query string as the start of another command.
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", u)
	case "darwin":
		cmd = exec.Command("open", u)
	default:
		cmd = exec.Command("xdg-open", u)
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		return err
	case <-time.After(3 * time.Second):
		return nil
	}
}
