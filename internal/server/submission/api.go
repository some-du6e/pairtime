// SPDX-License-Identifier: AGPL-3.0-or-later

package submission

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/some-du6e/pairtime/internal/server/accounts"
	"github.com/some-du6e/pairtime/internal/server/device"
	"github.com/some-du6e/pairtime/internal/server/ingest"
	"github.com/some-du6e/pairtime/internal/server/retention"
	"github.com/some-du6e/pairtime/internal/server/writer"
)

// The submissions API's paths (docs/spec/submissions-v1.md). A
// submission's own routes name it after SubmissionsPath: GET
// /api/v1/submissions/{id} and POST /api/v1/submissions/{id}/withdraw.
const (
	SubmissionsPath   = "/api/v1/submissions"
	DevlogWindowsPath = SubmissionsPath + "/devlog-windows"
	ProgramsPath      = "/api/v1/programs"
)

// FreshLogin is how long before a web session's create or withdrawal its
// fresh login may have been made (D-sb1-submissions, Q6), the 10 minutes
// a sign-in's own login state lasts.
const FreshLogin = 10 * time.Minute

// FreshLoginURL is where a web session gets a fresh login. The calling
// page adds &return_to= with its own path, which the API can't know.
const FreshLoginURL = "/auth/hca/login?fresh=1"

// MaxPage is the most submissions one page of the list holds.
const MaxPage = 100

// horizonEntry is the retention entry whose days are the submission
// horizon: the per-minute detail a submission can reach (DESIGN 4.4).
const horizonEntry = "server.minute_buckets"

// Devices finds the device of a token: *device.Store.
type Devices interface {
	Authenticate(ctx context.Context, token string) (device.Device, error)
}

// Sessions finds the web session of a request's cookie, or
// accounts.ErrNoSession: *auth.Service.
type Sessions interface {
	Session(r *http.Request) (*accounts.Session, error)
}

// Doer runs a write through the writer goroutine. *writer.Writer is one.
type Doer interface {
	Do(ctx context.Context, rows int, op writer.Op) error
}

// Options wire a Service.
type Options struct {
	// Devices authenticates a device's token, and Sessions a web session's
	// cookie. SameOrigin tells a write from pairtimed's own pages, the
	// check every session write passes; nil passes none.
	Devices    Devices
	Sessions   Sessions
	SameOrigin func(*http.Request) bool
	// Program, when set, finds the program whose credential a request
	// carries: its name, "" with no error when the request carries none,
	// or an error for a credential that doesn't check out. It is nil in
	// pairtimed until WP-PG6 connects its client-credentials check, so in
	// production no request reaches the program view.
	Program func(*http.Request) (string, error)
	// Programs are pairtime.toml's [[programs]].
	Programs []Program
	// Writer takes every write, and Read is the read pool.
	Writer Doer
	Read   Querier
	// Now is the clock. A freeze's time is Now inside the writer op that
	// stores it. Nil means time.Now.
	Now func() time.Time
	// BaseURL is the server's origin, for each submission's review_url.
	BaseURL string
	// ScoringVersion and Params are what a freeze records: Scoring's
	// result for pairtime.toml's scoring_version (D-sb1-submissions, Q10).
	ScoringVersion, Params string
	// Horizon is the submission horizon. Zero means server.minute_buckets'
	// days in docs/retention.json, so the two can't drift (Q12).
	Horizon time.Duration
	// Fingerprints finds the other accounts' projects that share a repo
	// fingerprint (Q11). Nil means NoFingerprints.
	Fingerprints Fingerprints
	// OnFreeze, when set, runs inside the freeze's writer op, after the
	// submission is stored and frozen: WP-SB4 writes its holds through it.
	// Its error refuses the create, which then stores nothing.
	OnFreeze func(ctx context.Context, tx writer.Tx, s Submission) error
	// OnWithdraw, when set, runs inside the withdrawal's writer op, after
	// the move to withdrawn: WP-SB5 marks drawn, unrevealed items withheld
	// through it.
	OnWithdraw func(ctx context.Context, tx writer.Tx, s Submission) error
	// Totals, when set, gives a submission's totals for the teen view and
	// the program view, read through q, the read pool: WP-SB4 connects its
	// re-score's totals through it in cmd/pairtimed, as it does OnFreeze.
	// Nil, or a nil answer, shows "totals": null.
	Totals func(ctx context.Context, q Querier, sub Submission) (any, error)
	// Hides, when set, reports whether the account's calibration flag
	// hides its credited time from it now, read through q, the read pool
	// (D-server-shadow; shadow.Hides). While it does, the account's teen
	// views show "totals": null, whatever Totals answers. The program view
	// is WP-SB4's to decide.
	Hides func(ctx context.Context, q Querier, account string) (bool, error)
	// Rate counts each device's requests to these routes, as ingest counts
	// its uploads (SP 8.6), once their signature verifies, and each
	// account's creates and withdrawals from a web session, under the
	// account's id. Nil means one of ingest's default rate, kept apart from
	// the device's other rates.
	Rate *ingest.Limiter
	// Logger hears of reads and writes that fail. Nil means
	// slog.Default().
	Logger *slog.Logger
}

// Service is the submissions API.
type Service struct {
	o                      Options
	programs               map[string]Program
	horizon                time.Duration
	scoringVersion, params string
	fingerprints           Fingerprints
	// refused is each signed write's refusal, for its copies.
	refused refusalStore
}

// Horizon returns the submission horizon: server.minute_buckets' days in
// docs/retention.json, the per-minute detail a submission can reach
// (DESIGN 4.4; D-sb1-submissions, Q12).
func Horizon() (time.Duration, error) {
	p, err := retention.Default()
	if err != nil {
		return 0, err
	}
	e, ok := p.Find(horizonEntry)
	if !ok || e.Days <= 0 {
		return 0, fmt.Errorf("submission: docs/retention.json has no days for %s, the submission horizon", horizonEntry)
	}
	return time.Duration(e.Days) * 24 * time.Hour, nil
}

// New returns the API.
func New(o Options) (*Service, error) {
	switch {
	case o.Devices == nil || o.Writer == nil || o.Read == nil:
		return nil, errors.New("submission: the API needs Devices, Writer and Read")
	case o.ScoringVersion == "" || o.Params == "":
		return nil, errors.New("submission: the API needs the scoring version and parameter hash a freeze records")
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Logger == nil {
		o.Logger = slog.Default()
	}
	if o.Rate == nil {
		o.Rate = ingest.NewLimiter(ingest.DefaultRateBurst, ingest.DefaultRateEvery)
	}
	s := &Service{o: o, programs: make(map[string]Program, len(o.Programs)), horizon: o.Horizon,
		scoringVersion: o.ScoringVersion, params: o.Params, fingerprints: o.Fingerprints}
	for _, p := range o.Programs {
		if _, dup := s.programs[p.Name]; dup {
			return nil, fmt.Errorf("submission: program %q is listed twice", p.Name)
		}
		s.programs[p.Name] = p
	}
	if s.horizon <= 0 {
		h, err := Horizon()
		if err != nil {
			return nil, err
		}
		s.horizon = h
	}
	if s.fingerprints == nil {
		s.fingerprints = NoFingerprints{}
	}
	return s, nil
}

// Register adds the API's routes to mux.
func (s *Service) Register(mux *http.ServeMux) {
	mux.HandleFunc("POST "+SubmissionsPath, s.keepRefusals(s.createSubmission))
	mux.HandleFunc("GET "+SubmissionsPath, s.listSubmissions)
	mux.HandleFunc("GET "+DevlogWindowsPath, s.previewDevlogWindows)
	mux.HandleFunc("GET "+SubmissionsPath+"/{id}", s.getSubmission)
	mux.HandleFunc("POST "+SubmissionsPath+"/{id}/withdraw", s.keepRefusals(s.withdrawSubmission))
	mux.HandleFunc("GET "+ProgramsPath, s.listPrograms)
}

// The codes of the refusals that are this API's own; the others are
// ingest's (docs/spec/submissions-v1.md 1).
const (
	CodeNotSignedIn        = "not_signed_in"
	CodeCrossOrigin        = "cross_origin"
	CodeFreshLoginRequired = "fresh_login_required"
	CodeNotVerified        = "not_verified"
	CodeProgramNotAllowed  = "program_not_allowed"
	CodeProgramUnknown     = "program_unknown"
	CodeRequestIDInvalid   = "request_id_invalid"
	CodeRequestConflict    = "request_conflict"
	CodeProjectInvalid     = "project_invalid"
	CodeProjectRepeated    = "project_repeated"
	CodeTooManyProjects    = "too_many_projects"
	CodeProjectUnknown     = "project_unknown"
	CodeWindowInvalid      = "window_invalid"
	CodeNoDevlogWindows    = "no_devlog_windows"
	CodeAlreadySubmitted   = "already_submitted"
	CodeLimitReached       = "limit_reached"
	CodeNotWithdrawable    = "not_withdrawable"
	CodeNotFound           = "not_found"
	CodeQueryInvalid       = "query_invalid"
)

// errorBody is the JSON of a refusal, as ingest's, with the members some
// refusals add.
type errorBody struct {
	Error  string `json:"error"`
	Detail string `json:"detail"`
	// DeviceKeyID names the device's key on signature_invalid.
	DeviceKeyID string `json:"device_key_id,omitempty"`
	// LoginURL is where to get a fresh login, on fresh_login_required.
	LoginURL string `json:"login_url,omitempty"`
	// Submissions are what the overlap refusal found, on
	// already_submitted to the account itself.
	Submissions []RefJSON `json:"submissions,omitempty"`
}

// RefJSON names a submission and its program in a refusal.
type RefJSON struct {
	ID      string `json:"id"`
	Program string `json:"program"`
}

// stamp sets the headers every answer carries: the server's clock, which
// a device corrects its X-Pairtime-Time by, and no-store.
func (s *Service) stamp(w http.ResponseWriter) {
	w.Header().Set(ingest.HeaderTime, strconv.FormatInt(s.o.Now().UnixMilli(), 10))
	w.Header().Set("Cache-Control", "no-store")
}

func (s *Service) writeJSON(w http.ResponseWriter, status int, v any) {
	s.stamp(w)
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func (s *Service) refuse(w http.ResponseWriter, r *refusal) {
	s.keep(w, r)
	if r.retryAfter > 0 {
		w.Header().Set("Retry-After", strconv.Itoa(r.retryAfter))
	}
	body := errorBody{Error: r.code, Detail: r.detail, DeviceKeyID: r.deviceKeyID, LoginURL: r.loginURL}
	for _, ref := range r.submissions {
		body.Submissions = append(body.Submissions, RefJSON{ID: ref.ID, Program: ref.Program})
	}
	s.writeJSON(w, r.status, body)
}

// caller is who a request comes from: an account, by a web session or one
// of its devices, or a program (WP-PG6).
type caller struct {
	account string
	// device is the calling device, for a device's request.
	device string
	// session is the web session, for a session's request.
	session *accounts.Session
	// program is the program, for a program credential's request.
	program string
	// done ends the device's request in its rate.
	done func()
}

// end tells the rate the request is answered.
func (c caller) end() {
	if c.done != nil {
		c.done()
	}
}

// admit finds who r comes from and reads its body: a program credential
// through Options.Program, a device's token with its signed headers, or
// the web session's cookie (docs/spec/submissions-v1.md 2). It answers a
// refusal itself and reports false. The caller calls end once it has
// answered.
func (s *Service) admit(w http.ResponseWriter, r *http.Request) (caller, []byte, bool) {
	if s.o.Program != nil {
		program, err := s.o.Program(r)
		switch {
		case err != nil:
			s.refuse(w, refuse(http.StatusUnauthorized, ingest.CodeTokenInvalid, "This program credential isn't valid."))
			return caller{}, nil, false
		case program != "":
			body, ref := readBody(w, r)
			if ref != nil {
				s.refuse(w, ref)
				return caller{}, nil, false
			}
			return caller{program: program}, body, true
		}
	}
	if len(r.Header.Values("Authorization")) > 0 {
		dev, body, at, ref := s.signed(w, r)
		if ref == nil {
			// A copy of a signed write that was refused gets the same
			// status and code, looked up at the time its clock check took
			// and before the device's rate (refusalStore).
			rec, recorded := w.(*recorder)
			key := dev.ID + " " + r.Header.Get(ingest.HeaderSignature)
			if recorded {
				if k, ok := s.refused.get(key, at.checkedMS); ok {
					s.answerAgain(w, k)
					return caller{}, nil, false
				}
			}
			// Only a signed request counts against the device's rate, so
			// nobody without the device's key can use it up.
			done, wait := s.o.Rate.Take(dev.ID, s.o.Now())
			if done != nil {
				// Only a request the rate admitted is kept once refused, so
				// the rate bounds what a device can keep (maxKeptRefusals).
				if recorded {
					rec.key = key
					rec.untilMS = at.signedMS + ingest.MaxSkewMS + 1
				}
				return caller{account: dev.Account, device: dev.ID, done: done}, body, true
			}
			ref = &refusal{status: http.StatusTooManyRequests, code: ingest.CodeRateLimited, retryAfter: wait,
				detail: "This device is sending faster than its rate, or another of its requests is still being answered; wait Retry-After seconds, then send it again."}
		}
		s.refuse(w, ref)
		return caller{}, nil, false
	}
	var session *accounts.Session
	err := accounts.ErrNoSession
	if s.o.Sessions != nil {
		session, err = s.o.Sessions.Session(r)
	}
	if err == nil && session == nil {
		err = accounts.ErrNoSession
	}
	switch {
	case errors.Is(err, accounts.ErrNoSession):
		w.Header().Add("WWW-Authenticate", `Bearer realm="pairtime"`)
		w.Header().Add("WWW-Authenticate", `Session realm="pairtime"`)
		s.refuse(w, refuse(http.StatusUnauthorized, CodeNotSignedIn, "Sign in on the web, or send the device token with a signed request."))
		return caller{}, nil, false
	case err != nil:
		s.o.Logger.Error("submission: reading a session", "err", err)
		s.refuse(w, unavailable())
		return caller{}, nil, false
	}
	body, ref := readBody(w, r)
	if ref != nil {
		s.refuse(w, ref)
		return caller{}, nil, false
	}
	return caller{account: session.User.ID, session: session}, body, true
}

// mayWrite checks that c may create or withdraw: one of the account's
// devices, or a web session from pairtimed's own pages whose fresh login
// was made at most FreshLogin before (D-sb1-submissions, Q6) and whose
// account is within its rate. It answers a refusal itself and reports
// false. The caller calls end once it has answered.
func (s *Service) mayWrite(w http.ResponseWriter, r *http.Request, c caller) (end func(), ok bool) {
	switch {
	case c.program != "":
		s.refuse(w, refuse(http.StatusForbidden, CodeProgramNotAllowed, "A program's credential can't create or withdraw a submission, or read devlog windows, on this server."))
		return nil, false
	case c.session == nil:
		// A device's request took its rate in admit.
		return func() {}, true
	case s.o.SameOrigin == nil || !s.o.SameOrigin(r):
		s.refuse(w, refuse(http.StatusForbidden, CodeCrossOrigin, "A web session creates and withdraws submissions only from this server's own pages."))
		return nil, false
	case !c.session.FreshLoginWithin(FreshLogin, s.o.Now()):
		ref := refuse(http.StatusForbidden, CodeFreshLoginRequired, "Submitting or withdrawing needs a fresh Hack Club Auth login, made at most 10 minutes before; sign in again at login_url, adding &return_to= with this page's path.")
		ref.loginURL = FreshLoginURL
		s.refuse(w, ref)
		return nil, false
	}
	// A web session's creates and withdrawals take one rate per account,
	// the one each computer has, so a signed-in account can't fill the
	// writer every account's writes share (docs/spec/submissions-v1.md
	// 2.1).
	done, wait := s.o.Rate.Take(sessionRate+c.account, s.o.Now())
	if done == nil {
		s.refuse(w, &refusal{status: http.StatusTooManyRequests, code: ingest.CodeRateLimited, retryAfter: wait,
			detail: "This account is submitting or withdrawing faster than its rate, or another of its submissions or withdrawals is still being answered; wait Retry-After seconds, then send it again."})
		return nil, false
	}
	return done, true
}

// sessionRate prefixes an account's id as its web session's key in
// Options.Rate, apart from its devices' ids.
const sessionRate = "account:"

// refusalOf is the answer to the error of a create's or a withdrawal's
// writer op, or of a read: nil for none, and 503 for any error the API
// doesn't name, which it logs unless the request ended first.
func (s *Service) refusalOf(r *http.Request, err error, what string) *refusal {
	var overlap *OverlapError
	switch {
	case err == nil:
		return nil
	case errors.Is(err, ErrRequestConflict):
		return refuse(http.StatusConflict, CodeRequestConflict, "This request_id was used for another request; make a new one for a new request.")
	case errors.Is(err, ErrProgramUnknown):
		return refuse(http.StatusBadRequest, CodeProgramUnknown, "This server takes no submissions for that program; GET "+ProgramsPath+" lists the ones it does.")
	case errors.Is(err, ErrNotVerified):
		return refuse(http.StatusForbidden, CodeNotVerified, "Only an account whose identity Hack Club Auth has verified can submit hours.")
	case errors.Is(err, ErrProjectUnknown):
		return refuse(http.StatusBadRequest, CodeProjectUnknown, "A project isn't one of the account's, or a key isn't one this computer uploaded.")
	case errors.Is(err, ErrLimit):
		return refuse(http.StatusConflict, CodeLimitReached, "This submission would take the account past 1,000 submissions or 10,000 frozen windows, the most it can hold.")
	case errors.Is(err, ErrWindowInvalid):
		return refuse(http.StatusBadRequest, CodeWindowInvalid, "A window starts before it ends, ends no later than now and starts no earlier than 180 days ago, the submission horizon.")
	case errors.Is(err, ErrNoDevlogWindows):
		return refuse(http.StatusConflict, CodeNoDevlogWindows, "These projects have no devlog windows to freeze; send one window instead.")
	case errors.As(err, &overlap):
		ref := refuse(http.StatusConflict, CodeAlreadySubmitted, "The windows overlap a submission of this account that covers one of the same projects, was frozen and wasn't withdrawn; submissions names each one.")
		ref.submissions = overlap.Submissions
		return ref
	case errors.Is(err, ErrNotFound):
		return refuse(http.StatusNotFound, CodeNotFound, "There is no such submission.")
	case errors.Is(err, ErrNotWithdrawable):
		return refuse(http.StatusConflict, CodeNotWithdrawable, "Once a submission's checks have run it can't be withdrawn.")
	case errors.Is(err, errResized):
		return unavailable()
	}
	if r.Context().Err() == nil {
		s.o.Logger.Error(what, "err", err)
	}
	return unavailable()
}

// isKey reports whether s is an id or a key: 1 to 128 letters, digits, _
// or -, the shape AGENTS.md's opaque keys have.
func isKey(s string) bool {
	if len(s) == 0 || len(s) > 128 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !('a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' || '0' <= c && c <= '9' || c == '_' || c == '-') {
			return false
		}
	}
	return true
}

// decodeObject reads data, one JSON object in valid UTF-8 with the members
// m, into v, refusing a member v lacks.
func decodeObject(data []byte, v any, m objectShape, shape string) *refusal {
	if !utf8.Valid(data) || !json.Valid(data) {
		return refuse(http.StatusBadRequest, ingest.CodeJSONInvalid, "The body must be one JSON object in valid UTF-8.")
	}
	if !m.holds(data) {
		return refuse(http.StatusBadRequest, ingest.CodeBodyShape, shape)
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil || !bytes.HasPrefix(bytes.TrimSpace(data), []byte("{")) {
		return refuse(http.StatusBadRequest, ingest.CodeBodyShape, shape)
	}
	return nil
}

// objectShape is the members a JSON object takes, each with the members
// its value takes when that value is an object too, or nil.
type objectShape map[string]objectShape

// holds reports whether raw is a JSON object that names each of its
// members once, spelled exactly as m names it and not null, and whether
// each member whose value m gives members of its own holds such an object.
// encoding/json alone takes a member's name in any letter case, keeps the
// last of a repeated one and reads a null as a member left out, and
// body_shape refuses all three. Ingest refuses the first two too, but
// reads a null member of its body as one left out (ingest-v1 2.3); a
// create sends exactly the members it means, one of each pair
// (docs/spec/submissions-v1.md 3.1).
func (m objectShape) holds(raw []byte) bool {
	dec := json.NewDecoder(bytes.NewReader(raw))
	if t, err := dec.Token(); err != nil || t != json.Delim('{') {
		return false
	}
	seen := make(map[string]bool, len(m))
	for dec.More() {
		t, err := dec.Token()
		if err != nil {
			return false
		}
		name, ok := t.(string)
		inner, known := m[name]
		if !ok || !known || seen[name] {
			return false
		}
		seen[name] = true
		var v json.RawMessage
		if err := dec.Decode(&v); err != nil {
			return false
		}
		if bytes.Equal(v, []byte("null")) || inner != nil && !inner.holds(v) {
			return false
		}
	}
	t, err := dec.Token()
	return err == nil && t == json.Delim('}')
}

// createMembers are a create's members (docs/spec/submissions-v1.md 3).
var createMembers = objectShape{"program": nil, "request_id": nil, "projects": nil, "project_keys": nil,
	"window": {"start": nil, "end": nil}, "devlog_windows": nil}

// createRequest is the body of POST /api/v1/submissions
// (docs/spec/submissions-v1.md 3).
type createRequest struct {
	Program       *string        `json:"program"`
	RequestID     *string        `json:"request_id"`
	Projects      *[]string      `json:"projects"`
	ProjectKeys   *[]string      `json:"project_keys"`
	Window        *windowRequest `json:"window"`
	DevlogWindows *bool          `json:"devlog_windows"`
}

type windowRequest struct {
	Start *time.Time `json:"start"`
	End   *time.Time `json:"end"`
}

const createShape = `The body is an object with program and request_id, exactly one of projects and project_keys, a list of keys, and exactly one of window, {"start", "end"} in RFC 3339, and devlog_windows, true.`

// checkCreate checks a create's body and turns it into the op's input.
func (s *Service) checkCreate(c caller, req createRequest) (createInput, *refusal) {
	shape := refuse(http.StatusBadRequest, ingest.CodeBodyShape, createShape)
	switch {
	case req.Program == nil || req.RequestID == nil,
		(req.Projects == nil) == (req.ProjectKeys == nil),
		(req.Window == nil) == (req.DevlogWindows == nil),
		req.DevlogWindows != nil && !*req.DevlogWindows,
		req.Window != nil && (req.Window.Start == nil || req.Window.End == nil):
		return createInput{}, shape
	case req.ProjectKeys != nil && c.device == "":
		return createInput{}, refuse(http.StatusBadRequest, ingest.CodeBodyShape, "A web session names projects by their ids, as the dashboard lists them; only a computer names its own project keys.")
	}
	if !isKey(*req.RequestID) {
		return createInput{}, refuse(http.StatusBadRequest, CodeRequestIDInvalid, "The request_id is 1 to 128 letters, digits, _ or -, made at random by the client and sent again on a retry.")
	}
	list := req.Projects
	if list == nil {
		list = req.ProjectKeys
	}
	sorted, ref := checkProjects(*list)
	if ref != nil {
		return createInput{}, ref
	}
	in := createInput{Account: c.account, CreatedBy: BySession, Device: c.device, Program: *req.Program, RequestID: *req.RequestID}
	if c.device != "" {
		in.CreatedBy = ByDevice
	}
	if req.Projects != nil {
		in.Projects = sorted
	} else {
		in.ProjectKeys = sorted
	}
	if w := req.Window; w != nil {
		in.Window = &Window{Start: w.Start.UnixMilli(), End: w.End.UnixMilli()}
	}
	in.RequestSHA256 = requestSum(in.Program, in.Device, in.Projects, in.ProjectKeys, in.Window)
	return in, nil
}

// checkProjects checks a list of project ids or keys, 1 to MaxProjects
// keys with none twice, and returns it sorted.
func checkProjects(list []string) ([]string, *refusal) {
	switch {
	case len(list) == 0:
		return nil, refuse(http.StatusBadRequest, ingest.CodeBodyShape, "Name at least one project.")
	case len(list) > MaxProjects:
		return nil, refuse(http.StatusBadRequest, CodeTooManyProjects, "A submission names at most 32 projects.")
	}
	for _, p := range list {
		if !isKey(p) {
			return nil, refuse(http.StatusBadRequest, CodeProjectInvalid, "A project id or key is 1 to 128 letters, digits, _ or -.")
		}
	}
	sorted, ok := sortedUnique(list)
	if !ok {
		return nil, refuse(http.StatusBadRequest, CodeProjectRepeated, "A project is named twice.")
	}
	return sorted, nil
}

// createSubmission is POST /api/v1/submissions: 201 with the submission
// frozen now, 200 with the one the account made before under the same
// request_id, and a refusal of docs/spec/submissions-v1.md 3 otherwise.
func (s *Service) createSubmission(w http.ResponseWriter, r *http.Request) {
	c, body, ok := s.admit(w, r)
	if !ok {
		return
	}
	defer c.end()
	end, ok := s.mayWrite(w, r, c)
	if !ok {
		return
	}
	defer end()
	var req createRequest
	if ref := decodeObject(body, &req, createMembers, createShape); ref != nil {
		s.refuse(w, ref)
		return
	}
	in, ref := s.checkCreate(c, req)
	if ref != nil {
		s.refuse(w, ref)
		return
	}
	rows, pre, oneRow := s.prepare(r.Context(), in)
	in.OneRow = oneRow
	var out Submission
	var created bool
	err := s.o.Writer.Do(r.Context(), rows, func(ctx context.Context, tx writer.Tx) error {
		var err error
		out, created, err = s.create(ctx, tx, in, pre)
		return err
	})
	if ref := s.refusalOf(r, err, "submission: creating a submission"); ref != nil {
		s.refuse(w, ref)
		return
	}
	v, err := s.teenView(r.Context(), out)
	if err != nil {
		s.refuse(w, s.refusalOf(r, err, "submission: reading a submission"))
		return
	}
	status := http.StatusOK
	if created {
		w.Header().Set("Location", SubmissionsPath+"/"+out.ID)
		status = http.StatusCreated
	}
	s.writeJSON(w, status, v)
}

// getSubmission is GET /api/v1/submissions/{id}: the teen view of one of
// the account's own submissions, or the program view of one addressed to
// the calling program, and 404 for any other id.
func (s *Service) getSubmission(w http.ResponseWriter, r *http.Request) {
	c, _, ok := s.admit(w, r)
	if !ok {
		return
	}
	defer c.end()
	id := r.PathValue("id")
	notFound := s.refusalOf(r, ErrNotFound, "")
	if !isKey(id) {
		s.refuse(w, notFound)
		return
	}
	sub, err := Get(r.Context(), s.o.Read, id)
	if err != nil {
		s.refuse(w, s.refusalOf(r, err, "submission: reading a submission"))
		return
	}
	var v any
	switch {
	case c.program != "" && (sub.Program != c.program || !sub.TakesHours):
		s.refuse(w, notFound)
		return
	case c.program != "":
		v, err = s.programView(r.Context(), sub)
	case sub.Account != c.account:
		s.refuse(w, notFound)
		return
	default:
		v, err = s.teenView(r.Context(), sub)
	}
	if err != nil {
		s.refuse(w, s.refusalOf(r, err, "submission: reading a submission"))
		return
	}
	s.writeJSON(w, http.StatusOK, v)
}

// withdrawSubmission is POST /api/v1/submissions/{id}/withdraw, which has
// no body: 200 with the submission withdrawn, or as it was when it was
// withdrawn already, 409 from checked on, and 404 for another account's.
func (s *Service) withdrawSubmission(w http.ResponseWriter, r *http.Request) {
	c, body, ok := s.admit(w, r)
	if !ok {
		return
	}
	defer c.end()
	end, ok := s.mayWrite(w, r, c)
	if !ok {
		return
	}
	defer end()
	if len(body) != 0 {
		s.refuse(w, refuse(http.StatusBadRequest, ingest.CodeBodyShape, "A withdrawal has no body."))
		return
	}
	id := r.PathValue("id")
	if !isKey(id) {
		s.refuse(w, s.refusalOf(r, ErrNotFound, ""))
		return
	}
	var out Submission
	err := s.o.Writer.Do(r.Context(), 3, func(ctx context.Context, tx writer.Tx) error {
		var err error
		out, err = s.withdraw(ctx, tx, c.account, id)
		return err
	})
	if ref := s.refusalOf(r, err, "submission: withdrawing a submission"); ref != nil {
		s.refuse(w, ref)
		return
	}
	v, err := s.teenView(r.Context(), out)
	if err != nil {
		s.refuse(w, s.refusalOf(r, err, "submission: reading a submission"))
		return
	}
	s.writeJSON(w, http.StatusOK, v)
}

// listQuery is what GET /api/v1/submissions takes.
var listQuery = []string{"updated_since", "limit", "cursor"}

// parseList reads the list's query: updated_since, an RFC 3339 time, 0 by
// default; limit, 1 to MaxPage and MaxPage by default; and the cursor of
// the page before.
func parseList(q url.Values) (int64, *Cursor, int, *refusal) {
	invalid := refuse(http.StatusBadRequest, CodeQueryInvalid, "The query takes updated_since, an RFC 3339 time, limit, 1 to 100, and cursor, as next_cursor gave it, each at most once.")
	for k, vs := range q {
		if !slices.Contains(listQuery, k) || len(vs) != 1 {
			return 0, nil, 0, invalid
		}
	}
	var since int64
	if v := q.Get("updated_since"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			return 0, nil, 0, invalid
		}
		since = t.UnixMilli()
	}
	limit := MaxPage
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > MaxPage {
			return 0, nil, 0, invalid
		}
		limit = n
	}
	var after *Cursor
	if v := q.Get("cursor"); v != "" {
		c, ok := decodeCursor(v)
		if !ok {
			return 0, nil, 0, invalid
		}
		after = &c
	}
	return since, after, limit, nil
}

// encodeCursor writes c as the opaque next_cursor.
func encodeCursor(c Cursor) string {
	return base64.RawURLEncoding.EncodeToString([]byte(strconv.FormatInt(c.UpdatedAtMS, 10) + "." + c.ID))
}

// decodeCursor reads a next_cursor.
func decodeCursor(v string) (Cursor, bool) {
	b, err := base64.RawURLEncoding.DecodeString(v)
	if err != nil {
		return Cursor{}, false
	}
	ms, id, ok := strings.Cut(string(b), ".")
	t, okMS := decimal(ms)
	if !ok || !okMS || !isKey(id) {
		return Cursor{}, false
	}
	return Cursor{UpdatedAtMS: t, ID: id}, true
}

// listJSON is GET /api/v1/submissions's answer.
type listJSON struct {
	Submissions []any   `json:"submissions"`
	NextCursor  *string `json:"next_cursor"`
}

// listSubmissions is GET /api/v1/submissions?updated_since=&limit=&cursor=:
// the account's own submissions in the teen view, or, for a program
// credential, the submissions addressed to that program in the program
// view (D-sb1-submissions, Q14), ordered by when each last changed and
// then by id.
func (s *Service) listSubmissions(w http.ResponseWriter, r *http.Request) {
	c, _, ok := s.admit(w, r)
	if !ok {
		return
	}
	defer c.end()
	since, after, limit, ref := parseList(r.URL.Query())
	if ref != nil {
		s.refuse(w, ref)
		return
	}
	scope := Scope{Account: c.account}
	if c.program != "" {
		scope = Scope{Program: c.program}
	}
	page, more, err := List(r.Context(), s.o.Read, scope, since, after, limit)
	if err != nil {
		s.refuse(w, s.refusalOf(r, err, "submission: listing submissions"))
		return
	}
	out := listJSON{Submissions: make([]any, 0, len(page))}
	for _, sub := range page {
		var v any
		if c.program != "" {
			v, err = s.programView(r.Context(), sub)
		} else {
			v, err = s.teenView(r.Context(), sub)
		}
		if err != nil {
			s.refuse(w, s.refusalOf(r, err, "submission: listing submissions"))
			return
		}
		out.Submissions = append(out.Submissions, v)
	}
	if more {
		last := page[len(page)-1]
		next := encodeCursor(Cursor{UpdatedAtMS: last.UpdatedAt.UnixMilli(), ID: last.ID})
		out.NextCursor = &next
	}
	s.writeJSON(w, http.StatusOK, out)
}

// devlogWindowsJSON is GET /api/v1/submissions/devlog-windows's answer.
type devlogWindowsJSON struct {
	NotBefore *time.Time   `json:"not_before"`
	Horizon   time.Time    `json:"horizon"`
	Windows   []WindowJSON `json:"windows"`
}

// previewDevlogWindows is GET /api/v1/submissions/devlog-windows, with
// project=<id> for each project or, from a device, project_key=<key> for
// each of its keys: the devlog windows a devlog-mode create of those
// projects would freeze now, cut at the horizon, and the frozen end they
// start from, exactly as the create works them out (D-sb1-submissions,
// Q11), for WP-SB6's preview.
func (s *Service) previewDevlogWindows(w http.ResponseWriter, r *http.Request) {
	c, _, ok := s.admit(w, r)
	if !ok {
		return
	}
	defer c.end()
	if c.program != "" {
		s.refuse(w, refuse(http.StatusForbidden, CodeProgramNotAllowed, "A program's credential can't create or withdraw a submission, or read devlog windows, on this server."))
		return
	}
	q := r.URL.Query()
	for k := range q {
		if k != "project" && k != "project_key" {
			s.refuse(w, refuse(http.StatusBadRequest, CodeQueryInvalid, "The query takes project=<id> for each project or, from a computer, project_key=<key> for each of its keys."))
			return
		}
	}
	projects, keys := q["project"], q["project_key"]
	switch {
	case (len(projects) == 0) == (len(keys) == 0):
		s.refuse(w, refuse(http.StatusBadRequest, CodeQueryInvalid, "Name the projects by project or, from a computer, by project_key, and not both."))
		return
	case len(keys) > 0 && c.device == "":
		s.refuse(w, refuse(http.StatusBadRequest, CodeQueryInvalid, "A web session names projects by their ids; only a computer names its own project keys."))
		return
	}
	list := projects
	if len(keys) > 0 {
		list = keys
	}
	sorted, ref := checkProjects(list)
	if ref != nil {
		s.refuse(w, ref)
		return
	}
	if len(keys) > 0 {
		projects, keys = nil, sorted
	} else {
		projects, keys = sorted, nil
	}
	now := s.o.Now()
	horizon := now.Add(-s.horizon)
	ids, pairs, err := resolve(r.Context(), s.o.Read, c.account, c.device, projects, keys)
	var ws []Window
	var notBefore int64
	if err == nil {
		ws, notBefore, err = devlogWindows(r.Context(), s.o.Read, c.account, ids, keysOf(pairs), horizon.UnixMilli())
	}
	if err != nil {
		s.refuse(w, s.refusalOf(r, err, "submission: reading devlog windows"))
		return
	}
	out := devlogWindowsJSON{Horizon: horizon.UTC().Truncate(time.Millisecond), Windows: windowsJSON(ws)}
	if notBefore > 0 {
		t := time.UnixMilli(notBefore).UTC()
		out.NotBefore = &t
	}
	s.writeJSON(w, http.StatusOK, out)
}

// listPrograms is GET /api/v1/programs: each program this server takes
// submissions for whose submissions claim hours, by name and the name
// teens see, as [[programs]] lists them, for a program picker
// (D-sb1-submissions, Q3). It needs no credential.
func (s *Service) listPrograms(w http.ResponseWriter, r *http.Request) {
	out := []ProgramJSON{}
	for _, p := range s.o.Programs {
		if p.TakesHours {
			out = append(out, ProgramJSON{Name: p.Name, DisplayName: p.DisplayName})
		}
	}
	s.writeJSON(w, http.StatusOK, struct {
		Programs []ProgramJSON `json:"programs"`
	}{Programs: out})
}

// ProgramJSON is a program in a view: its name and the name teens see.
type ProgramJSON struct {
	Name        string `json:"name"`
	DisplayName string `json:"display_name"`
}

// WindowJSON is a frozen window in a view.
type WindowJSON struct {
	Start time.Time `json:"start"`
	End   time.Time `json:"end"`
}

func windowsJSON(ws []Window) []WindowJSON {
	out := make([]WindowJSON, len(ws))
	for i, w := range ws {
		out[i] = WindowJSON{Start: time.UnixMilli(w.Start).UTC(), End: time.UnixMilli(w.End).UTC()}
	}
	return out
}

// KeyJSON is a frozen (device id, project key) pair in a view.
type KeyJSON struct {
	Device     string `json:"device_id"`
	ProjectKey string `json:"project_key"`
}

// ProjectJSON is one of P's projects in the teen view, with its frozen
// pairs.
type ProjectJSON struct {
	ID   string    `json:"id"`
	Keys []KeyJSON `json:"keys"`
}

// Times are a submission's times in a view, each null until it happens.
type Times struct {
	CreatedAt          time.Time  `json:"created_at"`
	UpdatedAt          time.Time  `json:"updated_at"`
	FrozenAt           *time.Time `json:"frozen_at"`
	MetadataRevealedAt *time.Time `json:"metadata_revealed_at"`
	ChallengedAt       *time.Time `json:"challenged_at"`
	DecidedAt          *time.Time `json:"decided_at"`
	WithdrawnAt        *time.Time `json:"withdrawn_at"`
	RevokedAt          *time.Time `json:"revoked_at"`
}

func timesOf(sub Submission) Times {
	opt := func(t time.Time) *time.Time {
		if t.IsZero() {
			return nil
		}
		return &t
	}
	return Times{
		CreatedAt: sub.CreatedAt, UpdatedAt: sub.UpdatedAt,
		FrozenAt: opt(sub.FrozenAt), MetadataRevealedAt: opt(sub.MetadataRevealedAt), ChallengedAt: opt(sub.ChallengedAt),
		DecidedAt: opt(sub.DecidedAt), WithdrawnAt: opt(sub.WithdrawnAt), RevokedAt: opt(sub.RevokedAt),
	}
}

// orNull is s as a view's member, null when it is "".
func orNull(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// TeenView is a submission as its own account sees it, on the web and on
// each of its computers (docs/spec/submissions-v1.md 5). It never holds a
// flag, and holds r only once the draw is stored.
type TeenView struct {
	ID         string      `json:"id"`
	Program    ProgramJSON `json:"program"`
	TakesHours bool        `json:"takes_hours"`
	State      State       `json:"state"`
	RequestID  *string     `json:"request_id"`
	ExternalID *string     `json:"external_id"`
	// CreatedBy is "session" or "device", and Device the device that made
	// it, null for a session.
	CreatedBy string        `json:"created_by"`
	Device    *string       `json:"device_id"`
	Projects  []ProjectJSON `json:"projects"`
	Mode      string        `json:"mode"`
	Windows   []WindowJSON  `json:"windows"`
	// ScoringVersion, Params and RSHA256 are the freeze's, null before it.
	ScoringVersion *string `json:"scoring_version"`
	Params         *string `json:"params"`
	RSHA256        *string `json:"r_sha256"`
	// R is r in lowercase hex, only once challenged_at is set.
	R                    string `json:"r,omitempty"`
	EligibilityToConfirm bool   `json:"eligibility_to_confirm"`
	Times
	ReviewURL string `json:"review_url"`
	// Totals are Options.Totals' answer, null until WP-SB4 connects it.
	Totals any `json:"totals"`
}

// UserJSON is the account a program view names (SP 10.2).
type UserJSON struct {
	PairtimeID string  `json:"pairtime_id"`
	HCASub     string  `json:"hca_sub"`
	SlackID    *string `json:"slack_id"`
}

// ProgramView is a submission as the program it is addressed to sees it
// (D-sb1-submissions, Q13): never its flags, its projects or keys, the
// credential or computer that made it, r, or any other submission.
type ProgramView struct {
	ID                   string       `json:"id"`
	ExternalID           *string      `json:"external_id"`
	State                State        `json:"state"`
	User                 UserJSON     `json:"user"`
	Windows              []WindowJSON `json:"windows"`
	ScoringVersion       *string      `json:"scoring_version"`
	Params               *string      `json:"params"`
	EligibilityToConfirm bool         `json:"eligibility_to_confirm"`
	Times
	ReviewURL string `json:"review_url"`
	Totals    any    `json:"totals"`
}

// teen builds the teen view of sub, with r when it is set, the program's
// display name, and the review URL under base.
func teen(sub Submission, r []byte, displayName, base string) TeenView {
	v := TeenView{
		ID: sub.ID, Program: ProgramJSON{Name: sub.Program, DisplayName: displayName}, TakesHours: sub.TakesHours,
		State: sub.State, RequestID: orNull(sub.RequestID), ExternalID: orNull(sub.ExternalID),
		CreatedBy: sub.CreatedBy, Device: orNull(sub.Device), Mode: sub.Mode, Windows: windowsJSON(sub.Windows),
		ScoringVersion: orNull(sub.ScoringVersion), Params: orNull(sub.Params), RSHA256: orNull(sub.RSHA256),
		EligibilityToConfirm: sub.EligibilityToConfirm, Times: timesOf(sub), ReviewURL: base + "/review/submissions/" + sub.ID,
	}
	if len(r) > 0 {
		v.R = hex.EncodeToString(r)
	}
	v.Projects = make([]ProjectJSON, len(sub.Projects))
	for i, p := range sub.Projects {
		keys := make([]KeyJSON, len(p.Keys))
		for j, k := range p.Keys {
			keys[j] = KeyJSON{Device: k.Device, ProjectKey: k.ProjectKey}
		}
		v.Projects[i] = ProjectJSON{ID: p.ID, Keys: keys}
	}
	return v
}

// revealed reads sub's r through RevealedR once its draw is stored, and
// nil before.
func revealed(ctx context.Context, q Querier, sub Submission) ([]byte, error) {
	if sub.ChallengedAt.IsZero() {
		return nil, nil
	}
	r, err := RevealedR(ctx, q, sub.ID)
	if errors.Is(err, ErrNotRevealed) {
		return nil, nil
	}
	return r, err
}

// teenView is the teen view of sub, as the API sends it, with the program's
// display name from [[programs]], or its name for a program the list no
// longer holds.
func (s *Service) teenView(ctx context.Context, sub Submission) (TeenView, error) {
	r, err := revealed(ctx, s.o.Read, sub)
	if err != nil {
		return TeenView{}, err
	}
	name := sub.Program
	if p, ok := s.programs[sub.Program]; ok {
		name = p.DisplayName
	}
	v := teen(sub, r, name, s.o.BaseURL)
	if v.Totals, err = s.teenTotals(ctx, sub); err != nil {
		return TeenView{}, err
	}
	return v, nil
}

// teenTotals is sub's totals for its teen view: totalsOf's, or nil while
// the account's calibration flag hides its credited time (Options.Hides),
// whatever Options.Totals answers.
func (s *Service) teenTotals(ctx context.Context, sub Submission) (any, error) {
	if s.o.Totals == nil {
		return nil, nil
	}
	if s.o.Hides != nil {
		hidden, err := s.o.Hides(ctx, s.o.Read, sub.Account)
		if err != nil || hidden {
			return nil, err
		}
	}
	return s.totalsOf(ctx, sub)
}

// totalsOf is sub's totals for a view: Options.Totals' answer, or nil,
// which a view shows as null, while nothing connects it.
func (s *Service) totalsOf(ctx context.Context, sub Submission) (any, error) {
	if s.o.Totals == nil {
		return nil, nil
	}
	return s.o.Totals(ctx, s.o.Read, sub)
}

// programView is the program view of sub.
func (s *Service) programView(ctx context.Context, sub Submission) (ProgramView, error) {
	u := UserJSON{PairtimeID: sub.Account}
	var slack sql.NullString
	if err := s.o.Read.QueryRowContext(ctx, `SELECT hca_sub, slack_id FROM users WHERE id = $1`, sub.Account).Scan(&u.HCASub, &slack); err != nil {
		return ProgramView{}, fmt.Errorf("submission: reading %s's account: %w", sub.ID, err)
	}
	u.SlackID = orNull(slack.String)
	totals, err := s.totalsOf(ctx, sub)
	if err != nil {
		return ProgramView{}, err
	}
	return ProgramView{
		ID: sub.ID, ExternalID: orNull(sub.ExternalID), State: sub.State, User: u, Windows: windowsJSON(sub.Windows),
		ScoringVersion: orNull(sub.ScoringVersion), Params: orNull(sub.Params), EligibilityToConfirm: sub.EligibilityToConfirm,
		Times: timesOf(sub), ReviewURL: s.o.BaseURL + "/review/submissions/" + sub.ID, Totals: totals,
	}, nil
}
