// SPDX-License-Identifier: AGPL-3.0-or-later

package uploader

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"net/http"
	"net/http/httptrace"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
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
// from its own keys (project-names-v1 1.6). The computer never sends a
// name on its own: a set goes only when the owner turns a name on or
// changes its label, and a delete when they turn it off. A name an answer
// lists as hidden, the owner hid on the dashboard: the uploader turns it
// off in config.toml, so it stays hidden until the owner turns it on again
// on the computer, and then tells the server with a delete. A name the
// server showed that an answer no longer lists at all, it dropped, as
// after the device's purge or a restore, and a name that is on when the
// computer signs in to another server or device was never shown there:
// both go off in config.toml too, each with a note, until the owner turns
// them on again. Whenever the uploader doesn't know what the server holds,
// it reads first, so nothing it sends undoes a hide. Only the labels the
// owner approved and the keys of the device's own projects go: nothing
// else about a project. Nothing goes before the server holds a batch from
// the device's link, so pairtime logout after a sign-in the wrong account
// approved still frees the device's id (project-names-v1 2).

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
	// Hides are the keys the server's last answer listed as hidden on the
	// dashboard: the server keeps each, without its label, until a delete
	// tells it the device turned the name off too (acks).
	Hides []string `json:"hides,omitempty"`
	// Sent are the names a set carried that may have reached the server,
	// each key's label, and Deleting the keys a delete carried: the uploader
	// writes them down before the request goes, and the next answer settles
	// them. A sent name the answer lists neither as a name nor as hidden
	// the server doesn't have, however it went, and goes off as dropped
	// rather than out again on its own (project-names-v1 2).
	Sent     map[string]string `json:"sent,omitempty"`
	Deleting []string          `json:"deleting,omitempty"`
}

// HiddenName is a name pairtime turned off in config.toml because the
// owner's dashboard doesn't show it: the owner hid it there, the server no
// longer has it, the computer signed in to another server or device, or
// project-names.json didn't read.
type HiddenName struct {
	// Label is the label config.toml had on when pairtime found it, or
	// else the one the dashboard showed or a set carried: a pending note's
	// label is what config.toml must change from before the name counts
	// as turned on again (hidePending).
	Label string `json:"label"`
	// At is when the uploader found it, in Unix ms.
	At int64 `json:"at"`
	// Reason says why: "" for a name the owner hid on the dashboard,
	// NoteDropped, NoteSignedIn and NoteUnreadable for the others.
	Reason string `json:"reason,omitempty"`
	// Pending is set while config.toml still has the name on, because
	// pairtime couldn't turn it off there: the uploader doesn't send it,
	// and tries again at each wake.
	Pending bool `json:"pending,omitempty"`
}

// The reasons a name went off other than a hide on the dashboard
// (HiddenName.Reason).
const (
	// NoteDropped is a name the server showed that an answer no longer
	// lists, as a name or as hidden: it dropped it, as after the device's
	// purge or a restore of its database, and pairtime can't tell which.
	NoteDropped = "dropped"
	// NoteSignedIn is a name that was on when the computer signed in to
	// another server, or as another device, which never showed it.
	NoteSignedIn = "signed_in"
	// NoteUnreadable is a name that was on when project-names.json didn't
	// read, so pairtime couldn't tell which sign-in the names were for.
	NoteUnreadable = "unreadable"
)

// ReadNames reads project-names.json in the data directory dataDir, while
// another process may replace it (home.ReadFile). A missing file is an
// error that wraps fs.ErrNotExist, and the device then knows of no names
// on the server, nor of any code. A code that isn't one
// counts for nothing, so what the file says can't put anything else where
// `pairtime projects` and the local dashboard show a code.
func ReadNames(dataDir string) (Names, error) {
	data, err := home.ReadFile(filepath.Join(dataDir, NamesFileName))
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
// server holds for the device after it, the keys of those the owner hid on
// the dashboard, and the short code of each project key the device
// uploaded minutes of, by key (project-names-v1 1.6). An answer without
// codes gives none, and one without hidden keys hides nothing.
type namesReply struct {
	Names  []nameItem        `json:"names"`
	Hidden []string          `json:"hidden"`
	Codes  map[string]string `json:"codes"`
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
// config.toml turns on, when it shows confirmed and lists acks as hidden on
// the dashboard: every name on whose label the server doesn't show, and
// every key it shows a name for, or lists as hidden, that is off, each in
// key order.
func namesDiff(wanted, confirmed map[string]string, acks []string) ([]nameItem, []string) {
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
	for _, key := range acks {
		_, on := wanted[key]
		if _, shown := confirmed[key]; !on && !shown && keyForm(key) {
			del = append(del, key)
		}
	}
	slices.Sort(del)
	return set, slices.Compact(del)
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
// owner's dashboard calls its projects by. None goes while the link's
// first batch isn't stored (Linking): a names request stores the device's
// ingest state as an upload does, after which pairtime logout no longer
// frees the device's id (device-auth-v1 1), and before that batch the
// server has no minutes to show a name on anyway.
func (u *Uploader) syncNames(c Credentials) (sent, more bool) {
	if u.opts.Names == nil {
		return false, false
	}
	wanted, ok := u.opts.Names()
	if !ok {
		return false, false
	}
	// The load records the sign-in, and turns off the names another
	// sign-in's file had on, before anything goes or waits.
	wanted = maps.Clone(wanted)
	u.loadNames(c, wanted)
	// It sends nothing: a name pairtime couldn't turn off, which the owner
	// turned off by hand, is no longer pending, so show-name takes it, even
	// while the link's first batch waits.
	u.hidePending(wanted)
	if u.st.Linking || u.now().Before(u.namesWait) {
		return false, false
	}
	set, del := namesDiff(wanted, u.names.Confirmed, u.acks())
	set = u.ownNames(set)
	change := takeNames(set, del)
	cb, err := buildNames(change)
	if err != nil {
		u.setProblem("names", fmt.Sprintf("pairtime can't build its project names request: %v", err))
		return false, false
	}
	// For a day after a 404 the server is taken to have no names endpoint
	// (namesMissing), and for an hour after a refusal the same request
	// would be refused again (namesRefused): nothing goes until config.toml
	// asks for something new, and then a read first, since the device
	// doesn't know what the server holds meanwhile.
	if (u.now().Before(u.missingUntil) && cb.hash == u.missingHash) || u.held(cb.hash) {
		return false, false
	}
	body, b := change, cb
	switch {
	case !u.namesStarted:
		// The device doesn't know what the server holds, as at the start,
		// after a request whose outcome it never learned, or after a sign-in:
		// a read first, so the diff after its answer can't undo a hide made
		// meanwhile.
		body = namesBody{}
		if b, err = buildNames(body); err != nil {
			u.setProblem("names", fmt.Sprintf("pairtime can't build its project names request: %v", err))
			return false, false
		}
	case len(set)+len(del) > 0:
	case readDue(u.names.AnsweredAt, u.now()):
		// The day's read: its answer says which names the owner hid, and
		// gives the codes.
	default:
		return false, false
	}
	return u.sendNames(c, body, b, cb.hash, wanted)
}

// builtNames is a names request's body as it goes: its JSON before gzip,
// the gzip stream, and the stream's SHA-256 in hex.
type builtNames struct {
	js, raw []byte
	hash    string
}

// buildNames encodes and compresses body.
func buildNames(body namesBody) (builtNames, error) {
	js, err := encodeNames(body)
	if err != nil {
		return builtNames{}, err
	}
	raw, err := compress(js)
	if err != nil {
		return builtNames{}, err
	}
	return builtNames{js: js, raw: raw, hash: bodyHash(raw)}, nil
}

// held reports whether what config.toml asks to send, whose body has
// hash, waits, held after a refusal, until namesAfter.
func (u *Uploader) held(hash string) bool {
	return hash == u.namesHeld && u.now().Before(u.namesAfter)
}

// loadNames reads project-names.json for the sign-in c, the first time and
// after each new sign-in, which brings a new token, so it finds what
// pairtime login wrote there meanwhile. When the file
// names another sign-in, that server showed the names, and this one never
// did: every name config.toml has on, and every name the old server
// showed, goes off in config.toml with a NoteSignedIn note, out of
// wanted, until the owner turns it on again here (project-names-v1 2).
// pairtime login does the same as it signs in (SignedInElsewhere), so this
// finds nothing on unless the sign-in came another way. Only a file that
// names no sign-in, as before a computer's first link, lets the names
// turned on before it go. The notes the file had stay, since they describe
// config.toml.
func (u *Uploader) loadNames(c Credentials, wanted map[string]string) {
	if u.names != nil && u.names.Server == c.Server && u.names.Device == c.DeviceID && u.namesToken == c.Token {
		return
	}
	n, err := ReadNames(u.opts.DataDir)
	unreadable := err != nil && !errors.Is(err, fs.ErrNotExist)
	if unreadable {
		u.log.Warn("project-names.json doesn't read, so pairtime can't tell which sign-in the names were for, and turns off the ones that are on", "err", err)
	}
	named := n.Server != "" && n.Server == c.Server && n.Device == c.DeviceID
	// A file that doesn't read counts as another sign-in's: pairtime
	// can't tell, so it fails closed.
	elsewhere := unreadable || (n.Server != "" && !named)
	old := n.Confirmed
	if !named {
		n = Names{Hidden: n.Hidden}
	}
	n.V, n.Server, n.Device = namesVersion, c.Server, c.DeviceID
	u.names, u.namesToken = &n, c.Token
	u.namesStarted = false
	u.namesKey, u.namesHash, u.namesHeld = "", "", ""
	u.missingUntil, u.missingHash = time.Time{}, ""
	if elsewhere {
		reason := NoteSignedIn
		if unreadable {
			reason = NoteUnreadable
		}
		now := u.now().UnixMilli()
		off := maps.Clone(old)
		if off == nil {
			off = make(map[string]string)
		}
		maps.Copy(off, wanted)
		for _, key := range slices.Sorted(maps.Keys(off)) {
			if keyForm(key) {
				u.hidden(key, off[key], reason, now)
			}
			// A name pairtime couldn't turn off stays in wanted for
			// hidePending, which tries again and keeps it out of what goes.
			if h, ok := u.names.Hidden[key]; !ok || !h.Pending {
				delete(wanted, key)
			}
		}
		if len(off) > 0 {
			u.log.Info("the project names that were on are off until you turn them on again here, since they were for another sign-in, or pairtime couldn't tell", "reason", reason, "names", len(off))
		}
	}
	if !named {
		// The file names this sign-in from now on, before any answer, so a
		// sign-in to another server later finds the names were this one's.
		u.saveNames()
	}
}

// noteSending writes down, before the request goes, the names body sets
// and the keys it deletes, so a crash or a lost answer leaves them known
// (Names.Sent). When project-names.json can't be written, it changes
// nothing and returns the error, and the request doesn't go.
func (u *Uploader) noteSending(body namesBody) error {
	if len(body.Set)+len(body.Delete) == 0 {
		return nil
	}
	sent, deleting := maps.Clone(u.names.Sent), slices.Clone(u.names.Deleting)
	if u.names.Sent == nil {
		u.names.Sent = make(map[string]string)
	}
	for _, it := range body.Set {
		u.names.Sent[it.Project] = it.Label
	}
	for _, key := range body.Delete {
		if !slices.Contains(u.names.Deleting, key) {
			u.names.Deleting = append(u.names.Deleting, key)
		}
	}
	if err := u.saveNames(); err != nil {
		u.names.Sent, u.names.Deleting = sent, deleting
		return err
	}
	return nil
}

// forgetSending takes back what noteSending wrote down for body, which
// never reached the server.
func (u *Uploader) forgetSending(body namesBody) {
	if len(body.Set)+len(body.Delete) == 0 {
		return
	}
	for _, it := range body.Set {
		delete(u.names.Sent, it.Project)
	}
	u.names.Deleting = slices.DeleteFunc(u.names.Deleting, func(key string) bool { return slices.Contains(body.Delete, key) })
	if len(u.names.Sent) == 0 {
		u.names.Sent = nil
	}
	if len(u.names.Deleting) == 0 {
		u.names.Deleting = nil
	}
	u.saveNames()
}

// acks returns the keys the server lists as hidden on the dashboard whose
// names config.toml has off now: their deletes tell the server the device
// turned them off too, so it forgets them. A name pairtime couldn't turn
// off there (HiddenName.Pending) stays hidden on the server until it
// could.
func (u *Uploader) acks() []string {
	var out []string
	for _, key := range u.names.Hides {
		if !u.names.Hidden[key].Pending {
			out = append(out, key)
		}
	}
	return out
}

// ownNames returns the names of set whose keys are the device's own: a key
// the server gave a code, which it holds minutes of from this device, or
// one of the projects the computer has seen (Options.OwnKeys). Another key,
// as in a config.toml copied from another computer, never goes, and doctor
// hears which (project-names-v1 2).
func (u *Uploader) ownNames(set []nameItem) []nameItem {
	if len(set) == 0 || u.opts.OwnKeys == nil {
		u.setProblem("names-elsewhere", "")
		return set
	}
	var own map[string]bool
	asked := false
	var out []nameItem
	var other []string
	for _, it := range set {
		_, coded := u.names.Codes[it.Project]
		if !coded && !asked {
			own, asked = u.opts.OwnKeys(), true
		}
		if coded || own[it.Project] {
			out = append(out, it)
		} else {
			other = append(other, it.Project)
		}
	}
	if len(other) == 0 {
		u.setProblem("names-elsewhere", "")
	} else {
		u.setProblem("names-elsewhere", fmt.Sprintf("config.toml turns on the names of projects this computer hasn't worked in, as when it was copied from another computer, so pairtime doesn't send them: %s", strings.Join(other, ", ")))
	}
	return out
}

// saveNames writes project-names.json.
func (u *Uploader) saveNames() error {
	data, err := json.Marshal(u.names)
	if err == nil {
		err = home.WritePrivate(filepath.Join(u.opts.DataDir, NamesFileName), append(data, '\n'))
	}
	if err != nil {
		u.log.Warn("couldn't save project-names.json", "err", err)
	}
	return err
}

// hidePending turns off in config.toml the names pairtime couldn't turn
// off there when it found that the dashboard doesn't show them (a pending
// HiddenName), and takes out of wanted those still on, which aren't sent.
// A name whose label config.toml changed since is one the owner turned on
// again, which goes: its note stays, no longer pending, so an answer that
// still lists the hide it is of, before the set goes, doesn't turn the
// name off again (namesAnswered).
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
			h.Pending = false
			u.names.Hidden[key] = h
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
	u.setProblem("hidden", fmt.Sprintf("your pairtime dashboard doesn't show project names that pairtime couldn't turn off in config.toml, so it doesn't send them again; to keep them off, write each in config.toml as a table of its own, [projects.<key>] with its name and online = false: %s", strings.Join(stuck, ", ")))
}

// sendNames sends a names request, its body raw with hash, signed again
// for each try that a clock skew or a changed body asks for, and handles
// its answer. change is the hash of what config.toml asked to send then,
// which a 404 holds back for a day (namesMissing) and a refusal for an
// hour (namesRefused), and wanted the names config.toml had on, for the
// answer's notes (namesAnswered). It reports whether a
// request reached the server, which is then the cycle's, and whether more
// goes at once, as the upload after a 200. A failure to reach the server
// reports none, so the cycle goes on to the upload (namesFailed).
func (u *Uploader) sendNames(c Credentials, body namesBody, b builtNames, change string, wanted map[string]string) (sent, more bool) {
	raw, hash := b.raw, b.hash
	if u.namesKey == "" || u.namesHash != hash {
		// A new body, a new key; a retry of the same one keeps it, as an
		// upload's does (ingest-v1 2.10).
		u.namesKey, u.namesHash = event.NewEventID(u.now()), hash
	}
	for try := 0; try < 3; try++ {
		if try > 0 && !u.namesStarted && len(body.Set)+len(body.Delete) > 0 {
			// The last try was refused, or its answer may have been the
			// transport's resend's: the next cycle reads first.
			return true, false
		}
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
		if err := u.noteSending(body); err != nil {
			// Unrecorded, a set whose answer was lost could bring back a
			// name hidden meanwhile: nothing goes until the file is written.
			u.setProblem("names", fmt.Sprintf("pairtime can't write project-names.json, so it sends no project name until it can: %v", err))
			u.namesWait = u.now().Add(Every)
			return false, false
		}
		// The tries the transport wrote in full: it sends a request again
		// itself when a reused connection drops before the answer, so with
		// more than one the answer may be the second's.
		var writes atomic.Int32
		trace := &httptrace.ClientTrace{WroteRequest: func(w httptrace.WroteRequestInfo) {
			if w.Err == nil {
				writes.Add(1)
			}
		}}
		a, err := u.do(httptrace.WithClientTrace(u.ctx, trace), hr)
		if !errors.Is(err, context.Canceled) {
			u.recordNames(pastNames(u.now(), c.Server, body, b.js, a, err))
		}
		switch n := writes.Load(); {
		case n == 0 || (n == 1 && err == nil && storedNothing(a)):
			// It never reached the server, or the server stored none of it.
			u.forgetSending(body)
		case n > 1 && (err != nil || a.status != http.StatusOK):
			// The first try may have been stored, whatever this answer says,
			// so the next request reads first.
			u.namesStarted = false
		}
		switch {
		case err != nil:
		case a.status == http.StatusNotFound:
			return u.namesMissing(change, a)
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
			return true, u.namesAnswered(body, change, wanted, a)
		case a.refusal.Error == refusalClockSkew:
			// The answer set the clock's offset. A read goes again at once;
			// a set or a delete, after a read, so a hide made meanwhile
			// stays (the guard above).
			u.namesStarted = false
			continue
		case a.refusal.Error == refusalCounter:
			// The state read sets the counter above the server's, and the
			// next cycle sends the names again (ingest-v1 5.1). The counter
			// may be this request's own, taken by a try whose answer was
			// lost, as when the transport sends a request again after a
			// connection dropped, so the next request is a read.
			u.namesStarted = false
			u.missingUntil, u.missingHash = time.Time{}, ""
			return true, u.sync(c)
		case a.refusal.Error == refusalBodyHash:
			// The body changed on the way: as for a clock skew.
			u.namesStarted = false
			continue
		case a.refusal.Error == refusalKeyReused:
			u.namesKey = event.NewEventID(u.now())
			u.namesStarted = false
			continue
		case a.refusal.Error == refusalBodyUnreadable:
			return u.namesFailed(errBodyUnreadable, a)
		}
		return true, u.namesRefused(c, change, a)
	}
	return true, u.namesRefused(c, change, answer{refusal: Refusal{Error: "retries"}})
}

// storedNothing reports whether answer a, to a request the transport
// wrote once, says the server stored none of it: a 404, from a server
// without the endpoint or a proxy that doesn't route it; a 429, which
// comes before anything is stored; or a refusal of pairtimed's, which
// rolls back all of a request.
func storedNothing(a answer) bool {
	return a.status == http.StatusNotFound || a.status == http.StatusTooManyRequests || (fromPairtimed(a) && a.status/100 == 4)
}

// namesMissing handles a 404 to a names request, which pairtimed's names
// endpoint never answers: a server without the endpoint, as an older one,
// whose own ServeMux answers it without X-Pairtime-Time, or a proxy that
// doesn't route it (project-names-v1 1.5). It is no refusal to act on:
// doctor hears nothing, and the cycle goes on to the upload, which it
// never holds back. For a day, the daily read's pace, nothing goes while
// config.toml asks for what it asked for then, change, the hash of that
// diff's body. A change to the names there goes at once, as a read first
// while the device doesn't know what the server holds, so a server that
// never gets the endpoint hears from the names once a day and at each
// change, and a set whose answer was lost never goes again unread.
func (u *Uploader) namesMissing(change string, a answer) (sent, more bool) {
	u.missingUntil, u.missingHash = u.now().Add(namesReadEvery), change
	// The owner may hide a name meanwhile, so what waited goes after a read.
	u.namesStarted = false
	u.log.Info("the server has no project names endpoint, so pairtime asks again in a day or when the names change", "status", a.status)
	return false, false
}

// namesFailed handles a names request that didn't reach the server, got
// an answer that isn't pairtimed's, or got a 429 or a 5xx: the names wait
// as an upload would, with full jitter on a backoff of their own and the
// server's Retry-After, and are sent again once it ends, and doctor hears
// nothing of it. They leave the upload's backoff and its upload-failure
// notes alone: the cycle goes on to the upload, which tries the server
// itself, so a server whose names endpoint fails never holds back an
// upload. After a 429 the cycle ends instead, since the server's bucket
// for the device ran dry. Any of them may have come after the server
// stored the request, as a timeout after it committed: the next request is
// a read, so the diff after it can't undo a hide made meanwhile.
func (u *Uploader) namesFailed(err error, a answer) (sent, more bool) {
	if errors.Is(err, context.Canceled) {
		return true, false // the agent is stopping
	}
	wait := u.namesBackoff.fail(retryAfter(a.header, u.now()))
	u.namesWait = u.now().Add(wait)
	u.log.Info("the project names didn't reach the server", "status", a.status, "retry_in", wait, "err", err)
	// The outcome is unknown, a 429 included, which may answer the
	// transport's own resend: the next request reads first, and a 404's
	// hold no longer counts.
	u.namesStarted = false
	u.missingUntil, u.missingHash = time.Time{}, ""
	if a.status == http.StatusTooManyRequests {
		u.pace.drain(time.Now())
		return true, false
	}
	return false, false
}

// namesAnswered handles a names request's 200: its names are what the
// owner's dashboard shows now, and its codes what it calls the device's
// projects by, which replace the ones kept before. wanted is what
// config.toml had on as the request went. A key it lists as hidden, the
// owner hid on the dashboard: its name goes off in config.toml
// (project-names-v1 2), and the next request's delete tells the server
// so. A name config.toml still has on that the server showed before, or
// that a set carried (Names.Sent), and that the answer neither shows nor
// lists as hidden, the server doesn't have, as after the device's purge,
// a restore of its database, or a lost answer whose hide was purged: it
// goes off in config.toml too, with a NoteDropped note, and is never sent
// again until the owner turns it on.
func (u *Uploader) namesAnswered(sent namesBody, change string, wanted map[string]string, a answer) bool {
	// The endpoint answered, so a 404's day ends.
	u.missingUntil, u.missingHash = time.Time{}, ""
	var reply namesReply
	if err := json.Unmarshal(a.body, &reply); err != nil || reply.Names == nil {
		// The request may have been stored, and the answer says nothing:
		// as after a failure to reach the server, the next request reads
		// first, once the names' backoff ends.
		u.namesWait = u.now().Add(u.namesBackoff.fail(retryAfter(a.header, u.now())))
		u.namesStarted = false
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
	hid := make(map[string]bool, len(reply.Hidden))
	for _, key := range reply.Hidden {
		if _, shown := got[key]; keyForm(key) && !shown {
			hid[key] = true
		}
	}
	// The label each hidden name showed: the one the server showed
	// before, or the one this request sent.
	showed := maps.Clone(u.names.Confirmed)
	if showed == nil {
		showed = make(map[string]string)
	}
	mismatch := false
	for _, it := range sent.Set {
		showed[it.Project] = it.Label
		if label, ok := got[it.Project]; !ok || label != it.Label {
			mismatch = true
		}
	}
	// A set that may have reached the server since the last answer: what
	// noteSending wrote down, this request's sets included.
	setSince := make(map[string]bool, len(u.names.Sent))
	for key, label := range u.names.Sent {
		setSince[key] = true
		if _, ok := showed[key]; !ok {
			showed[key] = label
		}
	}
	// A note keeps the label config.toml has on, which show-name shows
	// again and a pending note compares with (hidePending), or else the
	// one the server showed or was sent.
	noteLabel := func(key string) string { return cmp.Or(wanted[key], showed[key]) }
	var dropped []string
	for key := range u.names.Confirmed {
		// Only a name config.toml still has on: one turned off, whose
		// delete may not have gone yet, went as the owner asked.
		_, on := wanted[key]
		if _, ok := got[key]; on && !ok && !hid[key] && !slices.Contains(sent.Delete, key) && !slices.Contains(u.names.Deleting, key) {
			dropped = append(dropped, key)
		}
	}
	for key := range u.names.Sent {
		// A name a set carried that the server neither shows nor lists as
		// hidden: it doesn't have it, as after a lost answer and the hide's
		// 30 days, or a removed device's hide. Off, not out again, unless
		// the owner turned it off since.
		_, on := wanted[key]
		if _, ok := got[key]; on && !ok && !hid[key] && !slices.Contains(dropped, key) {
			dropped = append(dropped, key)
		}
	}
	u.names.Sent, u.names.Deleting = nil, nil
	now := u.now().UnixMilli()
	u.names.Confirmed, u.names.Codes, u.names.AnsweredAt = got, keptCodes(reply.Codes), now
	if len(got) == 0 {
		u.names.Confirmed = nil
	}
	u.names.Hides = slices.Sorted(maps.Keys(hid))
	if len(hid) == 0 {
		u.names.Hides = nil
	}
	for key := range got {
		// Shown, so a set of it reached the server after the note, and any
		// hide of it is gone: a note of a name config.toml has on is out of
		// date, whatever the label. A note of a name config.toml has off
		// stays, as one pairtime turned off for another sign-in, while its
		// delete goes, and a pending note's key is never in wanted
		// (hidePending).
		if _, on := wanted[key]; on {
			delete(u.names.Hidden, key)
		}
	}
	for _, key := range u.names.Hides {
		// Only a hide's own note, with no set that may have reached the
		// server since, is of this hide, which the owner may have turned
		// the name on again over. After such a set, as after show-name and
		// a lost answer, the set deleted that hide, so this one is new; and
		// a note made without a hide, at a sign-in, for a file that didn't
		// read or for a name the server dropped, is of no hide at all:
		// either way the name goes off again.
		h, noted := u.names.Hidden[key]
		if !noted || (!h.Pending && (setSince[key] || h.Reason != "")) {
			u.hidden(key, noteLabel(key), "", now)
		}
	}
	slices.Sort(dropped)
	for _, key := range dropped {
		// The server no longer has it, and pairtime can't tell a purge from
		// a restore: off here too, never sent again on its own.
		u.hidden(key, noteLabel(key), NoteDropped, now)
	}
	if len(dropped) > 0 {
		u.log.Info("the server no longer shows project names it showed, as after the device's purge or a restore, so pairtime turned them off here until you turn them on again", "names", len(dropped))
	}
	u.namesStarted = true
	u.saveNames()
	if mismatch {
		// The server shows another label than the one it was sent, which
		// sending it again wouldn't change: wait, rather than send it at
		// every wake, and read first once the hour ends, since the owner
		// may hide the name meanwhile.
		u.namesHeld, u.namesAfter = change, u.now().Add(namesHold)
		u.namesStarted = false
		u.setProblem("names", "the server answered with other project names than pairtime sent it")
		return true
	}
	u.setProblem("names", "")
	return true
}

// hidden notes that the owner's dashboard doesn't show key's name, which
// showed label, for reason at now, and turns the name off in config.toml.
// When that fails, the note is pending: the name isn't sent, and the
// uploader tries again at each wake (hidePending).
func (u *Uploader) hidden(key, label, reason string, now int64) {
	note := HiddenName{Label: label, At: now, Reason: reason}
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
	capNotes(u.names.Hidden)
	u.log.Info("a project name your dashboard doesn't show is off in config.toml", "reason", cmp.Or(reason, "hidden"), "pending", note.Pending)
}

// capNotes lets the oldest notes go past hiddenMax.
func capNotes(hidden map[string]HiddenName) {
	for len(hidden) > hiddenMax {
		oldest := ""
		for k, h := range hidden {
			if oldest == "" || h.At < hidden[oldest].At || (h.At == hidden[oldest].At && k < oldest) {
				oldest = k
			}
		}
		delete(hidden, oldest)
	}
}

// SignedInElsewhere is what `pairtime login` does as it links this
// computer to server as device, and what `pairtime projects show-name`,
// hide-name and the local app's switch do before the owner's change on a
// linked computer, when login couldn't (project-names-v1 2). When the data
// directory dataDir's project-names.json names another sign-in, that
// server showed the names and this one never did: every name on, which
// config.toml has on by project key with its label, and every name the old
// server showed goes off in config.toml through hide, each with a note of
// why, and the file is the new sign-in's from then on, with its notes, as
// the uploader's own load leaves it. A file that exists but doesn't read
// counts as another sign-in's, since pairtime can't tell (NoteUnreadable).
// A name config.toml has on that hide can't turn off gets a pending note,
// so the uploader never sends it. It returns what it turned off, for the
// caller to list. A file
// that names no sign-in, or none at all, as before the computer's first
// link, gets this sign-in written into it, its notes kept, and nothing
// goes off: the names turned on before it go, as show-name said they
// would.
func SignedInElsewhere(dataDir, server, device string, on map[string]string, hide func(key string) error, now time.Time) (Elsewhere, error) {
	n, err := ReadNames(dataDir)
	unreadable := err != nil && !errors.Is(err, fs.ErrNotExist)
	switch {
	case !unreadable && n.Server == server && n.Device == device:
		return Elsewhere{}, nil
	case !unreadable && n.Server == "":
		return Elsewhere{}, writeNames(dataDir, Names{V: namesVersion, Server: server, Device: device, Hidden: n.Hidden})
	}
	reason := NoteSignedIn
	if unreadable {
		reason, n = NoteUnreadable, Names{}
	}
	off := maps.Clone(n.Confirmed)
	if off == nil {
		off = make(map[string]string)
	}
	maps.Copy(off, on)
	maps.DeleteFunc(off, func(key, _ string) bool { return !keyForm(key) })
	hidden := n.Hidden
	if hidden == nil {
		hidden = make(map[string]HiddenName)
	}
	at := now.UnixMilli()
	e := Elsewhere{Off: off, Reason: reason}
	for _, key := range slices.Sorted(maps.Keys(off)) {
		note := HiddenName{Label: off[key], At: at, Reason: reason}
		// A failed hide of a name config.toml has off already changes
		// nothing: only one it has on stays out pending.
		if _, isOn := on[key]; hide(key) != nil && isOn {
			note.Pending = true
			e.Stuck = append(e.Stuck, key)
		}
		hidden[key] = note
	}
	capNotes(hidden)
	next := Names{V: namesVersion, Server: server, Device: device, Hidden: hidden}
	if len(hidden) == 0 {
		next.Hidden = nil
	}
	if err := writeNames(dataDir, next); err != nil {
		return Elsewhere{}, err
	}
	return e, nil
}

// Elsewhere is what SignedInElsewhere turned off for a sign-in: each
// name's label by project key, the keys of those config.toml still has on
// in a form pairtime couldn't change, which go nowhere all the same (a
// pending HiddenName), in key order, and why, NoteSignedIn or
// NoteUnreadable.
type Elsewhere struct {
	Off    map[string]string
	Stuck  []string
	Reason string
}

// RecordSignIn writes the sign-in server and device into the data
// directory dataDir's project-names.json when the file names no sign-in,
// or there is none, keeping its notes, as `pairtime projects show-name`
// and hide-name do on a linked computer: a sign-in to another server later
// finds the names were this one's (SignedInElsewhere). A file that names a
// sign-in, or doesn't read, stays as it is, for the uploader's load.
func RecordSignIn(dataDir, server, device string) error {
	n, err := ReadNames(dataDir)
	if (err != nil && !errors.Is(err, fs.ErrNotExist)) || n.Server != "" {
		return nil
	}
	return writeNames(dataDir, Names{V: namesVersion, Server: server, Device: device, Hidden: n.Hidden})
}

// writeNames writes n as dataDir's project-names.json, user-only.
func writeNames(dataDir string, n Names) error {
	data, err := json.Marshal(n)
	if err != nil {
		return err
	}
	return home.WritePrivate(filepath.Join(dataDir, NamesFileName), append(data, '\n'))
}

// namesRefused handles a names request the server refused for a reason the
// same body doesn't fix: a refused sign-in stops every request, as for an
// upload, and anything else holds the body for namesHold, while a change
// to the names in config.toml goes at once.
func (u *Uploader) namesRefused(c Credentials, change string, a answer) (more bool) {
	// The endpoint answered, so a 404's day ends. The owner may hide a name
	// while the request waits, so it goes again after a read.
	u.missingUntil, u.missingHash = time.Time{}, ""
	u.namesStarted = false
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
	u.namesHeld, u.namesAfter = change, u.now().Add(namesHold)
	// The upload goes on, at the pacer's pace.
	return true
}
