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
)

// takeCall records what a take-down was asked: which form, for which
// account and id.
type takeCall struct{ path, account, id string }

// publishedSite serves the owner's dashboard as ownerSite does, in time
// zone tz, with p as what the account published, or readErr when reading
// it fails, and take-downs that record their calls and answer takeErr. off
// leaves the list's three hooks nil.
func publishedSite(t *testing.T, in SignIn, account, tz string, p Published, readErr error, sameOrigin, off bool, takeErr error) (*http.ServeMux, *[]takeCall) {
	t.Helper()
	var calls []takeCall
	v := ownerView(t)
	v.TZ = tz
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
	if !off {
		d.Published = func(context.Context, string) (Published, error) { return p, readErr }
		d.DeleteDevlog = func(_ context.Context, a, id string) error {
			calls = append(calls, takeCall{DeleteDevlogPath, a, id})
			return takeErr
		}
		d.RemoveBadge = func(_ context.Context, a, id string) error {
			calls = append(calls, takeCall{RemoveBadgePath, a, id})
			return takeErr
		}
	}
	mux := http.NewServeMux()
	newSite(t, Options{}).RegisterDashboard(mux, in, d)
	return mux, &calls
}

// day3 is a devlog its owner's agent drafted, received at 23:30 UTC on 20
// November, which is 21 November in Tokyo.
var day3 = PublishedDevlog{ID: "dl_1", Project: "Project 7c41e9", Markdown: "# Day 3\nFixed the nil map.", AIAssisted: true,
	ReceivedAt: time.Date(2026, 11, 20, 23, 30, 0, 0, time.UTC)}

// devlogItem is the list's line of a devlog with that title, meta and id.
func devlogItem(title, meta, id string) string {
	return `<li><p class="item"><strong>` + title + `</strong> <span class="meta">` + meta + `</span></p><details class="take"><summary>Delete</summary><form method="post" action="/dashboard/devlogs/delete"><input type="hidden" name="id" value="` + id + `"><button type="submit">Delete this devlog</button></form></details></li>`
}

// badgeItem is the list's line of a badge with that name and id.
func badgeItem(name, id string) string {
	return `<li><p class="item"><strong>` + name + `</strong></p><details class="take"><summary>Remove</summary><form method="post" action="/dashboard/badges/remove"><input type="hidden" name="id" value="` + id + `"><button type="submit">Remove this badge</button></form></details></li>`
}

func TestPublishedList(t *testing.T) {
	rlo, pdf := string(rune(0x202e)), string(rune(0x202c))
	tests := []struct {
		name     string
		tz       string
		p        Published
		readErr  error
		off      bool
		wantCode int
		// want and notWant are looked for in the list's section, or in
		// the whole page when page is set.
		want, notWant []string
		page          bool
	}{
		{
			name: "a devlog shows its title, its project, the date it arrived in the account's time zone and whether the agent drafted it, with its delete behind a second click",
			tz:   "Asia/Tokyo", p: Published{Devlogs: []PublishedDevlog{day3}},
			want: []string{devlogItem("Day 3", "Project 7c41e9, 21 November 2026, drafted by your agent", "dl_1")},
		},
		{
			name: "a devlog the teen wrote alone says nothing of the agent",
			tz:   "UTC", p: Published{Devlogs: []PublishedDevlog{{ID: "dl_2", Project: "Project d2a8f6", Markdown: "Week one", ReceivedAt: day3.ReceivedAt}}},
			want: []string{devlogItem("Week one", "Project d2a8f6, 20 November 2026", "dl_2")},
		},
		{
			name: "a devlog's title is its first line with text, without heading marks or bidirectional controls",
			tz:   "UTC", p: Published{Devlogs: []PublishedDevlog{{ID: "dl_3", Project: "Project d2a8f6", Markdown: "\n  \n## " + rlo + "Saved" + pdf + " the  game\r\nrest", ReceivedAt: day3.ReceivedAt}}},
			want:    []string{"<strong>Saved the game</strong>"},
			notWant: []string{rlo, pdf},
		},
		{
			name: "a devlog with no text is called Untitled devlog",
			tz:   "UTC", p: Published{Devlogs: []PublishedDevlog{{ID: "dl_4", Project: "Project d2a8f6", Markdown: "#\n   \n", ReceivedAt: day3.ReceivedAt}}},
			want: []string{"<strong>Untitled devlog</strong>"},
		},
		{
			name: "a long title stops at 64 characters",
			tz:   "UTC", p: Published{Devlogs: []PublishedDevlog{{ID: "dl_5", Project: "Project d2a8f6", Markdown: strings.Repeat("a", 100), ReceivedAt: day3.ReceivedAt}}},
			want:    []string{"<strong>" + strings.Repeat("a", 64) + "</strong>"},
			notWant: []string{strings.Repeat("a", 65)},
		},
		{
			name: "a devlog's text is escaped, never markup",
			tz:   "UTC", p: Published{Devlogs: []PublishedDevlog{{ID: "dl_6", Project: "Project d2a8f6", Markdown: "<script>x</script> & more", ReceivedAt: day3.ReceivedAt}}},
			want:    []string{"<strong>&lt;script&gt;x&lt;/script&gt; &amp; more</strong>"},
			notWant: []string{"<script>x"},
		},
		{
			name: "a badge shows its name, with its removal behind a second click",
			tz:   "UTC", p: Published{Badges: []PublishedBadge{{ID: "b_1", Code: "long_fight"}}},
			want: []string{badgeItem("Won a long fight", "b_1")},
		},
		{
			name: "a badge code the list has no name for shows as itself",
			tz:   "UTC", p: Published{Badges: []PublishedBadge{{ID: "b_2", Code: "future_code"}}},
			want: []string{badgeItem("future_code", "b_2")},
		},
		{
			name: "with nothing published the list says so for devlogs and badges",
			tz:   "UTC",
			want: []string{`<p class="note">You haven&#39;t published a devlog.</p>`, `<p class="note">You don&#39;t show any badges.</p>`},
		},
		{
			name: "the list says how long the id of what was taken down is kept, from the retention schedule",
			tz:   "UTC",
			want: []string{"pairtime keeps only its ID, for 30 days, so a computer of yours that missed the change can&#39;t publish it again."},
		},
		{
			name: "a server that offers no list shows none",
			tz:   "UTC", off: true, page: true,
			notWant: []string{`<section aria-labelledby="published">`, DeleteDevlogPath, RemoveBadgePath},
		},
		{
			name: "a list that can't be read is an error, as a view that can't be read is",
			tz:   "UTC", readErr: errors.New("database is locked"), wantCode: http.StatusInternalServerError,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mux, _ := publishedSite(t, signInOn("Robin"), "u1", tt.tz, tt.p, tt.readErr, true, tt.off, nil)
			resp, page := serve(mux, httptest.NewRequest(http.MethodGet, "/dashboard", nil))
			want := tt.wantCode
			if want == 0 {
				want = http.StatusOK
			}
			if resp.StatusCode != want {
				t.Fatalf("GET /dashboard = %d, want %d", resp.StatusCode, want)
			}
			if want != http.StatusOK {
				return
			}
			if !tt.page {
				page = sectionOf(page, "published")
				if page == "" {
					t.Fatal("the dashboard has no list of what its owner published")
				}
			}
			for _, w := range tt.want {
				if !strings.Contains(page, w) {
					t.Errorf("the list doesn't contain %q:\n%s", w, page)
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

// TestPublishedListNotOnSample checks that the sample dashboard, which is
// nobody's, offers nothing to take down.
func TestPublishedListNotOnSample(t *testing.T) {
	tests := []struct {
		name   string
		signIn SignIn
	}{
		{name: "the sample a visitor sees has no list", signIn: signInOn("")},
		{name: "the sample a signed-in teen sees has no list either", signIn: signInOn("Robin")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, page := getDemo(t, Options{}, tt.signIn)
			for _, w := range []string{`<section aria-labelledby="published">`, DeleteDevlogPath, RemoveBadgePath} {
				if strings.Contains(page, w) {
					t.Errorf("the sample contains %q", w)
				}
			}
		})
	}
}

func TestTakeDownForms(t *testing.T) {
	post := func(path, form string) *http.Request {
		r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		return r
	}
	tests := []struct {
		name         string
		signIn       SignIn
		account      string
		sameOrigin   bool
		off          bool
		takeErr      error
		path, form   string
		wantCode     int
		wantLocation string
		wantCalls    []takeCall
	}{
		{
			name:   "a delete sent from the owner's dashboard takes that devlog down for their account, and the browser goes back to the list",
			signIn: signInOn("Robin"), account: "u1", sameOrigin: true, path: DeleteDevlogPath, form: "id=dl_1",
			wantCode: 303, wantLocation: "/dashboard#published", wantCalls: []takeCall{{DeleteDevlogPath, "u1", "dl_1"}},
		},
		{
			name:   "a removal sent from the owner's dashboard takes that badge down for their account",
			signIn: signInOn("Robin"), account: "u1", sameOrigin: true, path: RemoveBadgePath, form: "id=b_1",
			wantCode: 303, wantLocation: "/dashboard#published", wantCalls: []takeCall{{RemoveBadgePath, "u1", "b_1"}},
		},
		{
			name:   "a delete that doesn't come from this server's pages is refused, and nothing goes",
			signIn: signInOn("Robin"), account: "u1", sameOrigin: false, path: DeleteDevlogPath, form: "id=dl_1",
			wantCode: 403,
		},
		{
			name:   "a removal that doesn't come from this server's pages is refused, and nothing goes",
			signIn: signInOn("Robin"), account: "u1", sameOrigin: false, path: RemoveBadgePath, form: "id=b_1",
			wantCode: 403,
		},
		{
			name:   "a take-down that isn't signed in goes to sign-in, and nothing goes",
			signIn: signInOn(""), sameOrigin: true, path: DeleteDevlogPath, form: "id=dl_1",
			wantCode: 303, wantLocation: "/auth/hca/login?return_to=%2Fdashboard",
		},
		{
			name:   "a take-down whose session can't be read is an error, not a trip to sign-in, and nothing goes",
			signIn: signInOn("Robin"), account: unreadable, sameOrigin: true, path: RemoveBadgePath, form: "id=b_1",
			wantCode: 500,
		},
		{
			name:   "a take-down that names no id is refused",
			signIn: signInOn("Robin"), account: "u1", sameOrigin: true, path: DeleteDevlogPath, form: "id=",
			wantCode: 400,
		},
		{
			name:   "a take-down whose id is longer than any key is refused",
			signIn: signInOn("Robin"), account: "u1", sameOrigin: true, path: DeleteDevlogPath, form: "id=" + strings.Repeat("k", 129),
			wantCode: 400,
		},
		{
			name:   "a take-down that doesn't save is an error",
			signIn: signInOn("Robin"), account: "u1", sameOrigin: true, path: DeleteDevlogPath, form: "id=dl_1", takeErr: context.DeadlineExceeded,
			wantCode: 500, wantCalls: []takeCall{{DeleteDevlogPath, "u1", "dl_1"}},
		},
		{
			name:   "with sign-in off nothing goes, and the browser goes back to the dashboard",
			signIn: SignIn{}, sameOrigin: true, path: DeleteDevlogPath, form: "id=dl_1",
			wantCode: 303, wantLocation: "/dashboard",
		},
		{
			name:   "a server that offers no list takes nothing down and goes back to the dashboard",
			signIn: signInOn("Robin"), account: "u1", sameOrigin: true, off: true, path: RemoveBadgePath, form: "id=b_1",
			wantCode: 303, wantLocation: "/dashboard",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mux, calls := publishedSite(t, tt.signIn, tt.account, "UTC", Published{}, nil, tt.sameOrigin, tt.off, tt.takeErr)
			resp, _ := serve(mux, post(tt.path, tt.form))
			if resp.StatusCode != tt.wantCode {
				t.Fatalf("POST %s = %d, want %d", tt.path, resp.StatusCode, tt.wantCode)
			}
			if got := resp.Header.Get("Location"); got != tt.wantLocation {
				t.Errorf("Location = %q, want %q", got, tt.wantLocation)
			}
			if !reflect.DeepEqual(*calls, tt.wantCalls) {
				t.Errorf("take-down calls %v, want %v", *calls, tt.wantCalls)
			}
		})
	}
}
