// SPDX-License-Identifier: AGPL-3.0-or-later

package pages

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/internal/server/dashboard"
	"github.com/some-du6e/pairtime/pkg/projectname"
)

// hideCall records what HideName was asked.
type hideCall struct{ account, project string }

// hideSite serves the owner's dashboard as ownerSite does, with a
// HideName that records its calls and answers err; noHide leaves HideName
// nil.
func hideSite(t *testing.T, in SignIn, account string, v dashboard.View, sameOrigin, noHide bool, err error) (*http.ServeMux, *[]hideCall) {
	t.Helper()
	var calls []hideCall
	d := Dashboard{
		Source: fakeSource{v: v},
		Account: func(*http.Request) (string, error) {
			switch account {
			case "":
				return "", dashboard.ErrNotSignedIn
			case unreadable:
				return "", errors.New("database is locked")
			}
			return account, nil
		},
		SetZone:    func(context.Context, string, string) error { return nil },
		SameOrigin: func(*http.Request) bool { return sameOrigin },
		Now:        func() time.Time { return demoClock },
		Logger:     slog.New(slog.DiscardHandler),
	}
	if !noHide {
		d.HideName = func(_ context.Context, a, p string) error {
			calls = append(calls, hideCall{a, p})
			return err
		}
	}
	mux := http.NewServeMux()
	newSite(t, Options{}).RegisterDashboard(mux, in, d)
	return mux, &calls
}

// namedView is the sample's numbers as an owner's view with short codes,
// and the owner's name turned on for its first project, chip8-emulator.
func namedView(t *testing.T) dashboard.View {
	t.Helper()
	v := ownerView(t)
	keys := []string{"g7c41e9a2d05b8f63e1a94c7d2b6f0e85", "gd2a8f61c94e07b35a8c1f6e29d4b7a03", "g5be03d7f18a6c92e4b07d5a1c8f3e962"}
	codes := projectname.ShortCodes(keys)
	for i := range v.Projects {
		p := &v.Projects[i]
		p.Keys, p.Code, p.Name = []string{keys[i]}, codes[keys[i]], projectname.CodeName(codes[keys[i]])
	}
	v.Projects[0].Name, v.Projects[0].Named = "chip8-emulator", true
	return v
}

const hideButton = `<form class="hide" method="post" action="/dashboard/projects/hide"><input type="hidden" name="project" value="p_sample_chip8"><button type="submit">Hide this name</button></form>`

func TestNamedProjects(t *testing.T) {
	tests := []struct {
		name    string
		page    func(t *testing.T) string
		section string
		want    []string
		notWant []string
	}{
		{
			name: "a project its owner named is called by the name, with its code's name in a title, and the owner can hide it",
			page: func(t *testing.T) string {
				mux, _ := hideSite(t, signInOn("Robin"), "u1", namedView(t), true, false, nil)
				_, page := serve(mux, httptest.NewRequest(http.MethodGet, "/dashboard", nil))
				return page
			},
			section: "projects",
			want:    []string{`<tr><td title="Project 7c41e9"><span class="key c1"></span>chip8-emulator` + hideButton + `</td>`},
		},
		{
			name: "a project called by its code has no hide button, since there is no name to hide",
			page: func(t *testing.T) string {
				mux, _ := hideSite(t, signInOn("Robin"), "u1", namedView(t), true, false, nil)
				_, page := serve(mux, httptest.NewRequest(http.MethodGet, "/dashboard", nil))
				return page
			},
			section: "projects",
			want:    []string{`<tr><td title="gd2a8f61c94e07b35a8c1f6e29d4b7a03"><span class="key c2"></span>Project d2a8f6</td>`},
		},
		{
			name: "today's clock of a named project shows the name, with its code's name in a title",
			page: func(t *testing.T) string {
				mux, _ := hideSite(t, signInOn("Robin"), "u1", namedView(t), true, false, nil)
				_, page := serve(mux, httptest.NewRequest(http.MethodGet, "/dashboard", nil))
				return page
			},
			section: "today",
			want:    []string{`<p class="clock-name" title="Project 7c41e9"><span class="key c1"></span>chip8-emulator</p>`},
		},
		{
			name: "a server that can't hide a name offers no button",
			page: func(t *testing.T) string {
				mux, _ := hideSite(t, signInOn("Robin"), "u1", namedView(t), true, true, nil)
				_, page := serve(mux, httptest.NewRequest(http.MethodGet, "/dashboard", nil))
				return page
			},
			section: "projects",
			notWant: []string{`class="hide"`},
		},
		{
			name: "the sample never offers to hide a name",
			page: func(t *testing.T) string {
				_, page := getDemo(t, Options{}, signInOn("Robin"))
				return page
			},
			notWant: []string{`class="hide"`, "Hide this name"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			page := tt.page(t)
			if tt.section != "" {
				page = sectionOf(page, tt.section)
			}
			for _, w := range tt.want {
				if !strings.Contains(page, w) {
					t.Errorf("the page doesn't contain %q:\n%s", w, page)
				}
			}
			for _, w := range tt.notWant {
				if strings.Contains(page, w) {
					t.Errorf("the page contains %q", w)
				}
			}
		})
	}
}

func TestHideForm(t *testing.T) {
	post := func(form string) *http.Request {
		r := httptest.NewRequest(http.MethodPost, HidePath, strings.NewReader(form))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		return r
	}
	tests := []struct {
		name         string
		signIn       SignIn
		account      string
		sameOrigin   bool
		noHide       bool
		hideErr      error
		form         string
		wantCode     int
		wantLocation string
		wantCalls    []hideCall
	}{
		{
			name:   "a hide sent from the owner's dashboard deletes that project's name for their account, and the browser goes back to the dashboard",
			signIn: signInOn("Robin"), account: "u1", sameOrigin: true, form: "project=p_sample_chip8",
			wantCode: 303, wantLocation: "/dashboard", wantCalls: []hideCall{{"u1", "p_sample_chip8"}},
		},
		{
			name:   "a hide that doesn't come from this server's pages is refused, and nothing is hidden",
			signIn: signInOn("Robin"), account: "u1", sameOrigin: false, form: "project=p_sample_chip8",
			wantCode: 403,
		},
		{
			name:   "a hide that isn't signed in goes to sign-in, and nothing is hidden",
			signIn: signInOn(""), sameOrigin: true, form: "project=p_sample_chip8",
			wantCode: 303, wantLocation: "/auth/hca/login?return_to=%2Fdashboard",
		},
		{
			name:   "a hide whose session can't be read is an error, not a trip to sign-in, and nothing is hidden",
			signIn: signInOn("Robin"), account: unreadable, sameOrigin: true, form: "project=p_sample_chip8",
			wantCode: 500,
		},
		{
			name:   "a hide that names no project is refused",
			signIn: signInOn("Robin"), account: "u1", sameOrigin: true, form: "project=",
			wantCode: 400,
		},
		{
			name:   "a hide that doesn't save is an error",
			signIn: signInOn("Robin"), account: "u1", sameOrigin: true, form: "project=p_sample_chip8", hideErr: context.DeadlineExceeded,
			wantCode: 500, wantCalls: []hideCall{{"u1", "p_sample_chip8"}},
		},
		{
			name:   "with sign-in off, or a server that can't hide names, the form hides nothing and goes back to the dashboard",
			signIn: SignIn{}, sameOrigin: true, form: "project=p_sample_chip8",
			wantCode: 303, wantLocation: "/dashboard",
		},
		{
			name:   "a server without HideName hides nothing and goes back to the dashboard",
			signIn: signInOn("Robin"), account: "u1", sameOrigin: true, noHide: true, form: "project=p_sample_chip8",
			wantCode: 303, wantLocation: "/dashboard",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mux, calls := hideSite(t, tt.signIn, tt.account, namedView(t), tt.sameOrigin, tt.noHide, tt.hideErr)
			resp, _ := serve(mux, post(tt.form))
			if resp.StatusCode != tt.wantCode {
				t.Fatalf("POST %s = %d, want %d", HidePath, resp.StatusCode, tt.wantCode)
			}
			if got := resp.Header.Get("Location"); got != tt.wantLocation {
				t.Errorf("Location = %q, want %q", got, tt.wantLocation)
			}
			if !reflect.DeepEqual(*calls, tt.wantCalls) {
				t.Errorf("HideName calls %v, want %v", *calls, tt.wantCalls)
			}
		})
	}
}
