// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"html"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/some-du6e/pairtime/internal/agent/home"
	"github.com/some-du6e/pairtime/internal/agent/ipc"
	"github.com/some-du6e/pairtime/internal/agent/localhttp"
)

const dashboardUsage = `Usage: pairtime dashboard [--no-open]

Opens your pairtime dashboard in your browser, signed in: your time today
and this week, project by project. It runs on this computer only, served by
the pairtime agent on 127.0.0.1, and it starts the agent first if it isn't
running. The agent gives it a sign-in link over its socket, which only you
can reach, and it hands the link to your browser in a file that only you
can read, never on a command line. The link works once, for two minutes.
The browser stays signed in until the agent stops, or until it goes 8
hours without opening the dashboard.

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
	dirs func() (home.Dirs, error)
	// startAgent starts the agent the way a hook that found it down does
	// (startHookAgent): with the data directory as its working directory,
	// and never as root or with an elevated token, for which it returns
	// errNotTheUsers.
	startAgent func(home.Dirs) error
	// openFile asks the OS to open a local file in the default browser.
	openFile func(string) error
	// wait is how long to wait for an agent that has just started, and
	// ask how long each question to the agent may take.
	wait, ask time.Duration
}

func runDashboard(args []string, stdout, stderr io.Writer) int {
	return dashboard(args, stdout, stderr, dashboardDeps{
		dirs:       home.Default,
		startAgent: startHookAgent,
		openFile:   openBrowser,
		wait:       15 * time.Second,
		ask:        5 * time.Second,
	})
}

// errAgentStarting is an agent that answers on its socket but whose HTTP
// server doesn't listen yet, so it has no link to give.
var errAgentStarting = errors.New("the agent is still starting")

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
	link, err := askLink(dirs, deps.ask)
	switch {
	case errors.Is(err, errAgentDown):
		if err := deps.startAgent(dirs); err != nil {
			sayNotStarted(stderr, err)
			return 1
		}
		link, err = waitForLink(dirs, deps)
	case errors.Is(err, errAgentStarting):
		// An agent a hook or the login item has just started gives a link
		// once its HTTP server listens, a moment later.
		link, err = waitForLink(dirs, deps)
	}
	if err != nil {
		sayNoLink(stderr, err)
		return 1
	}
	if *noOpen {
		fmt.Fprintln(stdout, link.URL)
		return 0
	}
	ttl := words(time.Duration(link.ExpiresInS) * time.Second)
	page, err := writeSignInPage(dirs.Data, link, time.Now())
	if err == nil {
		err = deps.openFile(page)
	}
	if err != nil {
		fmt.Fprintf(stdout, "pairtime couldn't open a browser (%v).\nOpen this link in your browser. It works once, for the next %s:\n  %s\n", err, ttl, link.URL)
		return 0
	}
	fmt.Fprintf(stdout, "Opening your dashboard in your browser.\nIf it doesn't open, paste this link into your browser. It works once, for the next %s:\n  %s\n", ttl, link.URL)
	return 0
}

// askLink asks the agent for a one-time sign-in link over its socket
// (ipc.Ask), within timeout. ipc checks that the agent runs as this user
// before it writes anything, so unlike the local HTTP API this needs no
// bearer token, and the token goes nowhere (D-dashboard-link). Errors are
// sorted as askFailure (stores.go) sorts them: only a socket that nothing
// listens on is errAgentDown, and an agent that doesn't answer in time is
// errAgentSlow, which starts no second agent. An agent that answers with
// no link yet is errAgentStarting. A link it returns is the agent's own
// sign-in page on 127.0.0.1 and holds nothing else (checkLink).
func askLink(dirs home.Dirs, timeout time.Duration) (ipc.DashboardLink, error) {
	ep, err := ipc.EndpointFor(dirs)
	if err != nil {
		return ipc.DashboardLink{}, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	ans, err := ipc.Ask(ctx, ep, ipc.AskDashboardLink)
	if err != nil {
		return ipc.DashboardLink{}, askFailure(ctx, err)
	}
	var link ipc.DashboardLink
	if err := json.Unmarshal(ans, &link); err != nil {
		return ipc.DashboardLink{}, fmt.Errorf("the agent's answer isn't a link: %w", err)
	}
	if link.URL == "" {
		return ipc.DashboardLink{}, errAgentStarting
	}
	if err := checkLink(link); err != nil {
		return ipc.DashboardLink{}, err
	}
	return link, nil
}

// checkLink accepts only a link to the agent's own sign-in page:
// http://127.0.0.1:<port>/signin?code=<code>, with a port and a code of
// the form localhttp.NewToken gives, and nothing more, so the page that
// writeSignInPage writes holds that and nothing else.
func checkLink(link ipc.DashboardLink) error {
	refuse := fmt.Errorf("the agent's link %q isn't its own sign-in page on 127.0.0.1, so pairtime won't open it", link.URL)
	u, err := url.Parse(link.URL)
	if err != nil || u.Scheme != "http" || u.Opaque != "" || u.User != nil || u.Host != "127.0.0.1:"+u.Port() ||
		!validPort(u.Port()) || u.Path != "/signin" || u.RawPath != "" || u.Fragment != "" || u.RawFragment != "" {
		return refuse
	}
	code, found := strings.CutPrefix(u.RawQuery, "code=")
	if !found || !localhttp.ValidToken(code) || link.ExpiresInS <= 0 {
		return refuse
	}
	return nil
}

// validPort reports whether s is a TCP port written plainly: 1 to 65535,
// with no sign or leading zero.
func validPort(s string) bool {
	n, err := strconv.Atoi(s)
	return err == nil && n >= 1 && n <= 65535 && strconv.Itoa(n) == s
}

// waitForLink asks for a link until the agent gives one, for up to
// deps.wait: an agent that has just started listens on its socket first,
// and on its HTTP port a moment later.
func waitForLink(dirs home.Dirs, deps dashboardDeps) (ipc.DashboardLink, error) {
	deadline := time.Now().Add(deps.wait)
	for {
		link, err := askLink(dirs, deps.ask)
		switch {
		case err == nil:
			return link, nil
		case !errors.Is(err, errAgentDown) && !errors.Is(err, errAgentStarting) && !errors.Is(err, errAgentSlow):
			return ipc.DashboardLink{}, err
		case time.Now().After(deadline):
			return ipc.DashboardLink{}, fmt.Errorf("the pairtime agent didn't start within %s: %v. Run `pairtime agent` to see why", words(deps.wait), err)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// sayNotStarted says why the agent that `pairtime dashboard` found down
// isn't running now.
func sayNotStarted(stderr io.Writer, err error) {
	if errors.Is(err, errNotTheUsers) {
		fmt.Fprintln(stderr, "pairtime dashboard: the pairtime agent isn't running, and pairtime won't start it from here: this runs as root or with administrator rights, which the agent would keep on your files. Run pairtime dashboard as yourself, without sudo or an administrator terminal.")
		return
	}
	fmt.Fprintf(stderr, "pairtime dashboard: the pairtime agent isn't running, and starting it failed: %v\n", err)
}

// sayNoLink says why there is no link to open.
func sayNoLink(stderr io.Writer, err error) {
	switch {
	case errors.Is(err, errAgentSlow):
		fmt.Fprintln(stderr, "pairtime dashboard: the pairtime agent is running but didn't answer in time, so pairtime didn't start another one. Try again in a moment.")
	case errors.Is(err, errAgentOld):
		fmt.Fprintln(stderr, "pairtime dashboard: the running agent is from an older pairtime, which can't open the dashboard. Log out and back in to restart it.")
	default:
		fmt.Fprintf(stderr, "pairtime dashboard: %v\n", err)
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

// The page `pairtime dashboard` opens is a file in the data directory that
// only this user can read: it sends the browser on to the agent's sign-in
// link at once, and links to it for a browser that doesn't follow the
// refresh. A browser opened with a URL gets it on its command line, which
// on Linux every other user of the computer can read in /proc while the
// browser starts, in time to use the code first. The path a browser gets
// instead holds no secret (D-dashboard-link).
const (
	signInPrefix = "signin-"
	signInSuffix = ".html"
	// signInKeep is how long a page stays before a later `pairtime
	// dashboard` deletes it, well past the CodeTTL its link works for.
	signInKeep = 10 * time.Minute
)

const signInHTML = `<!doctype html>
<html lang="en">
<meta charset="utf-8">
<meta name="referrer" content="no-referrer">
<meta http-equiv="refresh" content="0; url=%[1]s">
<title>pairtime dashboard</title>
<p>Opening your pairtime dashboard. If it doesn't open, <a href="%[1]s">open it here</a>.</p>
</html>
`

// writeSignInPage writes the page that opens link, which checkLink has
// accepted, to a new file in dir with mode 0600, and returns its path. It
// first deletes the pages earlier runs left that are older than
// signInKeep, whose links no longer work.
func writeSignInPage(dir string, link ipc.DashboardLink, now time.Time) (string, error) {
	sweepSignInPages(dir, now)
	var b [8]byte
	rand.Read(b[:]) // never fails: crypto/rand crashes the program first
	name := filepath.Join(dir, signInPrefix+hex.EncodeToString(b[:])+signInSuffix)
	f, err := os.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return "", fmt.Errorf("writing the sign-in page: %w", err)
	}
	_, werr := fmt.Fprintf(f, signInHTML, html.EscapeString(link.URL))
	if err := errors.Join(werr, f.Close()); err != nil {
		os.Remove(name)
		return "", fmt.Errorf("writing the sign-in page: %w", err)
	}
	return name, nil
}

// sweepSignInPages deletes the sign-in pages in dir last written more than
// signInKeep before now. It leaves anything else alone, and a page it
// can't delete for the next run.
func sweepSignInPages(dir string, now time.Time) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		name := e.Name()
		if !e.Type().IsRegular() || !strings.HasPrefix(name, signInPrefix) || !strings.HasSuffix(name, signInSuffix) {
			continue
		}
		if fi, err := e.Info(); err == nil && now.Sub(fi.ModTime()) > signInKeep {
			os.Remove(filepath.Join(dir, name))
		}
	}
}

// openBrowser asks the OS to open the local file path in the default
// browser. A launcher that is still running after a few seconds has handed
// the file to a browser that is starting, which counts as opening it.
func openBrowser(path string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", path)
	case "darwin":
		cmd = exec.Command("open", path)
	default:
		cmd = exec.Command("xdg-open", path)
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
