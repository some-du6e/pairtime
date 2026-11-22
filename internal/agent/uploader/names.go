// SPDX-License-Identifier: AGPL-3.0-or-later

package uploader

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/some-du6e/pairtime/internal/agent/home"
	"github.com/some-du6e/pairtime/pkg/event"
	"github.com/some-du6e/pairtime/pkg/projectname"
)

// The owner's project names (D-owner-names; docs/spec/project-names-v1.md).
// A project's name shows on its owner's own dashboard while config.toml
// turns it on. The uploader sends what config.toml changed since the
// server's last answer in POST /api/v1/projects/names, signed like an
// upload over M_names, with the device's one request counter, and keeps
// the names that answer listed in project-names.json, with the short code
// it gives each project key the device uploaded minutes of: the code the
// owner's dashboard calls the project by, which the device can't work out
// from its own keys (project-names-v1 1.6). A name the device sent that an
// answer lacks, the owner hid on the dashboard: the uploader turns it off
// in config.toml, so it stays hidden until the owner turns it on again on
// the computer. Only the labels the owner approved and the keys they name
// go: nothing else about a project.

// pathNames is the names endpoint (project-names-v1 1).
const pathNames = "/api/v1/projects/names"

// namesContext starts M_names (project-names-v1 1.2): ingest-v1 2.2's M
// with its own context string, so a names signature never passes for an
// upload, nor an upload's for a names request.
const namesContext = "pairtime-project-names-v1\n"

// NamesFileName is the file in the data directory that holds what the
// uploader knows of the device's names on the server (Names).
const NamesFileName = "project-names.json"

// namesVersion is project-names.json's format.
const namesVersion = 1

// namesMax is the most items one names request carries in set and delete
// together, the server's limit (project-names-v1 1.4). More go in the
// requests after.
const namesMax = 200

// namesReadEvery is how often the uploader reads the device's names and
// codes, with {}: the read at the start finds the names hidden on the
// dashboard while the agent wasn't running and the codes the server gave
// meanwhile, and this one those since.
const namesReadEvery = 24 * time.Hour

// namesHold is how long a body the server refused for a reason sending it
// again doesn't fix waits before it goes again, unless config.toml changes
// it first.
const namesHold = time.Hour

// hiddenMax bounds the notes of names hidden on the dashboard; past it the
// oldest go.
const hiddenMax = 1000

// Names is project-names.json, what the uploader knows of the device's
// names on the server, for `pairtime projects` and the local dashboard to
// show. Only the uploader writes it, with home.WritePrivate: it is the
// computer's own record, and nothing in it leaves the computer but in the
// requests the server answered.
type Names struct {
	V int `json:"v"`
	// Server and Device are the sign-in the names are the server's for: a
	// sign-in to another server, or as another device, has none of them.
	Server string `json:"server"`
	Device string `json:"device"`
	// Confirmed are the names the server's last answer listed, each
	// project's label by its key: what the owner's dashboard shows.
	Confirmed map[string]string `json:"confirmed,omitempty"`
	// Codes are the short codes the server's last answer gave the
	// device's project keys, each by its key: the owner's dashboard calls
	// the project "Project <code>" while its name is off. The server works
	// them out over every key the account's devices uploaded, and a code
	// gets longer when another device's key starts the same way, so the
	// device can't work one out itself. A key the server has no minutes of
	// has none (project-names-v1 1.6).
	Codes map[string]string `json:"codes,omitempty"`
	// AnsweredAt is when that answer came, in Unix ms on the device's
	// clock, 0 before the first.
	AnsweredAt int64 `json:"answered_at,omitempty"`
	// Hidden are the names the owner hid on the dashboard, by key, each
	// turned off in config.toml when it was found.
	Hidden map[string]HiddenName `json:"hidden,omitempty"`
}

// HiddenName is a name the owner hid on their dashboard.
type HiddenName struct {
	// Label is the label the dashboard showed.
	Label string `json:"label"`
	// At is when the uploader found it hidden, in Unix ms.
	At int64 `json:"at"`
	// Pending is set while config.toml still has the name on, because
	// pairtime couldn't turn it off there: the uploader doesn't send it,
	// and tries again at each wake.
	Pending bool `json:"pending,omitempty"`
}

// ReadNames reads project-names.json in the data directory dataDir. A
// missing file is an error that wraps fs.ErrNotExist, and the device then
// knows of no names on the server, nor of any code. A code that isn't one
// counts for nothing, so what the file says can't put anything else where
// `pairtime projects` and the local dashboard show a code.
func ReadNames(dataDir string) (Names, error) {
	data, err := os.ReadFile(filepath.Join(dataDir, NamesFileName))
	if err != nil {
		return Names{}, err
	}
	var n Names
	if err := json.Unmarshal(data, &n); err != nil {
		return Names{}, fmt.Errorf("%s doesn't read: %w", NamesFileName, err)
	}
	if n.V != namesVersion {
		return Names{}, fmt.Errorf("%s is format %d, not %d", NamesFileName, n.V, namesVersion)
	}
	n.Codes = keptCodes(n.Codes)
	return n, nil
}

// nameItem is a name as the wire carries it: a project key and its label
// (project-names-v1 1.3, 1.6).
type nameItem struct {
	Project string `json:"project"`
	Label   string `json:"label"`
}

// namesBody is a names request's JSON, inside its gzip stream
// (project-names-v1 1.3): the names to show and the keys whose names go.
// Empty members are left out, so a read is {}.
type namesBody struct {
	Set    []nameItem `json:"set,omitempty"`
	Delete []string   `json:"delete,omitempty"`
}

// namesReply is the answer to a names request, status 200: every name the
// server holds for the device after it, and the short code of each project
// key the device uploaded minutes of, by key (project-names-v1 1.6). An
// answer without codes gives none.
type namesReply struct {
	Names []nameItem        `json:"names"`
	Codes map[string]string `json:"codes"`
}

// encodeNames returns b's JSON as it goes: encoding/json's, with a label's
// <, > and & as they are.
func encodeNames(b namesBody) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(b); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(buf.Bytes(), []byte("\n")), nil
}

// keyForm reports whether s has an opaque key's form: 1 to 128 of A-Z a-z
// 0-9 _ - (ingest-v1 1).
func keyForm(s string) bool {
	if s == "" || len(s) > 128 {
		return false
	}
	return !strings.ContainsFunc(s, func(r rune) bool {
		return !('a' <= r && r <= 'z' || 'A' <= r && r <= 'Z' || '0' <= r && r <= '9' || r == '_' || r == '-')
	})
}

// codeMax is the longest code the server gives one of the device's keys:
// a git adapter's whole key, its kind prefix and 32 hex digits, which
// projectname.ShortCodes falls back to when no part of it tells two keys
// apart.
const codeMax = 33

// codeForm reports whether s has a short code's form, as the server's
// answer gives one: 1 to codeMax of 0-9 a-z _ -.
func codeForm(s string) bool {
	if s == "" || len(s) > codeMax {
		return false
	}
	return !strings.ContainsFunc(s, func(r rune) bool {
		return !('a' <= r && r <= 'z' || '0' <= r && r <= '9' || r == '_' || r == '-')
	})
}

// keptCodes returns the codes of codes that are a key's and have a code's
// form, nil for none: what the device shows, and keeps.
func keptCodes(codes map[string]string) map[string]string {
	var out map[string]string
	for key, code := range codes {
		if keyForm(key) && codeForm(code) {
			if out == nil {
				out = make(map[string]string, len(codes))
			}
			out[key] = code
		}
	}
	return out
}

// namesDiff returns what the server needs to show wanted, the names
// config.toml turns on, when it shows confirmed: every name on whose label
// the server doesn't show, and every key it shows a name for that is off,
// each in key order.
func namesDiff(wanted, confirmed map[string]string) ([]nameItem, []string) {
	var set []nameItem
	for _, key := range slices.Sorted(maps.Keys(wanted)) {
		label := wanted[key]
		if !keyForm(key) || !projectname.ValidLabel(label) {
			continue
		}
		if got, ok := confirmed[key]; !ok || got != label {
			set = append(set, nameItem{Project: key, Label: label})
		}
	}
	var del []string
	for _, key := range slices.Sorted(maps.Keys(confirmed)) {
		if _, on := wanted[key]; !on {
			del = append(del, key)
		}
	}
	return set, del
}

// takeNames returns the body of the next request for set and del: the
// deletes first, then the names to show, namesMax items at most.
func takeNames(set []nameItem, del []string) namesBody {
	var b namesBody
	n := min(len(del), namesMax)
	b.Delete = del[:n]
	b.Set = set[:min(len(set), namesMax-n)]
	return b
}

// readDue reports whether a read is due at now after the last answer at
// at, in Unix ms: a day after it, or at once when the clock went back.
func readDue(at int64, now time.Time) bool {
	d := now.UnixMilli() - at
	return d >= namesReadEvery.Milliseconds() || d < 0
}

// syncNames makes the names request that config.toml and the server's last
// answer call for, if any, and reports whether it made one, which is then
// the cycle's request, and whether more goes at once, as the upload after
// it. The request carries what config.toml changed since that answer, or,
// when nothing changed, a read, {}, at the start and a day after the last
// answer, which a device with no name on sends too, to learn the codes the
// owner's dashboard calls its projects by.
func (u *Uploader) syncNames(c Credentials) (sent, more bool) {
	if u.opts.Names == nil {
		return false, false
	}
	wanted, ok := u.opts.Names()
	if !ok {
		return false, false
	}
	if u.now().Before(u.namesWait) {
		return false, false
	}
	wanted = maps.Clone(wanted)
	u.loadNames(c)
	u.hidePending(wanted)
	set, del := namesDiff(wanted, u.names.Confirmed)
	var body namesBody
	switch {
	case len(set)+len(del) > 0:
		body = takeNames(set, del)
	case !u.namesStarted || readDue(u.names.AnsweredAt, u.now()):
		// A read: its answer says which names the owner hid, and gives
		// the codes.
	default:
		return false, false
	}
	js, err := encodeNames(body)
	if err != nil {
		u.setProblem("names", fmt.Sprintf("pairtime can't build its project names request: %v", err))
		return false, false
	}
	raw, err := compress(js)
	if err != nil {
		u.setProblem("names", fmt.Sprintf("pairtime can't build its project names request: %v", err))
		return false, false
	}
	hash := bodyHash(raw)
	if hash == u.namesHeld && u.now().Before(u.namesAfter) {
		return false, false
	}
	return u.sendNames(c, body, raw, hash)
}

// loadNames reads project-names.json for the sign-in c, the first time and
// after a sign-in to another server or as another device, whose server
// shows none of the names the file has: then only its notes of names
// hidden on the dashboard stay, which describe config.toml.
func (u *Uploader) loadNames(c Credentials) {
	if u.names != nil && u.names.Server == c.Server && u.names.Device == c.DeviceID {
		return
	}
	n, err := ReadNames(u.opts.DataDir)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		u.log.Warn("project-names.json doesn't read, so the server's names are read again", "err", err)
	}
	if n.Server != c.Server || n.Device != c.DeviceID {
		n = Names{Hidden: n.Hidden}
	}
	n.V, n.Server, n.Device = namesVersion, c.Server, c.DeviceID
	u.names = &n
	u.namesStarted = false
	u.namesKey, u.namesHash, u.namesHeld = "", "", ""
}

// saveNames writes project-names.json.
func (u *Uploader) saveNames() {
	data, err := json.Marshal(u.names)
	if err == nil {
		err = home.WritePrivate(filepath.Join(u.opts.DataDir, NamesFileName), append(data, '\n'))
	}
	if err != nil {
		u.log.Warn("couldn't save project-names.json", "err", err)
	}
}

// hidePending turns off in config.toml the names the owner hid on the
// dashboard that pairtime couldn't turn off there when it found them, and
// takes out of wanted those still on, which aren't sent. A name whose
// label config.toml changed since is one the owner turned on again, which
// goes.
func (u *Uploader) hidePending(wanted map[string]string) {
	changed := false
	var stuck []string
	for _, key := range slices.Sorted(maps.Keys(u.names.Hidden)) {
		h := u.names.Hidden[key]
		if !h.Pending {
			continue
		}
		label, on := wanted[key]
		switch {
		case on && label != h.Label:
			delete(u.names.Hidden, key)
		case on && (u.opts.HideName == nil || u.opts.HideName(key) != nil):
			delete(wanted, key)
			stuck = append(stuck, key)
			continue
		default:
			delete(wanted, key)
			h.Pending = false
			u.names.Hidden[key] = h
		}
		changed = true
	}
	if changed {
		u.saveNames()
	}
	if len(stuck) == 0 {
		u.setProblem("hidden", "")
		return
	}
	u.setProblem("hidden", fmt.Sprintf("you hid project names on your pairtime dashboard that pairtime couldn't turn off in config.toml, so it doesn't send them again; to keep them off, set online = false under [projects.<key>] in config.toml for %s", strings.Join(stuck, ", ")))
}

// sendNames sends a names request, its body raw with hash, signed again
// for each try that a clock skew or a changed body asks for, and handles
// its answer. It reports whether a request reached the server, which is
// then the cycle's, and whether more goes at once, as the upload after a
// 200. A failure to reach the server reports none, so the cycle goes on to
// the upload (namesFailed).
func (u *Uploader) sendNames(c Credentials, body namesBody, raw []byte, hash string) (sent, more bool) {
	if u.namesKey == "" || u.namesHash != hash {
		// A new body, a new key; a retry of the same one keeps it, as an
		// upload's does (ingest-v1 2.10).
		u.namesKey, u.namesHash = event.NewEventID(u.now()), hash
	}
	for try := 0; try < 3; try++ {
		// The sign-in may have gone since the cycle began: nothing goes then
		// (device-auth-v1 8).
		if now, ok := u.opts.Login.Credentials(); !ok || now.Token != c.Token || now.Server != c.Server || now.DeviceID != c.DeviceID {
			return true, false
		}
		req, err := u.reserve()
		if err != nil {
			u.setProblem("local", fmt.Sprintf("pairtime can't keep its request counter in state.json: %v", err))
			u.notBefore = u.now().Add(Every)
			return true, false
		}
		sg, err := signNames(c.Sign, c.DeviceID, req, u.clock.serverNow().UnixMilli(), hash)
		if err != nil {
			u.setProblem("login", fmt.Sprintf("pairtime can't sign its requests: %v", err))
			u.notBefore = u.now().Add(Every)
			return true, false
		}
		hr, err := u.newRequest(http.MethodPost, c, pathNames, raw)
		if err != nil {
			u.setProblem("login", fmt.Sprintf("the server address %q doesn't work: %v", c.Server, err))
			u.notBefore = u.now().Add(Every)
			return true, false
		}
		setHeaders(hr.Header, c, u.namesKey, hash, sg, u.userAgent)
		a, err := u.do(u.ctx, hr)
		switch {
		case err != nil:
		case a.status == http.StatusNotFound:
			// A server without the names endpoint (errNoNames).
			err = errNoNames
		case a.status != http.StatusTooManyRequests && a.status < 500 && !fromPairtimed(a):
			// A proxy's answer, or Cloudflare's: a network error (ingest-v1
			// 5.5), never acted on.
			err = errNotPairtimed
		}
		if err != nil || a.status == http.StatusTooManyRequests || a.status >= 500 {
			return u.namesFailed(err, a)
		}
		switch {
		case a.status == http.StatusOK:
			return true, u.namesAnswered(body, hash, a)
		case a.refusal.Error == refusalClockSkew:
			continue // the answer set the clock's offset
		case a.refusal.Error == refusalCounter:
			// The state read sets the counter above the server's, and the
			// next cycle sends the names again (ingest-v1 5.1).
			return true, u.sync(c)
		case a.refusal.Error == refusalBodyHash:
			continue // the body changed on the way
		case a.refusal.Error == refusalKeyReused:
			u.namesKey = event.NewEventID(u.now())
			continue
		case a.refusal.Error == refusalBodyUnreadable:
			return u.namesFailed(errBodyUnreadable, a)
		}
		return true, u.namesRefused(c, hash, a)
	}
	return true, u.namesRefused(c, hash, answer{refusal: Refusal{Error: "retries"}})
}

// errNoNames is a 404 to a names request: a server without the names
// endpoint, as one older than it, since pairtimed refuses no names request
// with a 404. Like an answer that isn't pairtimed's, it tells doctor
// nothing: the names wait on their own backoff (namesFailed).
var errNoNames = errors.New("uploader: the server has no project names endpoint")

// namesFailed handles a names request that didn't reach the server, got
// an answer that isn't pairtimed's or a 404 (errNoNames), or got a 429 or
// a 5xx: the names wait as an upload would, with full jitter on a backoff
// of their own and the server's Retry-After, and are sent again once it
// ends, and doctor hears nothing of it. They leave the upload's backoff
// and its upload-failure notes alone: the cycle goes on to the upload,
// which tries the server itself, so a server whose names endpoint fails,
// or one too old to have it, never holds back an upload. After a 429 the
// cycle ends instead, since the server's bucket for the device ran dry.
func (u *Uploader) namesFailed(err error, a answer) (sent, more bool) {
	if errors.Is(err, context.Canceled) {
		return true, false // the agent is stopping
	}
	wait := u.namesBackoff.fail(retryAfter(a.header, u.now()))
	u.namesWait = u.now().Add(wait)
	u.log.Info("the project names didn't reach the server", "status", a.status, "retry_in", wait, "err", err)
	if a.status == http.StatusTooManyRequests {
		u.pace.drain(time.Now())
		return true, false
	}
	return false, false
}

// namesAnswered handles a names request's 200: its names are what the
// owner's dashboard shows now, and its codes what it calls the device's
// projects by, which replace the ones kept before. A name the device sent,
// in this request or before, that they lack, and that this request didn't
// delete, the owner hid on the dashboard: it goes off in config.toml
// (project-names-v1 2).
func (u *Uploader) namesAnswered(sent namesBody, hash string, a answer) bool {
	var reply namesReply
	if err := json.Unmarshal(a.body, &reply); err != nil || reply.Names == nil {
		u.namesHeld, u.namesAfter = hash, u.now().Add(namesHold)
		u.setProblem("names", "the server's answer about this computer's project names doesn't read")
		return false
	}
	u.namesBackoff.ok()
	got := make(map[string]string, len(reply.Names))
	for _, n := range reply.Names {
		if keyForm(n.Project) && projectname.ValidLabel(n.Label) {
			got[n.Project] = n.Label
		}
	}
	gone := make(map[string]string)
	for key, label := range u.names.Confirmed {
		if _, ok := got[key]; !ok && !slices.Contains(sent.Delete, key) {
			gone[key] = label
		}
	}
	mismatch := false
	for _, it := range sent.Set {
		switch label, ok := got[it.Project]; {
		case !ok:
			gone[it.Project] = it.Label
		case label != it.Label:
			mismatch = true
		}
	}
	now := u.now().UnixMilli()
	u.names.Confirmed, u.names.Codes, u.names.AnsweredAt = got, keptCodes(reply.Codes), now
	if len(got) == 0 {
		u.names.Confirmed = nil
	}
	for key := range got {
		// Shown again: the owner turned it on on the computer.
		delete(u.names.Hidden, key)
	}
	for _, key := range slices.Sorted(maps.Keys(gone)) {
		u.hidden(key, gone[key], now)
	}
	u.namesStarted = true
	u.saveNames()
	if mismatch {
		// The server shows another label than the one it was sent, which
		// sending it again wouldn't change: wait, rather than send it at
		// every wake.
		u.namesHeld, u.namesAfter = hash, u.now().Add(namesHold)
		u.setProblem("names", "the server answered with other project names than pairtime sent it")
		return true
	}
	u.setProblem("names", "")
	return true
}

// hidden notes that the owner hid key's name, which showed label, on the
// dashboard at now, and turns it off in config.toml. When that fails, the
// note is pending: the name isn't sent, and the uploader tries again at
// each wake (hidePending).
func (u *Uploader) hidden(key, label string, now int64) {
	note := HiddenName{Label: label, At: now}
	if u.opts.HideName == nil {
		note.Pending = true
	} else if err := u.opts.HideName(key); err != nil {
		note.Pending = true
		u.log.Warn("couldn't turn off in config.toml a project name hidden on the dashboard", "err", err)
	}
	if u.names.Hidden == nil {
		u.names.Hidden = make(map[string]HiddenName)
	}
	u.names.Hidden[key] = note
	for len(u.names.Hidden) > hiddenMax {
		oldest := ""
		for k, h := range u.names.Hidden {
			if oldest == "" || h.At < u.names.Hidden[oldest].At || (h.At == u.names.Hidden[oldest].At && k < oldest) {
				oldest = k
			}
		}
		delete(u.names.Hidden, oldest)
	}
	u.log.Info("a project name was hidden on the dashboard, so it's off in config.toml", "pending", note.Pending)
}

// namesRefused handles a names request the server refused for a reason the
// same body doesn't fix: a refused sign-in stops every request, as for an
// upload, and anything else holds the body for namesHold, while a change
// to the names in config.toml goes at once.
func (u *Uploader) namesRefused(c Credentials, hash string, a answer) (more bool) {
	code := a.refusal.Error
	if code == "" {
		code = fmt.Sprintf("status %d", a.status)
	}
	u.log.Warn("the server refused the project names", "code", code, "detail", a.refusal.Detail)
	switch code {
	case refusalMismatch:
		u.mismatched(c)
		return false
	case refusalTokenMissing, refusalTokenInvalid, refusalRevoked, refusalKeyMissing:
		u.blocked = c.Token
		u.setProblem("server", fmt.Sprintf("the server refused this computer's sign-in (%s), so pairtime uploads nothing until you run pairtime login again", code))
		return false
	case refusalTooManyNames:
		u.setProblem("names", "the server shows at most 1,000 project names for one computer, so turn some off with pairtime projects hide-name before the rest go")
	default:
		u.setProblem("names", fmt.Sprintf("the server refused this computer's project names as %s, so pairtime sends them again in an hour, or when you change them", code))
	}
	u.namesHeld, u.namesAfter = hash, u.now().Add(namesHold)
	// The upload goes on, at the pacer's pace.
	return true
}
