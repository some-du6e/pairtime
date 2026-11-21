// SPDX-License-Identifier: AGPL-3.0-or-later

package pages

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
)

// signInOn is sign-in as pairtimed wires it, with every request signed in
// as handle, or none signed in when handle is "".
func signInOn(handle string) SignIn {
	return SignIn{
		Enabled:    true,
		LoginPath:  "/auth/hca/login",
		LogoutPath: "/auth/logout",
		Visitor:    func(*http.Request) (string, bool) { return handle, handle != "" },
	}
}

// getHome serves GET / from a Site built from o with sign-in wired as in,
// and returns the response and its body.
func getHome(t *testing.T, o Options, in SignIn) (*http.Response, string) {
	t.Helper()
	mux := http.NewServeMux()
	newSite(t, o).RegisterHome(mux, in)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	resp := rec.Result()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp, string(body)
}

func TestHome(t *testing.T) {
	signInButton := `<a class="button" href="/auth/hca/login">Sign in with Hack Club</a>`
	signOutForm := `<form method="post" action="/auth/logout"><button type="submit">Sign out</button></form>`
	tests := []struct {
		name    string
		opts    Options
		signIn  SignIn
		want    []string // substrings of the page
		notWant []string
	}{
		{
			name:   "the page says pairtime counts the time you spend building with AI, not the agent's, and that Hack Club programs can use it",
			signIn: signInOn(""),
			want: []string{
				`<h1 id="pairtime-counts-the-time-you-spend-building-with-ai">pairtime counts the time you spend building with AI</h1>`,
				"<p>Not the time your AI spends.</p>",
				"The time your agent works on its own doesn&#39;t, however long it runs.",
				"When you ship a project to a Hack Club program, the program can use the hours pairtime counted.",
			},
		},
		{
			name:   "the page says how it counts: your own acts through hooks, your presence, levels A, B and C, and what you type staying on your computer",
			signIn: signInOn(""),
			want: []string{
				"hooks in your AI tools tell it each time you send a prompt or answer your agent.",
				"A second counts only when something you did started it, your computer saw you there, and that project was in front.",
				`<a href="/docs/evidence">evidence level</a>: A when your keyboard backs it up, B when your computer saw you but can&#39;t tell whether you were typing, and C for time`,
				"What you type stays on your computer, apart from a few prompts you look over first if you submit hours to a program.",
			},
		},
		{
			name:   "the page links the privacy notice, the evidence levels, and the source on GitHub with its license",
			signIn: signInOn(""),
			want: []string{
				`<a href="/privacy">Privacy notice</a>`,
				`<a href="/docs/evidence">Evidence levels</a>`,
				`<a href="https://github.com/some-du6e/pairtime">Source code on GitHub</a>. pairtime is free software under the AGPL-3.0 license`,
			},
		},
		{
			name:    "while sign-ups are closed the page says it's an early pilot with testers by invitation, and makes accounts only for the people who run the server",
			signIn:  signInOn(""),
			want:    []string{"<strong>Early pilot.</strong> Sign-ups are closed for now, and testers join by invitation. Until sign-ups open, this server makes accounts only for the people who run it.</p>"},
			notWant: []string{"Sign-ups on this server are open"},
		},
		{
			name:    "with sign-ups open the page says so and invites no one in particular",
			opts:    Options{SignupsOpen: true},
			signIn:  signInOn(""),
			want:    []string{"<strong>Early pilot.</strong> Sign-ups on this server are open. Sign in with Hack Club to make your account.</p>"},
			notWant: []string{"testers join by invitation", "makes accounts only for"},
		},
		{
			name:    "with sign-in set up, a visitor who isn't signed in gets the Sign in with Hack Club button and no sign-out form",
			signIn:  signInOn(""),
			want:    []string{signInButton},
			notWant: []string{"Signed in", "<form"},
		},
		{
			name:    "without sign-in set up, the page offers no sign-in and says it isn't set up",
			signIn:  SignIn{},
			want:    []string{"Sign-ups are closed for now, and testers join by invitation. Sign-in isn&#39;t set up on this server yet.</p>"},
			notWant: []string{"Sign in with Hack Club", `class="account"`, "<form", "makes accounts only for"},
		},
		{
			name:    "without sign-in set up and sign-ups open, the page still says sign-in isn't set up",
			opts:    Options{SignupsOpen: true},
			signIn:  SignIn{},
			want:    []string{"Sign-ups on this server are open. Sign-in isn&#39;t set up on this server yet, though.</p>"},
			notWant: []string{"Sign in with Hack Club"},
		},
		{
			name: "without sign-in set up, no request counts as signed in, whatever the visitor function says",
			signIn: SignIn{LoginPath: "/auth/hca/login", LogoutPath: "/auth/logout",
				Visitor: func(*http.Request) (string, bool) { return "U0NOTSHOWN", true }},
			notWant: []string{"U0NOTSHOWN", "Signed in", "<form"},
		},
		{
			name:    "a signed-in visitor sees who they're signed in as and a sign-out button that posts to /auth/logout",
			signIn:  signInOn("U0FAKEVERIFIED"),
			want:    []string{"<p>Signed in as <strong>U0FAKEVERIFIED</strong></p>", signOutForm},
			notWant: []string{signInButton},
		},
		{
			name:    "a handle is shown as text, never as markup",
			signIn:  signInOn(`<script>alert(1)</script>`),
			want:    []string{"Signed in as <strong>&lt;script&gt;alert(1)&lt;/script&gt;</strong>"},
			notWant: []string{"<script>"},
		},
		{
			name:   "the page links the sample dashboard at /demo",
			signIn: signInOn(""),
			want:   []string{`<p><a href="/demo">See a sample dashboard</a></p>`},
		},
		{
			name:   "the browser tab says pairtime, and link previews get a description",
			signIn: signInOn(""),
			want: []string{
				"<title>pairtime</title>",
				`<meta name="description" content="pairtime counts the time you spend building with AI, not the time your AI spends on its own.">`,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, page := getHome(t, tt.opts, tt.signIn)
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("GET / = %d, want 200", resp.StatusCode)
			}
			for _, w := range tt.want {
				if !strings.Contains(page, w) {
					t.Errorf("the page doesn't contain %q", w)
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

func TestHomeHeaders(t *testing.T) {
	tests := []struct {
		name   string
		signIn SignIn
		header string
		want   string // a substring of the header's value
	}{
		{name: "the page is HTML", signIn: signInOn(""), header: "Content-Type", want: "text/html; charset=utf-8"},
		{name: "browsers may not read it as another type", signIn: signInOn(""), header: "X-Content-Type-Options", want: "nosniff"},
		{name: "nothing may store a signed-out page, since the page differs by session", signIn: signInOn(""), header: "Cache-Control", want: "no-store"},
		{name: "nothing may store a signed-in page", signIn: signInOn("U0FAKEVERIFIED"), header: "Cache-Control", want: "no-store"},
		{name: "a visitor who isn't signed in gets a CSP that allows no form, as on the other pages", signIn: signInOn(""), header: "Content-Security-Policy", want: "form-action 'none'"},
		{name: "without sign-in the CSP allows no form", signIn: SignIn{}, header: "Content-Security-Policy", want: "form-action 'none'"},
		{name: "a signed-in visitor's CSP lets the sign-out form post to this server only", signIn: signInOn("U0FAKEVERIFIED"), header: "Content-Security-Policy", want: "form-action 'self'"},
		{name: "the CSP still allows nothing but the page's own stylesheet", signIn: signInOn("U0FAKEVERIFIED"), header: "Content-Security-Policy", want: "default-src 'none'; style-src 'sha256-"},
		{name: "the page can't be framed", signIn: signInOn("U0FAKEVERIFIED"), header: "Content-Security-Policy", want: "frame-ancestors 'none'"},
		// Under no-referrer, browsers send "Origin: null" with the sign-out
		// form's POST, and POST /auth/logout refuses an Origin that isn't
		// this server's.
		{name: "the referrer policy is same-origin, so the sign-out form's POST carries this server's Origin", signIn: signInOn("U0FAKEVERIFIED"), header: "Referrer-Policy", want: "same-origin"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, _ := getHome(t, Options{}, tt.signIn)
			if got := resp.Header.Get(tt.header); !strings.Contains(got, tt.want) {
				t.Errorf("%s = %q, want it to contain %q", tt.header, got, tt.want)
			}
		})
	}
}

func TestHomeRoute(t *testing.T) {
	mux := http.NewServeMux()
	s := newSite(t, Options{})
	s.Register(mux)
	s.RegisterHome(mux, signInOn(""))
	srv := httptest.NewServer(mux)
	defer srv.Close()

	tests := []struct {
		name     string
		method   string
		path     string
		wantCode int
	}{
		{name: "/ serves the home page", path: "/", wantCode: 200},
		{name: "HEAD / answers too", method: http.MethodHead, path: "/", wantCode: 200},
		{name: "POST / is refused", method: http.MethodPost, path: "/", wantCode: 405},
		{name: "a path nothing serves is still a 404, not the home page", path: "/nothing-here", wantCode: 404},
		{name: "/index.html isn't the home page either", path: "/index.html", wantCode: 404},
		{name: "the other pages still answer beside it", path: "/privacy", wantCode: 200},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			method := tt.method
			if method == "" {
				method = http.MethodGet
			}
			req, err := http.NewRequest(method, srv.URL+tt.path, nil)
			if err != nil {
				t.Fatal(err)
			}
			resp, err := srv.Client().Do(req)
			if err != nil {
				t.Fatal(err)
			}
			resp.Body.Close()
			if resp.StatusCode != tt.wantCode {
				t.Errorf("%s %s = %d, want %d", method, tt.path, resp.StatusCode, tt.wantCode)
			}
		})
	}
}

var (
	// fetches matches markup that makes a browser fetch something while it
	// loads the page: a script, a stylesheet or font through <link>, an
	// image, a frame, media, a <base> that moves relative links, a meta
	// refresh, and CSS that loads a URL.
	fetches = regexp.MustCompile(`(?i)<(script|link|img|image|iframe|frame|object|embed|audio|video|source|track|picture|svg|base)\b|\b(src|srcset|poster)=|@import|url\(|http-equiv`)
	// formAction finds where each form on a page posts.
	formAction = regexp.MustCompile(`<form\b[^>]*\baction="([^"]*)"`)
	// strictCSP is the only shape of CSP the pages send: nothing but the
	// inline stylesheet by its hash, and a form only to this server.
	strictCSP = regexp.MustCompile(`^default-src 'none'; style-src 'sha256-[A-Za-z0-9+/]+=*'; base-uri 'none'; form-action '(none|self)'; frame-ancestors 'none'$`)
)

// TestNoExternalResources checks every page, the home page in each sign-in
// state included, for anything a browser would fetch from elsewhere as it
// loads it, such as a script, a font, an image or a tracker, and checks
// that the page's CSP would block one if it were there.
func TestNoExternalResources(t *testing.T) {
	s := newSite(t, Options{})
	type page struct{ name, body, csp string }
	pages := []page{
		{name: "the privacy notice", body: string(s.privacy), csp: s.csp},
		{name: "the evidence page", body: string(s.current), csp: s.csp},
	}
	for _, e := range s.evidence {
		pages = append(pages, page{name: "the evidence definition from scoring " + e.from.String(), body: string(e.page), csp: s.csp})
	}
	homes := []struct {
		name   string
		signIn SignIn
	}{
		{name: "the home page without sign-in", signIn: SignIn{}},
		{name: "the home page for a visitor who isn't signed in", signIn: signInOn("")},
		{name: "the home page for a signed-in visitor", signIn: signInOn("U0FAKEVERIFIED")},
	}
	for _, h := range homes {
		resp, body := getHome(t, Options{}, h.signIn)
		pages = append(pages, page{name: h.name, body: body, csp: resp.Header.Get("Content-Security-Policy")})
	}
	demos := []struct {
		name   string
		opts   Options
		signIn SignIn
	}{
		{name: "the sample dashboard without sign-in", signIn: SignIn{}},
		{name: "the sample dashboard with sign-ups closed", signIn: signInOn("")},
		{name: "the sample dashboard with sign-ups open", opts: Options{SignupsOpen: true}, signIn: signInOn("")},
	}
	for _, d := range demos {
		resp, body := getDemo(t, d.opts, d.signIn)
		pages = append(pages, page{name: d.name, body: body, csp: resp.Header.Get("Content-Security-Policy")})
	}
	for _, p := range pages {
		t.Run(p.name+" loads nothing from anywhere, and its CSP would block anything it tried to", func(t *testing.T) {
			if m := fetches.FindString(p.body); m != "" {
				t.Errorf("the page has %q, which makes a browser fetch something", m)
			}
			for _, m := range formAction.FindAllStringSubmatch(p.body, -1) {
				if !strings.HasPrefix(m[1], "/") || strings.HasPrefix(m[1], "//") {
					t.Errorf("a form posts to %q, which isn't a path on this server", m[1])
				}
			}
			if !strictCSP.MatchString(p.csp) {
				t.Errorf("CSP %q allows more than the page's own stylesheet and forms to this server", p.csp)
			}
		})
	}
}

// TestHeader checks that every page's header links the home page.
func TestHeader(t *testing.T) {
	s := newSite(t, Options{})
	_, home := getHome(t, Options{}, signInOn(""))
	_, demo := getDemo(t, Options{}, signInOn(""))
	tests := []struct {
		name string
		page string
	}{
		{name: "the privacy notice links the home page", page: string(s.privacy)},
		{name: "the evidence page links the home page", page: string(s.current)},
		{name: "the home page links itself", page: home},
		{name: "the sample dashboard links the home page", page: demo},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if want := `<header>
<a class="name" href="/">pairtime</a>`; !strings.Contains(tt.page, want) {
				t.Errorf("the page doesn't contain %q", want)
			}
		})
	}
}

// TestLoginURL checks the one helper behind every sign-in link that comes
// back: /dashboard's here, and /device's and /account/devices' in package
// device. It encodes return_to, so the login handler's query reads back
// exactly the path it was given, whatever the path's own query holds.
func TestLoginURL(t *testing.T) {
	tests := []struct {
		name, returnTo, want string
	}{
		{name: "a path's slashes are encoded", returnTo: "/dashboard", want: "/auth/hca/login?return_to=%2Fdashboard"},
		{name: "a path with a query of its own comes back whole, as one parameter", returnTo: "/device?user_code=WDJB-MJHT", want: "/auth/hca/login?return_to=%2Fdevice%3Fuser_code%3DWDJB-MJHT"},
		{name: "an & in the path's query doesn't start a parameter of the login's", returnTo: "/projects/demo?tab=time&day=today"},
		{name: "a # in the path stays in return_to instead of ending the login URL", returnTo: "/docs/evidence#levels"},
		{name: "a space and a plus come back as they were", returnTo: "/a b+c"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := LoginURL("/auth/hca/login", tt.returnTo)
			if tt.want != "" && got != tt.want {
				t.Errorf("LoginURL = %q, want %q", got, tt.want)
			}
			u, err := url.Parse(got)
			if err != nil {
				t.Fatalf("LoginURL = %q, which doesn't parse: %v", got, err)
			}
			q := u.Query()
			if u.Path != "/auth/hca/login" || u.Fragment != "" || len(q) != 1 || q.Get("return_to") != tt.returnTo {
				t.Errorf("LoginURL = %q reads back as path %q, fragment %q, query %v; want return_to %q alone", got, u.Path, u.Fragment, q, tt.returnTo)
			}
		})
	}
}
