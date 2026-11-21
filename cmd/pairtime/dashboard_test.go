// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/internal/agent/home"
	"github.com/some-du6e/pairtime/internal/agent/ipc"
	"github.com/some-du6e/pairtime/internal/agent/localhttp"
	"github.com/some-du6e/pairtime/internal/platform"
)

// linkAnswer answers ipc.AskDashboardLink with url, as the agent does, and
// no other question.
func linkAnswer(url string) func(string) (json.RawMessage, error) {
	return func(q string) (json.RawMessage, error) {
		if q != ipc.AskDashboardLink {
			return nil, ipc.ErrUnknownQuestion
		}
		return json.Marshal(ipc.DashboardLink{URL: url, ExpiresInS: 120})
	}
}

// signInPages lists the sign-in pages in dir.
func signInPages(t *testing.T, dir string) []string {
	t.Helper()
	got, err := filepath.Glob(filepath.Join(dir, signInPrefix+"*"+signInSuffix))
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestDashboard(t *testing.T) {
	code := localhttp.NewToken()
	good := "http://127.0.0.1:7247/signin?code=" + code
	type world struct {
		h       fakeHome
		opened  []string
		started int
		asked   func() []string // what the agent was asked, once one listens
		pages   string          // where pageDir puts the page; "" is the data directory
	}
	tests := []struct {
		name     string
		args     []string
		setup    func(t *testing.T, w *world)       // starts the agent when one runs already
		start    func(t *testing.T, w *world) error // nil: the agent can't be started
		open     error                              // what opening the page returns
		ask      time.Duration                      // how long each question may take; 0 is 2 s
		restart  time.Duration                      // how long before a start is tried again; 0 is never
		alive    func(w *world) (int, bool)         // agentAlive; nil is no agent running
		wantCode int
		check    func(t *testing.T, w *world, stdout, stderr string)
	}{
		{
			name:  "with the agent running it opens a page only you can read, which sends the browser to the agent's link",
			setup: func(t *testing.T, w *world) { w.asked = w.h.agent(linkAnswer(good)) },
			check: func(t *testing.T, w *world, stdout, stderr string) {
				if len(w.opened) != 1 {
					t.Fatalf("opened %v, want the sign-in page", w.opened)
				}
				page := w.opened[0]
				if filepath.Dir(page) != w.h.dir || !strings.HasPrefix(filepath.Base(page), signInPrefix) {
					t.Errorf("opened %s, want a sign-in page in the data directory %s", page, w.h.dir)
				}
				b, err := os.ReadFile(page)
				if err != nil {
					t.Fatal(err)
				}
				if !strings.Contains(string(b), `content="0; url=`+good+`"`) || !strings.Contains(string(b), `href="`+good+`"`) {
					t.Errorf("the page doesn't send the browser to %s:\n%s", good, b)
				}
				if runtime.GOOS != "windows" {
					fi, err := os.Stat(page)
					if err != nil {
						t.Fatal(err)
					}
					if fi.Mode().Perm() != 0o600 {
						t.Errorf("the page's mode is %v, want 0600", fi.Mode().Perm())
					}
				}
				if w.started != 0 {
					t.Error("it started a second agent")
				}
			},
		},
		{
			name:  "the code is in the page, never in the path the browser is opened with",
			setup: func(t *testing.T, w *world) { w.h.agent(linkAnswer(good)) },
			check: func(t *testing.T, w *world, stdout, stderr string) {
				if len(w.opened) != 1 || strings.Contains(w.opened[0], code) || strings.Contains(w.opened[0], "signin?") {
					t.Errorf("opened %v, want a path with no code in it", w.opened)
				}
			},
		},
		{
			name:  "it prints the link to paste, and how long it works",
			setup: func(t *testing.T, w *world) { w.h.agent(linkAnswer(good)) },
			check: func(t *testing.T, w *world, stdout, stderr string) {
				if !strings.Contains(stdout, good) || !strings.Contains(stdout, "It works once, for the next 2 minutes") {
					t.Errorf("stdout %q doesn't give the link and how long it works", stdout)
				}
			},
		},
		{
			name:  "it asks the agent for the link over its socket, and nothing else",
			setup: func(t *testing.T, w *world) { w.asked = w.h.agent(linkAnswer(good)) },
			check: func(t *testing.T, w *world, stdout, stderr string) {
				if got := w.asked(); len(got) != 1 || got[0] != ipc.AskDashboardLink {
					t.Errorf("the agent was asked %q, want %q once", got, ipc.AskDashboardLink)
				}
			},
		},
		{
			name:  "--no-open prints the link alone, and writes and opens nothing",
			args:  []string{"--no-open"},
			setup: func(t *testing.T, w *world) { w.h.agent(linkAnswer(good)) },
			check: func(t *testing.T, w *world, stdout, stderr string) {
				if stdout != good+"\n" || len(w.opened) != 0 || len(signInPages(t, w.h.dir)) != 0 {
					t.Errorf("stdout %q, opened %v, pages %v; want %q alone", stdout, w.opened, signInPages(t, w.h.dir), good)
				}
			},
		},
		{
			name: "with nothing on the socket it starts the agent as a hook does and opens the link once the agent answers",
			start: func(t *testing.T, w *world) error {
				w.h.agent(linkAnswer(good))
				return nil
			},
			check: func(t *testing.T, w *world, stdout, stderr string) {
				if w.started != 1 || len(w.opened) != 1 {
					t.Errorf("started %d agents and opened %v, want 1 and the page", w.started, w.opened)
				}
			},
		},
		{
			name: "an agent that has no link yet, before its HTTP server listens, is asked again and no second one starts",
			setup: func(t *testing.T, w *world) {
				var n atomic.Int32
				w.asked = w.h.agent(func(q string) (json.RawMessage, error) {
					if n.Add(1) <= 3 {
						return json.Marshal(ipc.DashboardLink{})
					}
					return linkAnswer(good)(q)
				})
			},
			check: func(t *testing.T, w *world, stdout, stderr string) {
				if w.started != 0 || len(w.opened) != 1 || len(w.asked()) != 4 {
					t.Errorf("started %d, opened %v, asked %d times; want no start, the page, and 4 questions", w.started, w.opened, len(w.asked()))
				}
			},
		},
		{
			name:     "when the agent can't be started it says so and exits 1",
			start:    func(t *testing.T, w *world) error { return errors.New("no such file") },
			wantCode: 1,
			check: func(t *testing.T, w *world, stdout, stderr string) {
				if !strings.Contains(stderr, "isn't running, and starting it failed: no such file") || len(w.opened) != 0 {
					t.Errorf("stderr %q, opened %v", stderr, w.opened)
				}
			},
		},
		{
			name:     "run as root or with an elevated token it starts no agent and says why",
			start:    func(t *testing.T, w *world) error { return errNotTheUsers },
			wantCode: 1,
			check: func(t *testing.T, w *world, stdout, stderr string) {
				if !strings.Contains(stderr, "pairtime won't start it here") || len(w.opened) != 0 {
					t.Errorf("stderr %q, opened %v", stderr, w.opened)
				}
				// Windows refuses an elevated token; elsewhere the data
				// directory's owner is someone else.
				if want := w.h.dir; runtime.GOOS == "windows" {
					if !strings.Contains(stderr, "administrator") {
						t.Errorf("stderr %q doesn't name administrator rights", stderr)
					}
				} else if !strings.Contains(stderr, want) || !strings.Contains(stderr, "belongs to another user") {
					t.Errorf("stderr %q doesn't say %s belongs to another user", stderr, want)
				}
			},
		},
		{
			name:     "when a started agent never answers it gives up after the wait and exits 1",
			start:    func(t *testing.T, w *world) error { return nil },
			wantCode: 1,
			check: func(t *testing.T, w *world, stdout, stderr string) {
				if !strings.Contains(stderr, "didn't start within 1 second") || len(w.opened) != 0 {
					t.Errorf("stderr %q, opened %v", stderr, w.opened)
				}
			},
		},
		{
			name: "an agent that doesn't answer in time is reported as slow, and no second agent starts",
			setup: func(t *testing.T, w *world) {
				release := make(chan struct{})
				w.h.agent(func(q string) (json.RawMessage, error) {
					<-release
					return linkAnswer(good)(q)
				})
				// Registered after the agent, so it runs before the
				// agent's cleanup waits for its answer.
				t.Cleanup(func() { close(release) })
			},
			ask:      200 * time.Millisecond,
			wantCode: 1,
			check: func(t *testing.T, w *world, stdout, stderr string) {
				if !strings.Contains(stderr, "didn't answer in time") || w.started != 0 || len(w.opened) != 0 {
					t.Errorf("stderr %q, started %d, opened %v", stderr, w.started, w.opened)
				}
			},
		},
		{
			name:     "an agent that doesn't know the question is from an older pairtime, and no second agent starts",
			setup:    func(t *testing.T, w *world) { w.h.agent(nil) },
			wantCode: 1,
			check: func(t *testing.T, w *world, stdout, stderr string) {
				if !strings.Contains(stderr, "older pairtime") || w.started != 0 {
					t.Errorf("stderr %q, started %d", stderr, w.started)
				}
			},
		},
		{
			name:     "a link that isn't the agent's own sign-in page on 127.0.0.1 is never opened or written",
			setup:    func(t *testing.T, w *world) { w.h.agent(linkAnswer("https://example.com/signin?code=" + code)) },
			wantCode: 1,
			check: func(t *testing.T, w *world, stdout, stderr string) {
				if len(w.opened) != 0 || len(signInPages(t, w.h.dir)) != 0 || !strings.Contains(stderr, "won't open it") {
					t.Errorf("opened %v, pages %v, stderr %q", w.opened, signInPages(t, w.h.dir), stderr)
				}
			},
		},
		{
			name:  "when no browser opens it prints the link to paste and exits 0",
			setup: func(t *testing.T, w *world) { w.h.agent(linkAnswer(good)) },
			open:  errors.New("xdg-open: no method available"),
			check: func(t *testing.T, w *world, stdout, stderr string) {
				if !strings.Contains(stdout, "couldn't open a browser (xdg-open: no method available)") || !strings.Contains(stdout, good) {
					t.Errorf("stdout %q", stdout)
				}
			},
		},
		{
			name: "a started agent that exits at once, as one does while a stopping agent holds the lock, is started again",
			start: func(t *testing.T, w *world) error {
				if w.started == 2 {
					w.h.agent(linkAnswer(good))
				}
				return nil
			},
			restart: 200 * time.Millisecond,
			check: func(t *testing.T, w *world, stdout, stderr string) {
				if w.started != 2 || len(w.opened) != 1 {
					t.Errorf("started %d agents and opened %v, want 2 and the page", w.started, w.opened)
				}
			},
		},
		{
			name: "a second start that is refused says why and exits 1",
			start: func(t *testing.T, w *world) error {
				if w.started == 2 {
					return errNotTheUsers
				}
				return nil
			},
			restart:  200 * time.Millisecond,
			wantCode: 1,
			check: func(t *testing.T, w *world, stdout, stderr string) {
				if !strings.Contains(stderr, "pairtime won't start it here") || len(w.opened) != 0 {
					t.Errorf("stderr %q, opened %v", stderr, w.opened)
				}
			},
		},
		{
			name: "the page goes in the folder pageDir names, and the data directory keeps none",
			setup: func(t *testing.T, w *world) {
				w.h.agent(linkAnswer(good))
				w.pages = t.TempDir()
			},
			check: func(t *testing.T, w *world, stdout, stderr string) {
				if len(w.opened) != 1 || filepath.Dir(w.opened[0]) != w.pages || len(signInPages(t, w.h.dir)) != 0 {
					t.Errorf("opened %v, want a page in %s and none in %s", w.opened, w.pages, w.h.dir)
				}
			},
		},
		{
			name:     "an agent that runs with a socket this terminal can't reach is named, and no second agent starts",
			alive:    func(w *world) (int, bool) { return 4242, true },
			wantCode: 1,
			check: func(t *testing.T, w *world, stdout, stderr string) {
				if !strings.Contains(stderr, "is running, as process 4242, but pairtime dashboard can't reach it") || w.started != 0 || len(w.opened) != 0 {
					t.Errorf("stderr %q, started %d, opened %v", stderr, w.started, w.opened)
				}
			},
		},
		{
			name:     "a started agent that runs but never answers here is named once the wait is over, not said not to have started",
			start:    func(t *testing.T, w *world) error { return nil },
			alive:    func(w *world) (int, bool) { return 4242, w.started > 0 },
			wantCode: 1,
			check: func(t *testing.T, w *world, stdout, stderr string) {
				if !strings.Contains(stderr, "can't reach it") || strings.Contains(stderr, "didn't start") || w.started != 1 {
					t.Errorf("stderr %q, started %d", stderr, w.started)
				}
			},
		},
		{name: "an argument is a usage error", args: []string{"today"}, wantCode: 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := &world{h: newHome(t)}
			if tt.setup != nil {
				tt.setup(t, w)
			}
			ask := tt.ask
			if ask == 0 {
				ask = 2 * time.Second
			}
			restart := tt.restart
			if restart == 0 {
				restart = time.Hour
			}
			deps := dashboardDeps{
				dirs: func() (home.Dirs, error) { return w.h.dirs(), nil },
				startAgent: func(d home.Dirs) error {
					w.started++
					if d != w.h.dirs() {
						t.Errorf("startAgent got %+v, want the dashboard's directories %+v", d, w.h.dirs())
					}
					if tt.start == nil {
						return errors.New("this test starts no agent")
					}
					return tt.start(t, w)
				},
				openFile: func(p string) error {
					w.opened = append(w.opened, p)
					return tt.open
				},
				agentAlive: func(home.Dirs) (int, bool) {
					if tt.alive == nil {
						return 0, false
					}
					return tt.alive(w)
				},
				pageDir: func(d home.Dirs) string {
					if w.pages != "" {
						return w.pages
					}
					return d.Data
				},
				wait:    time.Second,
				ask:     ask,
				restart: restart,
			}
			var stdout, stderr bytes.Buffer
			if code := dashboard(tt.args, &stdout, &stderr, deps); code != tt.wantCode {
				t.Fatalf("exit %d, want %d; stdout %q, stderr %q", code, tt.wantCode, stdout.String(), stderr.String())
			}
			if tt.check != nil {
				tt.check(t, w, stdout.String(), stderr.String())
			}
		})
	}
}

func TestCheckLink(t *testing.T) {
	code := localhttp.NewToken()
	tests := []struct {
		name string
		url  string
		ttl  int
		ok   bool
	}{
		{name: "the agent's own sign-in link passes", url: "http://127.0.0.1:7247/signin?code=" + code, ttl: 120, ok: true},
		{name: "https is refused", url: "https://127.0.0.1:7247/signin?code=" + code, ttl: 120},
		{name: "another host is refused", url: "http://example.com:7247/signin?code=" + code, ttl: 120},
		{name: "localhost is refused, since the agent links 127.0.0.1", url: "http://localhost:7247/signin?code=" + code, ttl: 120},
		{name: "a link without a port is refused", url: "http://127.0.0.1/signin?code=" + code, ttl: 120},
		{name: "a port with a leading zero is refused", url: "http://127.0.0.1:07247/signin?code=" + code, ttl: 120},
		{name: "a port past 65535 is refused", url: "http://127.0.0.1:70000/signin?code=" + code, ttl: 120},
		{name: "another path is refused", url: "http://127.0.0.1:7247/v1/status?code=" + code, ttl: 120},
		{name: "a second parameter is refused", url: "http://127.0.0.1:7247/signin?code=" + code + "&next=https://example.com", ttl: 120},
		{name: "a code that isn't a token is refused", url: "http://127.0.0.1:7247/signin?code=abc", ttl: 120},
		{name: "markup in place of the code is refused", url: `http://127.0.0.1:7247/signin?code="><script>x</script>`, ttl: 120},
		{name: "a fragment is refused", url: "http://127.0.0.1:7247/signin?code=" + code + "#x", ttl: 120},
		{name: "a user name is refused", url: "http://x@127.0.0.1:7247/signin?code=" + code, ttl: 120},
		{name: "a link with no time left is refused", url: "http://127.0.0.1:7247/signin?code=" + code, ttl: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := checkLink(ipc.DashboardLink{URL: tt.url, ExpiresInS: tt.ttl})
			if (err == nil) != tt.ok {
				t.Errorf("checkLink(%q) = %v, want ok %v", tt.url, err, tt.ok)
			}
		})
	}
}

func TestSignInPage(t *testing.T) {
	link := ipc.DashboardLink{URL: "http://127.0.0.1:7247/signin?code=" + localhttp.NewToken(), ExpiresInS: 120}
	now := time.Now()
	tests := []struct {
		name  string
		check func(t *testing.T, dir string)
	}{
		{
			name: "each run writes a page of its own, which sends the browser on and links to the same place",
			check: func(t *testing.T, dir string) {
				a, err := writeSignInPage(dir, link, now)
				if err != nil {
					t.Fatal(err)
				}
				b, err := writeSignInPage(dir, link, now)
				if err != nil {
					t.Fatal(err)
				}
				if a == b {
					t.Errorf("two runs wrote the same page %s", a)
				}
				page, err := os.ReadFile(a)
				if err != nil {
					t.Fatal(err)
				}
				if !strings.HasPrefix(string(page), "<!doctype html>") || strings.Count(string(page), link.URL) != 2 {
					t.Errorf("the page doesn't refresh to and link the URL once each:\n%s", page)
				}
			},
		},
		{
			name: "only you can read the page",
			check: func(t *testing.T, dir string) {
				if runtime.GOOS == "windows" {
					t.Skip("on Windows the data directory's DACL, which files inherit, keeps others out")
				}
				p, err := writeSignInPage(dir, link, now)
				if err != nil {
					t.Fatal(err)
				}
				fi, err := os.Stat(p)
				if err != nil {
					t.Fatal(err)
				}
				if fi.Mode().Perm() != 0o600 {
					t.Errorf("mode %v, want 0600", fi.Mode().Perm())
				}
			},
		},
		{
			name: "a run deletes the pages older than signInKeep and leaves newer pages and other files",
			check: func(t *testing.T, dir string) {
				write := func(name string, age time.Duration) string {
					p := filepath.Join(dir, name)
					if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
						t.Fatal(err)
					}
					if err := os.Chtimes(p, now.Add(-age), now.Add(-age)); err != nil {
						t.Fatal(err)
					}
					return p
				}
				old := write(signInPrefix+"0000000000000000"+signInSuffix, signInKeep+time.Minute)
				recent := write(signInPrefix+"1111111111111111"+signInSuffix, time.Minute)
				other := write("signin-notes.txt", time.Hour)
				if _, err := writeSignInPage(dir, link, now); err != nil {
					t.Fatal(err)
				}
				if _, err := os.Stat(old); !errors.Is(err, os.ErrNotExist) {
					t.Errorf("the old page is still there (%v)", err)
				}
				for _, p := range []string{recent, other} {
					if _, err := os.Stat(p); err != nil {
						t.Errorf("%s is gone: %v", p, err)
					}
				}
			},
		},
		{
			name: "a page that can't be written is an error, and leaves nothing behind",
			check: func(t *testing.T, dir string) {
				missing := filepath.Join(dir, "nowhere")
				if _, err := writeSignInPage(missing, link, now); err == nil {
					t.Error("writing into a directory that doesn't exist worked")
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.check(t, t.TempDir())
		})
	}
}

func TestAgentRunning(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	me := filepath.Base(exe)
	tests := []struct {
		name  string
		pid   int // port.json's; -1 for no port.json
		procs []platform.Proc
		err   error // from reading the process table
		want  bool
	}{
		{name: "port.json naming a live process of this program is a running agent", pid: 4242, procs: []platform.Proc{{PID: 4242, Name: me}}, want: true},
		{name: "a pid another program has since is no agent", pid: 4242, procs: []platform.Proc{{PID: 4242, Name: "firefox"}}},
		{name: "a pid no process has is no agent", pid: 4242, procs: []platform.Proc{{PID: 1, Name: me}}},
		{name: "an agent that stopped cleanly wrote pid 0", pid: 0, procs: []platform.Proc{{PID: 0, Name: me}}},
		{name: "no port.json is no agent", pid: -1, procs: []platform.Proc{{PID: 4242, Name: me}}},
		{name: "this process is never taken for the agent", pid: 7, procs: []platform.Proc{{PID: 7, Name: me}}},
		{name: "a process table that can't be read says nothing", pid: 4242, err: errors.New("no /proc")},
		{name: "the name matches case aside on Windows only", pid: 4242, procs: []platform.Proc{{PID: 4242, Name: strings.ToUpper(me)}}, want: runtime.GOOS == "windows" && strings.ToUpper(me) != me},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dirs := home.Dirs{Data: t.TempDir()}
			if tt.pid >= 0 {
				if err := localhttp.WritePortFile(dirs.PortFile(), localhttp.PortFile{Port: 7247, Token: localhttp.NewToken(), PID: tt.pid}); err != nil {
					t.Fatal(err)
				}
			}
			procs := func() ([]platform.Proc, error) { return tt.procs, tt.err }
			if _, got := agentRunning(dirs, procs, 7); got != tt.want {
				t.Errorf("agentRunning = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestWords(t *testing.T) {
	tests := []struct {
		name string
		d    time.Duration
		want string
	}{
		{name: "whole minutes are minutes", d: 2 * time.Minute, want: "2 minutes"},
		{name: "one minute is singular", d: time.Minute, want: "1 minute"},
		{name: "anything else is seconds", d: 90 * time.Second, want: "90 seconds"},
		{name: "one second is singular", d: time.Second, want: "1 second"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := words(tt.d); got != tt.want {
				t.Errorf("words(%s) = %q, want %q", tt.d, got, tt.want)
			}
		})
	}
}
