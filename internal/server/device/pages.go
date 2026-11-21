// SPDX-License-Identifier: AGPL-3.0-or-later

package device

import (
	"bytes"
	"errors"
	"html/template"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/some-du6e/pairtime/internal/server/accounts"
	"github.com/some-du6e/pairtime/internal/server/auth"
	"github.com/some-du6e/pairtime/internal/server/pages"
)

// The limit on wrong codes: after maxWrongCodes within wrongCodeWindow, an
// account gets no more tries until the window ends, however many times it
// signs in again (RFC 8628 5.1). Codes typed on /device and codes posted
// to /device/decision count alike.
const (
	maxWrongCodes   = 10
	wrongCodeWindow = 15 * time.Minute
)

// The pages' bodies. Each renders inside the server's layout.html.
var tmpl = template.Must(template.New("device").Funcs(template.FuncMap{
	"os":   osName,
	"when": when,
}).Parse(`
{{- define "closed" -}}
<h1>Linking isn't open yet</h1>
<p>This pairtime server isn't taking sign-ins yet, so the pairtime app can't be linked to an account here.</p>
<p>pairtime keeps counting on your computer in the meantime, and nothing is lost: everything stays there. Once sign-in opens, run <code>pairtime login</code> again.</p>
<p><a href="/">About pairtime</a></p>
{{- end}}

{{- define "enter" -}}
<h1>Link the pairtime app</h1>
<p>Type the code that <code>pairtime login</code> shows in your terminal, to link that computer to your account, <strong>{{.Handle}}</strong>.</p>
{{- with .Problem}}
<blockquote><p>{{.}}</p></blockquote>
{{- end}}
{{- if not .Locked}}
<form method="post" action="/device">
<input type="hidden" name="csrf" value="{{.CSRF}}">
<p><label for="user_code">Code</label><br>
<input id="user_code" name="user_code" value="{{.Typed}}" required maxlength="20" autocomplete="off" autocapitalize="characters" spellcheck="false" placeholder="XXXX-XXXX"></p>
<p><button class="button" type="submit">Continue</button></p>
</form>
{{- end}}
<p>Didn't run <code>pairtime login</code>? Then there's nothing to do here. <a href="/account/devices">Your devices</a></p>
{{- end}}

{{- define "confirm" -}}
<h1>Link this computer?</h1>
<p>pairtime on <strong>{{os .OS}}</strong> (pairtime {{.ClientBuild}}) asks to be linked to your account, <strong>{{.Handle}}</strong>.</p>
<p>Check that your terminal shows this code: <code>{{.UserCode}}</code></p>
<p>Once it's linked, that computer uploads the minutes pairtime counts, as numbers, to this server. What you type stays on your computer.</p>
<p>If you didn't just run <code>pairtime login</code> yourself, or the code doesn't match, choose <strong>Don't link</strong>: someone may be trying to get you to link their computer.</p>
{{- template "decision" .}}
{{- end}}

{{- define "relink" -}}
<h1>This computer is linked already</h1>
<p>pairtime on <strong>{{os .OS}}</strong> (pairtime {{.ClientBuild}}) asks to be linked to your account, <strong>{{.Handle}}</strong>, as the computer <code>{{.DeviceID}}</code>. That computer has been linked to your account since {{when .LinkedAt}}, and last connected {{when .LastSeenAt}}.</p>
<p>Check that your terminal shows this code: <code>{{.UserCode}}</code></p>
<p>Linking it again signs that copy of pairtime out: its sign-in stops working, and only the copy that asked now uploads as that computer.</p>
<p>If you didn't just run <code>pairtime login</code> on that computer yourself, or the code doesn't match, choose <strong>Don't link</strong>: someone may be trying to take that computer's place.</p>
{{- template "decision" .}}
{{- end}}

{{- define "decision" -}}
<form method="post" action="/device/decision">
<input type="hidden" name="csrf" value="{{.CSRF}}">
<input type="hidden" name="user_code" value="{{.UserCode}}">
<p><button class="button" type="submit" name="decision" value="approve">Link it</button>
<button type="submit" name="decision" value="deny">Don't link</button></p>
</form>
{{- end}}

{{- define "taken" -}}
<h1>This computer is linked to another account</h1>
<p>The pairtime app that asked is linked to a different account on this server, which keeps its device ID while it holds what that computer uploaded. So it can't be linked to your account, <strong>{{.Handle}}</strong>, and nothing changed.</p>
<p>If you share that computer, sign in here as the account it's linked to, and type the code again. Otherwise choose <strong>Stop pairtime login</strong>: it stops waiting and says that computer can only be linked to your account as a new device.</p>
<form method="post" action="/device/decision">
<input type="hidden" name="csrf" value="{{.CSRF}}">
<input type="hidden" name="user_code" value="{{.UserCode}}">
<p><button class="button" type="submit" name="decision" value="approve">Stop pairtime login</button></p>
</form>
{{- end}}

{{- define "done" -}}
{{- if .Approved}}
<h1>Linked</h1>
<p>Go back to your terminal: <code>pairtime login</code> finishes on its own within a few seconds.</p>
<p>See and remove your linked computers on <a href="/account/devices">Your devices</a>.</p>
{{- else if .Taken}}
<h1>Not linked</h1>
<p>That computer is linked to another account, so it wasn't linked to yours. <code>pairtime login</code> on it stops waiting and says what it takes to link it as a new device.</p>
{{- else}}
<h1>Not linked</h1>
<p>That computer isn't linked to your account. <code>pairtime login</code> stops waiting, and pairtime keeps counting on that computer alone.</p>
{{- end}}
{{- end}}

{{- define "devices" -}}
<h1>Your devices</h1>
<p>The computers linked to your account, <strong>{{.Handle}}</strong>. To link another one, run <code>pairtime login</code> on it.</p>
{{- if .Devices}}
<div class="table"><table>
<thead><tr><th>Computer</th><th>Linked</th><th>Last connected</th><th></th></tr></thead>
<tbody>
{{- range .Devices}}
<tr>
<td>{{if .RemovedAt.IsZero}}{{os .OS}}, pairtime {{.ClientBuild}}{{else}}A removed computer{{end}}<br><code>{{.ID}}</code></td>
<td>{{when .LinkedAt}}</td>
<td>{{when .LastSeenAt}}</td>
<td>{{if .Revoked}}Removed {{when .RemovedAt}}{{else}}<form method="post" action="/account/devices/remove"><input type="hidden" name="csrf" value="{{$.CSRF}}"><input type="hidden" name="device_id" value="{{.ID}}"><button type="submit">Remove</button></form>{{end}}</td>
</tr>
{{- end}}
</tbody>
</table></div>
<p>Removing a computer stops its uploads at once. A computer that never uploaded anything is deleted at once. Of any other, only its device ID stays listed, with your account, for 90 days, or for as long as this server keeps what it uploaded, as the <a href="/privacy">privacy notice</a> says.</p>
{{- else}}
<p>No computer is linked yet.</p>
{{- end}}
{{- end}}
`))

// osName is an OS family in words.
func osName(family string) string {
	switch family {
	case OSWindows:
		return "Windows"
	case OSMacOS:
		return "macOS"
	case OSLinux:
		return "Linux"
	}
	return "another OS"
}

// when writes a time for the pages, in UTC, since the server doesn't know
// the reader's time zone.
func when(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format("2006-01-02 15:04") + " UTC"
}

// pageData is what the page templates read.
type pageData struct {
	Handle string
	CSRF   string
	// Typed is the code as typed, refilled after a mistake.
	Typed   string
	Problem string
	// Locked hides the form after too many wrong codes.
	Locked bool
	Pending
	Approved bool
	// Taken says the code's device is another account's, so nothing was
	// linked.
	Taken   bool
	Devices []Device
}

// render writes the page template name with d through the layout.
func (s *Service) render(w http.ResponseWriter, status int, title, name string, d *pageData, sess *accounts.Session) {
	var body bytes.Buffer
	if err := tmpl.ExecuteTemplate(&body, name, d); err != nil {
		s.log.Error("rendering a device page", "page", name, "err", err)
		http.Error(w, "This page didn't render.", http.StatusInternalServerError)
		return
	}
	p := pages.Page{Title: title, Body: template.HTML(body.String()), Form: true}
	if sess != nil {
		p.Account = &pages.Header{SignedIn: true, Handle: s.handle(&sess.User), Login: auth.LoginPath, Logout: auth.LogoutPath}
	}
	s.site.WritePage(w, status, p)
}

// session returns the request's session for a page, or answers the
// request itself and returns nil: with sign-in off, a page saying linking
// isn't open, and signed out, a redirect through sign-in that comes back
// to returnTo.
func (s *Service) session(w http.ResponseWriter, r *http.Request, returnTo string) *accounts.Session {
	if !s.signIn.Enabled() {
		s.render(w, http.StatusOK, "Linking isn't open yet", "closed", &pageData{}, nil)
		return nil
	}
	sess, err := s.signIn.Session(r)
	if err != nil {
		if !errors.Is(err, accounts.ErrNoSession) {
			s.log.Error("reading a session for a device page", "err", err)
		}
		http.Redirect(w, r, auth.LoginPath+"?return_to="+url.QueryEscape(returnTo), http.StatusSeeOther)
		return nil
	}
	return sess
}

// page is GET /device, where verification_uri points: the form for the
// code, or with ?user_code= from verification_uri_complete, the
// confirmation for that code at once (RFC 8628 3.3.1).
func (s *Service) page(w http.ResponseWriter, r *http.Request) {
	typed := r.URL.Query().Get("user_code")
	code, valid := NormalizeUserCode(typed)
	returnTo := PagePath
	if valid {
		returnTo += "?user_code=" + url.QueryEscape(FormatUserCode(code))
	}
	sess := s.session(w, r, returnTo)
	if sess == nil {
		return
	}
	d := s.data(r, sess)
	if typed == "" {
		s.render(w, http.StatusOK, "Link the pairtime app", "enter", d, sess)
		return
	}
	s.lookup(w, r, sess, d, typed)
}

// enter is POST /device: the code the teen typed.
func (s *Service) enter(w http.ResponseWriter, r *http.Request) {
	f, problem := form(w, r)
	if problem != "" {
		http.Error(w, problem, http.StatusBadRequest)
		return
	}
	sess := s.session(w, r, PagePath)
	if sess == nil {
		return
	}
	d := s.data(r, sess)
	if !s.checkCSRF(r, f) {
		d.Problem = "That form was out of date, so nothing happened. Type the code again."
		s.render(w, http.StatusForbidden, "Link the pairtime app", "enter", d, sess)
		return
	}
	s.lookup(w, r, sess, d, f.Get("user_code"))
}

// lookup shows the confirmation for the typed code, or the form again
// with what was wrong. Every wrong code counts against the account's
// limit, whichever way it came.
func (s *Service) lookup(w http.ResponseWriter, r *http.Request, sess *accounts.Session, d *pageData, typed string) {
	key := sess.User.ID
	d.Typed = typed
	// The try is taken before the lookup and given back for a code that
	// exists, so tries sent at once can't all pass a check made before
	// any of them failed.
	if !s.tries.take(key) {
		d.Locked = true
		d.Problem = "That's too many wrong codes for now. Wait a quarter of an hour, then try again."
		s.render(w, http.StatusTooManyRequests, "Link the pairtime app", "enter", d, sess)
		return
	}
	code, ok := NormalizeUserCode(typed)
	var p Pending
	err := ErrNoCode
	if ok {
		p, err = s.store.Lookup(r.Context(), code)
	}
	if !errors.Is(err, ErrNoCode) {
		s.tries.refund(key)
	}
	switch {
	case errors.Is(err, ErrNoCode):
		d.Problem = "That code isn't right, or it has expired. Check it against your terminal, or run pairtime login again for a new one."
		s.render(w, http.StatusOK, "Link the pairtime app", "enter", d, sess)
	case err != nil:
		s.log.Error("looking up a device code", "err", err)
		d.Problem = "Something went wrong on this pairtime server. Try again in a minute."
		s.render(w, http.StatusInternalServerError, "Link the pairtime app", "enter", d, sess)
	case p.Owner != "" && p.Owner != sess.User.ID:
		d.Pending = p
		s.render(w, http.StatusOK, "Linked to another account", "taken", d, sess)
	case p.Linked:
		// A code in the name of a computer the account has linked now,
		// which approving would sign out (RFC 8628 5.4).
		d.Pending = p
		s.render(w, http.StatusOK, "Link this computer again?", "relink", d, sess)
	default:
		d.Pending = p
		s.render(w, http.StatusOK, "Link this computer?", "confirm", d, sess)
	}
}

// decide is POST /device/decision: Link it, or Don't link.
func (s *Service) decide(w http.ResponseWriter, r *http.Request) {
	f, problem := form(w, r)
	if problem != "" {
		http.Error(w, problem, http.StatusBadRequest)
		return
	}
	sess := s.session(w, r, PagePath)
	if sess == nil {
		return
	}
	d := s.data(r, sess)
	if !s.checkCSRF(r, f) {
		d.Problem = "That form was out of date, so nothing happened. Type the code again."
		s.render(w, http.StatusForbidden, "Link the pairtime app", "enter", d, sess)
		return
	}
	approve := f.Get("decision") == "approve"
	if !approve && f.Get("decision") != "deny" {
		http.Error(w, "decision must be approve or deny", http.StatusBadRequest)
		return
	}
	// A decision names its code itself, so it is held to the same limit
	// as a typed code.
	key := sess.User.ID
	if !s.tries.take(key) {
		d.Locked = true
		d.Problem = "That's too many wrong codes for now. Wait a quarter of an hour, then try again."
		s.render(w, http.StatusTooManyRequests, "Link the pairtime app", "enter", d, sess)
		return
	}
	code, ok := NormalizeUserCode(f.Get("user_code"))
	err := ErrNoCode
	if ok {
		err = s.store.Decide(r.Context(), code, sess.User.ID, approve)
	}
	if !errors.Is(err, ErrNoCode) {
		s.tries.refund(key)
	}
	switch {
	case errors.Is(err, ErrNoCode):
		d.Problem = "That code has expired, or was answered already. Run pairtime login again for a new one."
		s.render(w, http.StatusOK, "Link the pairtime app", "enter", d, sess)
	case errors.Is(err, ErrOtherAccount):
		s.log.Info("a device code for another account's device was turned away", "user", sess.User.ID)
		d.Taken = true
		s.render(w, http.StatusOK, "Not linked", "done", d, sess)
	case err != nil:
		s.log.Error("recording a device decision", "err", err)
		d.Problem = "Something went wrong on this pairtime server, and nothing was linked. Try again in a minute."
		s.render(w, http.StatusInternalServerError, "Link the pairtime app", "enter", d, sess)
	default:
		s.log.Info("a device code was answered", "user", sess.User.ID, "approved", approve)
		d.Approved = approve
		title := "Not linked"
		if approve {
			title = "Linked"
		}
		s.render(w, http.StatusOK, title, "done", d, sess)
	}
}

// devices is GET /account/devices: the account's devices, with Remove.
func (s *Service) devices(w http.ResponseWriter, r *http.Request) {
	sess := s.session(w, r, DevicesPath)
	if sess == nil {
		return
	}
	list, err := s.store.List(r.Context(), sess.User.ID)
	if err != nil {
		s.log.Error("listing devices", "err", err)
		http.Error(w, "Something went wrong on this pairtime server. Try again in a minute.", http.StatusInternalServerError)
		return
	}
	d := s.data(r, sess)
	d.Devices = list
	s.render(w, http.StatusOK, "Your devices", "devices", d, sess)
}

// remove is POST /account/devices/remove: Remove on one device.
func (s *Service) remove(w http.ResponseWriter, r *http.Request) {
	f, problem := form(w, r)
	if problem != "" {
		http.Error(w, problem, http.StatusBadRequest)
		return
	}
	sess := s.session(w, r, DevicesPath)
	if sess == nil {
		return
	}
	if !s.checkCSRF(r, f) {
		http.Error(w, "That form was out of date, so nothing was removed. Go back, reload the page and try again.", http.StatusForbidden)
		return
	}
	id := f.Get("device_id")
	err := s.store.Remove(r.Context(), sess.User.ID, id)
	switch {
	case errors.Is(err, ErrNoDevice):
		http.Error(w, "Your account has no such device.", http.StatusNotFound)
		return
	case err != nil:
		s.log.Error("removing a device", "err", err)
		http.Error(w, "Something went wrong on this pairtime server, and nothing was removed. Try again in a minute.", http.StatusInternalServerError)
		return
	}
	s.log.Info("removed a device", "user", sess.User.ID, "device", id)
	http.Redirect(w, r, DevicesPath, http.StatusSeeOther)
}

// data is what every signed-in page reads.
func (s *Service) data(r *http.Request, sess *accounts.Session) *pageData {
	return &pageData{Handle: s.handle(&sess.User), CSRF: s.csrfToken(r)}
}

// limiter counts wrong codes per account over fixed windows.
type limiter struct {
	max    int
	window time.Duration
	now    func() time.Time

	mu    sync.Mutex
	tries map[string]*tally
}

type tally struct {
	start time.Time
	n     int
}

// maxTallies bounds the limiter's memory. When it is full, windows that
// have ended are dropped, and if none has, the oldest is.
const maxTallies = 10000

func newLimiter(max int, window time.Duration, now func() time.Time) *limiter {
	return &limiter{max: max, window: window, now: now, tries: make(map[string]*tally)}
}

// take counts a try for key and reports true, or reports false when key
// has used up its tries in this window. The caller gives the try back
// with refund once the code turns out right, so only wrong codes stay
// counted. One lock covers the check and the count, so tries sent at
// once can't pass together.
func (l *limiter) take(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	t, ok := l.tries[key]
	switch {
	case ok && now.Sub(t.start) < l.window:
		if t.n >= l.max {
			return false
		}
		t.n++
		return true
	case !ok && len(l.tries) >= maxTallies:
		l.prune(now)
	}
	l.tries[key] = &tally{start: now, n: 1}
	return true
}

// refund gives back a try take counted, for a code that was right.
func (l *limiter) refund(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if t, ok := l.tries[key]; ok && t.n > 0 {
		t.n--
	}
}

// prune drops ended windows, or the oldest window when none has ended.
func (l *limiter) prune(now time.Time) {
	var oldest string
	for k, t := range l.tries {
		if now.Sub(t.start) >= l.window {
			delete(l.tries, k)
			continue
		}
		if oldest == "" || t.start.Before(l.tries[oldest].start) {
			oldest = k
		}
	}
	if len(l.tries) >= maxTallies && oldest != "" {
		delete(l.tries, oldest)
	}
}
