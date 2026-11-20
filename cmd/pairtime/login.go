// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/some-du6e/pairtime/internal/agent/account"
	"github.com/some-du6e/pairtime/internal/agent/config"
	"github.com/some-du6e/pairtime/internal/agent/home"
	"github.com/some-du6e/pairtime/internal/agent/keys"
	"github.com/some-du6e/pairtime/internal/version"
)

const loginUsage = `Usage: pairtime login [--server URL] [--no-browser]

Link this computer to your pairtime account, so the minutes pairtime counts
here show on your dashboard on the server. pairtime shows a code and the
page to type it on, and opens that page in your browser where it can. Sign
in there with Hack Club and approve. This computer never sees your Hack Club
sign-in: the server gives it a device token of its own, which pairtime
keeps in this computer's keystore with the key that signs its uploads.

What goes up is numbers: minutes, why they counted, and opaque keys. What
you type stays on this computer. pairtime logout undoes the link.

  --server URL   link to this server instead of the one config.toml names,
                 or https://pairtime.karimeltaib.com when it names none
  --no-browser   show the page, and don't open a browser

Exit status:
  0  linked
  1  not linked: the code was turned down or expired, the server isn't
     taking sign-ins yet, or something failed
  2  usage error
`

const logoutUsage = `Usage: pairtime logout

Unlink this computer from its pairtime server. The server removes the
device, so its token stops working, and pairtime forgets the token and the
device key and goes back to local mode. The time pairtime tracked stays on
this computer, and it keeps counting.

Exit status:
  0  unlinked, or this computer wasn't linked
  1  something failed, and the message says what is left to do
  2  usage error
`

// loginTransport, when not nil, makes login's and logout's requests. The
// command's tests set it to trust their test server's certificate.
var loginTransport http.RoundTripper

// loginContext is the parent of login's and logout's context, which
// Ctrl-C also ends. The command's tests give it a deadline.
var loginContext = context.Background

// chainWait is how long login waits for an agent it started to begin its
// ledger chain, whose id is the device's.
const chainWait = 30 * time.Second

func init() {
	register(command{
		name:    "login",
		summary: "Link this computer to your pairtime account, to see your hours online",
		run:     runLogin,
	})
	register(command{
		name:    "logout",
		summary: "Unlink this computer from its pairtime server and go back to local mode",
		run:     runLogout,
	})
}

// parseNoArgs parses a command's flags and refuses arguments.
func parseNoArgs(flags *flag.FlagSet, args []string, stderr io.Writer) (code int, done bool) {
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0, true
		}
		return 2, true
	}
	if flags.NArg() > 0 {
		fmt.Fprintf(stderr, "pairtime %s: takes no arguments, got %q\n", flags.Name(), flags.Args())
		return 2, true
	}
	return 0, false
}

func runLogin(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("login", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.Usage = func() { fmt.Fprint(flags.Output(), loginUsage) }
	server := flags.String("server", "", "the pairtime server to link to")
	noBrowser := flags.Bool("no-browser", false, "don't open a browser")
	if code, done := parseNoArgs(flags, args, stderr); done {
		return code
	}
	dirs, err := home.Default()
	if err == nil {
		err = dirs.Create()
	}
	if err != nil {
		fmt.Fprintf(stderr, "pairtime login: %v\n", err)
		return 1
	}
	// A config.toml that is missing or doesn't parse gives the defaults.
	cfg, _, _ := config.Load(dirs.ConfigFile())
	raw := *server
	if raw == "" {
		raw = cfg.Server.URL
	}
	if raw == "" {
		raw = config.DefaultServer
	}
	srv, err := config.CheckServerURL(raw)
	if err != nil {
		fmt.Fprintf(stderr, "pairtime login: the server %q %v\n", raw, err)
		return 2
	}
	if l, err := account.Load(dirs.Data); err == nil {
		fmt.Fprintf(stdout, "This computer is already linked to %s, as device %s.\nTo link it again, run pairtime logout first.\n", l.Server, l.DeviceID)
		return 1
	}
	id, err := loginDeviceID(dirs, stdout)
	if err != nil {
		fmt.Fprintf(stderr, "pairtime login: %v\n", err)
		return 1
	}
	ks, err := keys.Open(keys.Options{Dir: dirs.Data, Stores: agentKeyStores})
	if err != nil {
		fmt.Fprintf(stderr, "pairtime login: the device keystore can't be opened: %v\n", err)
		return 1
	}
	ctx, stop := signal.NotifyContext(loginContext(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	dev := account.Device{ID: id, OS: account.OSFamily(), Build: account.Build(version.Get().Version)}
	r, err := account.Login(ctx, loginOptions(srv, dirs, ks), dev, func(c account.Code) {
		fmt.Fprintf(stdout, "To link this computer to your pairtime account, open\n\n    %s\n\nand enter the code\n\n    %s\n\n", c.VerificationURI, c.UserCode)
		if u, ok := account.BrowserURL(c, srv); ok && !*noBrowser {
			if account.OpenBrowser(u) == nil {
				fmt.Fprintln(stdout, "pairtime opened that page in your browser.")
			}
		}
		fmt.Fprintf(stdout, "The code works for %d minutes. Waiting for you to approve it (Ctrl-C stops)...\n", max(c.ExpiresIn/60, 1))
	})
	switch {
	case errors.Is(err, account.ErrClosed):
		fmt.Fprintf(stdout, "%s isn't taking sign-ins yet, so this computer can't be linked there. pairtime keeps counting here, and nothing is lost.\n", srv)
		return 1
	case errors.Is(err, account.ErrDenied):
		fmt.Fprintln(stdout, "The code was turned down, so this computer isn't linked.")
		return 1
	case errors.Is(err, account.ErrExpired):
		fmt.Fprintln(stdout, "The code expired before anyone approved it. Run pairtime login again for a new one.")
		return 1
	case ctx.Err() != nil:
		fmt.Fprintln(stdout, "\nStopped. This computer isn't linked.")
		return 1
	case err != nil:
		fmt.Fprintf(stderr, "pairtime login: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "\nLinked. This computer is device %s on %s.\n", r.Linked.DeviceID, srv)
	fmt.Fprintf(stdout, "pairtime uploads the minutes it counts here to that server, as numbers. What you type stays on this computer.\nSee your linked computers at %s/account/devices, and unlink this one with pairtime logout.\n", srv)
	if r.ConfigErr != nil {
		fmt.Fprintf(stderr, "pairtime login: %v\n", r.ConfigErr)
	}
	return 0
}

func runLogout(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("logout", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.Usage = func() { fmt.Fprint(flags.Output(), logoutUsage) }
	if code, done := parseNoArgs(flags, args, stderr); done {
		return code
	}
	dirs, err := home.Default()
	if err != nil {
		fmt.Fprintf(stderr, "pairtime logout: %v\n", err)
		return 1
	}
	_, lerr := account.Load(dirs.Data)
	if errors.Is(lerr, account.ErrNotLinked) {
		if _, err := keys.Inspect(dirs.Data); err != nil {
			// No keystore at all, so nothing to forget, and opening one
			// would make a device key.
			fmt.Fprintln(stdout, "This computer isn't linked to a pairtime server.")
			return 0
		}
	}
	ks, err := keys.Open(keys.Options{Dir: dirs.Data, Stores: agentKeyStores})
	if err != nil {
		fmt.Fprintf(stderr, "pairtime logout: the device keystore can't be opened: %v\n", err)
		return 1
	}
	ctx, stop := signal.NotifyContext(loginContext(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	r, err := account.Logout(ctx, loginOptions("", dirs, ks))
	switch {
	case errors.Is(err, account.ErrNotLinked):
		fmt.Fprintln(stdout, "This computer isn't linked to a pairtime server.")
		return 0
	case err != nil:
		fmt.Fprintf(stderr, "pairtime logout: %v\n", err)
		return 1
	}
	if r.ServerErr != nil {
		fmt.Fprintf(stdout, "pairtime couldn't reach %s to remove this computer (%v), so the server still lists it. This computer has forgotten its token, so it can't upload; remove it at %s/account/devices.\n", r.Linked.Server, r.ServerErr, r.Linked.Server)
	}
	fmt.Fprintf(stdout, "Unlinked from %s. pairtime is back in local mode: it keeps counting, and uploads nothing.\n", r.Linked.Server)
	if r.ConfigErr != nil {
		fmt.Fprintf(stderr, "pairtime logout: %v\n", r.ConfigErr)
	}
	return 0
}

// loginOptions are the account options of login and logout.
func loginOptions(server string, dirs home.Dirs, ks *keys.Keystore) account.Options {
	return account.Options{
		Server:     server,
		DataDir:    dirs.Data,
		ConfigFile: dirs.ConfigFile(),
		Secrets:    ks,
		HTTP:       &http.Client{Timeout: 30 * time.Second, Transport: loginTransport},
		UserAgent:  "pairtime-cli/" + version.Get().Version,
	}
}

// loginDeviceID returns this install's ledger chain id, its device id on
// the server (docs/spec/device-auth-v1.md section 1). The agent's ledger
// records it in keystore.json when it starts its chain, at the agent's
// first start. When that hasn't happened yet, login starts the agent and
// waits for it.
func loginDeviceID(dirs home.Dirs, stdout io.Writer) (string, error) {
	id, err := keys.Chain(dirs.Data)
	if err != nil || id != "" {
		return id, err
	}
	fmt.Fprintln(stdout, "Starting pairtime's agent, which hasn't run on this computer yet...")
	if _, err := startAgent(""); err != nil {
		return "", fmt.Errorf("starting the agent: %w", err)
	}
	for deadline := time.Now().Add(chainWait); time.Now().Before(deadline); {
		time.Sleep(200 * time.Millisecond)
		if id, err := keys.Chain(dirs.Data); err != nil || id != "" {
			return id, err
		}
	}
	return "", fmt.Errorf("the agent didn't start its ledger within %v; run pairtime agent to see why, then pairtime login again", chainWait)
}
