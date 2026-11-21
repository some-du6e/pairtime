// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/internal/server/ops"
)

func TestCheckPushURL(t *testing.T) {
	const token = "s3cr3t-push-token"
	tests := []struct {
		name    string
		url     string
		wantErr string
	}{
		{name: "an https URL is a push monitor, as a Healthchecks.io check is", url: "https://hc-ping.com/" + token},
		{name: "a plain http URL on the LAN is one too, as the pilot's Uptime Kuma monitors are",
			url: "http://192.168.1.10:3001/api/push/" + token + "?status=up&msg=OK&ping="},
		{name: "a URL with a user is refused", url: "https://user:" + token + "@example.org/ping", wantErr: "no user"},
		{name: "another scheme is refused", url: "ftp://example.org/" + token, wantErr: "http or https"},
		{name: "text that isn't a URL with a host is refused", url: token, wantErr: "isn't a URL"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := checkPushURL("PAIRTIME_HEARTBEAT_URL", tt.url)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error = %v, want it to say %q", err, tt.wantErr)
			}
			if strings.Contains(err.Error(), token) {
				t.Errorf("the error quotes the URL's secret part: %v", err)
			}
		})
	}
}

func TestDistinctPushURLs(t *testing.T) {
	tests := []struct {
		name    string
		urls    map[string]string
		wantErr string
	}{
		{name: "three monitors of their own pass", urls: map[string]string{
			envHeartbeatURL: "http://kuma/api/push/a", envSnapshotHeartbeatURL: "http://kuma/api/push/b", envLitestreamHeartbeatURL: "http://kuma/api/push/c"}},
		{name: "unset monitors don't count", urls: map[string]string{envHeartbeatURL: "http://kuma/api/push/a"}},
		{name: "two parts sharing one monitor fail, naming both", urls: map[string]string{
			envHeartbeatURL: "http://kuma/api/push/a", envLitestreamHeartbeatURL: "http://kuma/api/push/a"},
			wantErr: envHeartbeatURL + " and " + envLitestreamHeartbeatURL},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := distinctPushURLs(func(name string) string { return tt.urls[name] })
			if tt.wantErr == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error = %v, want it to say %q", err, tt.wantErr)
			}
		})
	}
}

func TestNextSnapshot(t *testing.T) {
	day := time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC)
	tests := []struct {
		name string
		now  time.Time
		want time.Time
	}{
		{name: "before 03:10 UTC the snapshot is that day's", now: day.Add(2 * time.Hour), want: day.Add(snapshotAt)},
		{name: "at 03:10 UTC exactly the next is a day later", now: day.Add(snapshotAt), want: day.Add(24*time.Hour + snapshotAt)},
		{name: "late in the day the next is tomorrow's", now: day.Add(23 * time.Hour), want: day.Add(24*time.Hour + snapshotAt)},
		{name: "a clock in a zone whose date isn't UTC's still means 03:10 UTC",
			now: time.Date(2026, 11, 20, 1, 0, 0, 0, time.FixedZone("-05", -5*3600)), want: day.Add(24*time.Hour + snapshotAt)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := nextSnapshot(tt.now); !got.Equal(tt.want) {
				t.Errorf("nextSnapshot(%s) = %s, want %s", tt.now, got, tt.want)
			}
		})
	}
}

func TestFirstSnapshotWait(t *testing.T) {
	day := time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC)
	at := func(h, m int) time.Time { return day.Add(time.Duration(h)*time.Hour + time.Duration(m)*time.Minute) }
	tests := []struct {
		name        string
		now, newest time.Time
		want        time.Duration
	}{
		{name: "with no snapshot yet one runs soon after start", now: at(2, 0), want: snapshotStartDelay},
		{name: "a newest snapshot over a day old means a night was missed, so one runs soon", now: at(2, 0), newest: at(2, 0).Add(-30 * time.Hour), want: snapshotStartDelay},
		{name: "a snapshot from after the last 03:10 UTC means the next waits for the next 03:10", now: at(2, 0), newest: at(0, 0), want: 70 * time.Minute},
		{name: "a server back within an hour of a 03:10 it missed takes that night's snapshot soon, though the newest is under 25 hours old",
			now: at(4, 0), newest: at(3, 12).Add(-24 * time.Hour), want: snapshotStartDelay},
		{name: "a server that restarted during the 03:10 snapshot takes it again soon", now: at(3, 15), newest: at(3, 11).Add(-24 * time.Hour), want: snapshotStartDelay},
		{name: "a snapshot taken by hand after the last 03:10 stands in for that night's", now: at(20, 0), newest: at(15, 0), want: 7*time.Hour + 10*time.Minute},
		{name: "last night's snapshot, a few minutes after its 03:10, means the next waits for tonight's", now: at(20, 0), newest: at(3, 12), want: 7*time.Hour + 10*time.Minute},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := firstSnapshotWait(tt.now, tt.newest); got != tt.want {
				t.Errorf("firstSnapshotWait(%s, %s) = %s, want %s", tt.now, tt.newest, got, tt.want)
			}
		})
	}
}

// monitor is a fake push monitor that records each ping.
type monitor struct {
	*httptest.Server
	mu     sync.Mutex
	pings  map[string]int
	agents []string
	// answer, when set, writes the answer instead of 200 OK.
	answer func(w http.ResponseWriter)
}

func newMonitor(t *testing.T) *monitor {
	t.Helper()
	m := &monitor{pings: make(map[string]int)}
	m.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		m.mu.Lock()
		m.pings[r.URL.Path]++
		m.agents = append(m.agents, r.UserAgent())
		answer := m.answer
		m.mu.Unlock()
		if answer != nil {
			answer(w)
			return
		}
		w.Write([]byte("OK"))
	}))
	t.Cleanup(m.Close)
	return m
}

func (m *monitor) count(path string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.pings[path]
}

func TestHeartbeat(t *testing.T) {
	pass := ops.Check{Name: "database", Run: func(context.Context) error { return nil }}
	fail := ops.Check{Name: "replication", Run: func(context.Context) error { return errors.New("behind") }}
	tests := []struct {
		name      string
		checks    []ops.Check
		answer    func(w http.ResponseWriter)
		wantPings int
		wantErr   string
	}{
		{name: "when every check passes the monitor gets one ping", checks: []ops.Check{pass}, wantPings: 1},
		{name: "when a check fails no ping is sent, so the monitor alerts", checks: []ops.Check{pass, fail}, wantPings: 0},
		{name: "a monitor that answers 500 is an error naming its host, not its path", checks: []ops.Check{pass},
			answer: func(w http.ResponseWriter) { w.WriteHeader(http.StatusInternalServerError) }, wantPings: 1, wantErr: "answered the ping with 500"},
		{name: "a Cloudflare challenge says so", checks: []ops.Check{pass},
			answer: func(w http.ResponseWriter) {
				w.Header().Set("Cf-Mitigated", "challenge")
				w.WriteHeader(http.StatusForbidden)
			}, wantPings: 1, wantErr: "Bot Fight Mode"},
		{name: "a redirect isn't followed", checks: []ops.Check{pass},
			answer: func(w http.ResponseWriter) {
				w.Header().Set("Location", "https://elsewhere.example/ping")
				w.WriteHeader(http.StatusFound)
			}, wantPings: 1, wantErr: "302"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := newMonitor(t)
			m.answer = tt.answer
			raw := m.URL + "/api/push/the-token?status=up"
			res, err := heartbeat(context.Background(), pushClient(), raw, tt.checks)
			if got := m.count("/api/push/the-token"); got != tt.wantPings {
				t.Errorf("%d pings, want %d", got, tt.wantPings)
			}
			if res.Ready != (len(tt.checks) == 1) {
				t.Errorf("ready = %v", res.Ready)
			}
			if tt.wantErr == "" {
				if err != nil {
					t.Fatal(err)
				}
			} else {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("error = %v, want it to say %q", err, tt.wantErr)
				}
				if strings.Contains(err.Error(), "the-token") {
					t.Errorf("the error quotes the monitor's URL: %v", err)
				}
			}
			for _, a := range m.agents {
				if !strings.HasPrefix(a, "pairtimed/") || !strings.Contains(a, "(+https://github.com/some-du6e/pairtime)") {
					t.Errorf("User-Agent %q isn't pairtimed's descriptive one", a)
				}
			}
		})
	}
}
