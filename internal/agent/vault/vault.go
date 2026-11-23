// SPDX-License-Identifier: AGPL-3.0-or-later

// Package vault keeps the content the ledger commits to, encrypted, so a
// reveal can open it later (DESIGN 3.5, 3.6; docs/PLAN.md, WP-LG6): the
// text each record commits, which is a prompt as the harness delivered it,
// a turn's final agent message cut to its first 16 KiB, or a track label
// (ledger-v1 10.1), and the file paths of the agent's edits, for local
// recaps. The ledger holds only commitments and keyed features; the vault
// is what gets revealed (EV 3.4).
//
// # Files
//
// The vault directory, home.Dirs.Vault(), holds YYYY-MM-DD.seg, one per UTC
// day the ledger writer wrote content in, beside the ledger segment of the
// same name, which holds the records whose content it keeps. A file is a
// run of frames, as a ledger file is (frame.go): a head frame,
// {"v":1,"key_id":...}, naming the device key whose K_vault sealed the
// file's entries, then one frame per entry:
//
//	entry = u64(s) || u8(what) || nonce || AES-256-GCM(K_vault, nonce, content, ad = u64(s) || u8(what))
//
// s is the ledger sequence number of the record the content belongs to,
// what is 't' for the text the record commits and 'p' for the paths of
// its edits, as a JSON array of strings, and the nonce is 12 random bytes,
// drawn for each entry. The associated data binds an entry to its record's
// number and to what it holds, so no entry opens as another record's, or
// as paths where text was kept (DESIGN 3.5; WP-LG6).
//
// old/<key id>/ holds the files of a device key this installation no
// longer writes with, as the ledger's old/ holds that key's chain. Only
// the key they were sealed under opens them, as an archived key restored
// with `pairtime restore` does (DESIGN 7.11).
//
// # Numbers
//
// The ledger writer keeps each record's content right after it appends
// the record, under the lock its records take, so the entries are in the
// order of their numbers, and each file's day is its record's segment's,
// which never goes back (Keep). A crash can cut records no batch holds
// yet, and after the restart the next records take their numbers
// (internal/agent/ledger). So at its Open, after it has cut what the crash
// left, the writer calls Reconcile with the number its next record takes,
// before any record takes it, and the vault cuts every entry from that
// number on: no record holds those numbers now, and an entry left for one
// would pass for the content of the next record to take it. A reveal
// still checks the text it opens against the record's commitment (Text).
//
// # Durability
//
// An entry is written with one write call and no flush, as a record is.
// The writer calls Sync before it flushes a batch, so the content of a
// sealed record is on disk once its batch is. A crash between the two
// leaves an entry whose record the next Open cuts, and Reconcile cuts the
// entry with it.
//
// # Encryption
//
// The vault's encryption protects against other users of a family PC,
// cloud sync of the home folder and casual reading, and deleting K_dev
// makes every entry unreadable. It doesn't protect against malware
// running as the same user, which can read K_dev from its store (DESIGN
// 3.5; CP 5.2).
package vault

import (
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/some-du6e/pairtime/internal/agent/home"
	"github.com/some-du6e/pairtime/pkg/ledger"
)

// What an entry holds.
const (
	whatText  byte = 't' // the text the record commits (ledger-v1 10.1)
	whatPaths byte = 'p' // the paths of the record's edits, for recaps
)

// headVersion is the version of the head frame's payload.
const headVersion = 1

// segSuffix ends every day file's name, YYYY-MM-DD.seg (DESIGN 3.5).
const segSuffix = ".seg"

// oldDir holds the files of device keys this installation no longer
// writes with, each in a directory named after its key's id.
const oldDir = "old"

// entryMin is the shortest entry payload: its number, what it holds, its
// nonce and a tag.
const entryMin = 8 + 1 + nonceSize + tagSize

// ErrNotKept means the vault has no such entry: the record commits no
// text, or has no paths; "keep prompt text" was off when it was written;
// or the entry was deleted since, by its retention, by the teen turning
// "keep prompt text" off, or because no record held its number after a
// crash. A reveal answers expired for such a text (DESIGN 7.5 step 8).
var ErrNotKept = errors.New("vault: the vault keeps no such entry")

// Options configure Open.
type Options struct {
	// Dir is the vault directory, home.Dirs.Vault(), an absolute path. Open
	// makes it.
	Dir string
	// Keys are the ledger keys of this installation's K_dev, from the
	// device keystore. The vault seals with K_vault (ledger-v1 3) and
	// checks a text against its record's commitment with K_tok.
	Keys *ledger.Keys
	// KeyID is K_dev's public id, keys.Status.KeyID. It names whose
	// K_vault sealed a file.
	KeyID string
	// KeepText is "keep prompt text" as config.toml says when the vault
	// opens (DESIGN 4.4). SetKeepText changes it.
	KeepText bool
	// Problem gets a description of what went wrong when the vault can't
	// keep or flush an entry, or deletes a file it couldn't read, and ""
	// once a later write succeeds. Keep calls it under the ledger writer's
	// record lock, so it must be quick and take no lock that is held
	// around a call of the writer or the vault. Nil drops them.
	Problem func(msg string)
}

// Vault is a device's vault. Its methods are safe for concurrent use.
type Vault struct {
	dir     string
	keyID   string
	keys    *ledger.Keys
	gcm     *gcm
	problem func(string)

	keepText atomic.Bool

	// files is held for writing while Reconcile, a rewrite or Close
	// replaces or deletes files, and for reading while Text or Paths reads
	// one: Windows can neither rename over a file nor delete one that a
	// handle has open. syncMu is held while Sync flushes cur, and while the
	// newest file, which Keep appends to, is replaced or deleted. Both are
	// taken before mu, files before syncMu, and Keep takes neither.
	files  sync.RWMutex
	syncMu sync.Mutex

	mu     sync.Mutex
	ready  bool   // Reconcile has run
	off    error  // why the vault keeps nothing more in this run
	closed bool   // Close has run
	last   uint64 // s of the newest entry, 0 for none
	newest string // the newest file's day, "" for none
	cur    *os.File
	curDay string
	size   int64 // cur's size
	dirty  bool  // cur has writes no flush covers
	failed bool  // the last write or flush reported a problem
}

// Open opens the vault in opts.Dir. Files another device key sealed go to
// old/<its key id>, and a file whose first frame isn't a whole head holds
// no entry anyone can open, so it is deleted. The vault keeps nothing
// until Reconcile has run.
func Open(opts Options) (*Vault, error) {
	if !filepath.IsAbs(opts.Dir) {
		return nil, fmt.Errorf("vault: the vault directory must be an absolute path, not %q", opts.Dir)
	}
	if opts.Keys == nil {
		return nil, errors.New("vault: Open needs the device's ledger keys")
	}
	if !isKeyID(opts.KeyID) {
		return nil, errors.New("vault: the key id must be 16 to 64 lowercase hex digits")
	}
	key := opts.Keys.VaultKey()
	g, err := newGCM(key[:])
	clear(key[:])
	if err != nil {
		return nil, err
	}
	v := &Vault{
		dir:     filepath.Clean(opts.Dir),
		keyID:   opts.KeyID,
		keys:    opts.Keys,
		gcm:     g,
		problem: opts.Problem,
	}
	v.keepText.Store(opts.KeepText)
	if err := os.MkdirAll(v.dir, 0o700); err != nil {
		return nil, err
	}
	if err := v.setAside(); err != nil {
		return nil, err
	}
	return v, nil
}

// isKeyID reports whether s can be a key id: 16 to 64 lowercase hex
// digits, which also makes it safe as a directory name.
func isKeyID(s string) bool {
	if len(s) < 16 || len(s) > 64 {
		return false
	}
	for i := 0; i < len(s); i++ {
		if c := s[i]; !('0' <= c && c <= '9' || 'a' <= c && c <= 'f') {
			return false
		}
	}
	return true
}

// head is a head frame's payload.
type head struct {
	V     int    `json:"v"`
	KeyID string `json:"key_id"`
}

// setAside moves the files another device key sealed to old/<key id>, and
// deletes the files that hold nothing anyone can open, and the temporary
// file of a rewrite a crash cut short (filterFile).
func (v *Vault) setAside() error {
	entries, err := os.ReadDir(v.dir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if n := e.Name(); strings.HasPrefix(n, ".") && strings.Contains(n, segSuffix+".tmp-") && e.Type().IsRegular() {
			if err := os.Remove(filepath.Join(v.dir, n)); err != nil {
				return err
			}
		}
	}
	days, err := segments(v.dir)
	if err != nil {
		return err
	}
	for _, day := range days {
		name := segPath(v.dir, day)
		keyID, err := readHead(name)
		switch {
		case err == nil && keyID == v.keyID:
			continue
		case err == nil:
			dst := filepath.Join(v.dir, oldDir, keyID)
			if err := os.MkdirAll(dst, 0o700); err != nil {
				return err
			}
			to := segPath(dst, day)
			if _, err := os.Lstat(to); err == nil {
				return fmt.Errorf("vault: %s and %s both hold the entries device key %s sealed on %s", name, to, keyID, day)
			}
			if err := os.Rename(name, to); err != nil {
				return err
			}
			home.SyncDir(dst)
			home.SyncDir(v.dir)
		case errors.Is(err, errTorn) || errors.Is(err, errHead):
			// No head, so no entry of the file can be told which key sealed
			// it: a crash cut the file's first write short, or the disk
			// damaged it.
			if rerr := os.Remove(name); rerr != nil {
				return rerr
			}
			home.SyncDir(v.dir)
			if !errors.Is(err, errEmpty) {
				v.report(fmt.Sprintf("the vault deleted %s, which held no whole entry: %v", filepath.Base(name), err))
			}
		default:
			return err
		}
	}
	return nil
}

// errHead is a file whose first whole frame isn't a head of this version.
var errHead = errors.New("vault: the file doesn't start with a head")

// errEmpty is a file with nothing in it, which a crash leaves when it
// comes between the file's creation and its first write.
var errEmpty = fmt.Errorf("%w: the file is empty", errTorn)

// readHead returns the key id the head of the file name names.
func readHead(name string) (string, error) {
	f, err := os.Open(name)
	if err != nil {
		return "", err
	}
	defer f.Close()
	fr, err := newFrameReader(f).next()
	switch {
	case errors.Is(err, io.EOF):
		return "", errEmpty
	case err != nil:
		return "", err
	}
	return parseHead(fr)
}

// parseHead returns the key id of a head frame.
func parseHead(fr frame) (string, error) {
	if fr.kind != kindHead {
		return "", fmt.Errorf("%w: its first frame is a %q frame", errHead, fr.kind)
	}
	var h head
	if err := json.Unmarshal(fr.payload, &h); err != nil {
		return "", fmt.Errorf("%w: %v", errHead, err)
	}
	if h.V != headVersion || !isKeyID(h.KeyID) {
		return "", fmt.Errorf("%w: version %d, key id %q", errHead, h.V, h.KeyID)
	}
	return h.KeyID, nil
}

// entry is an entry as a file holds it, still sealed.
type entry struct {
	s      uint64
	what   byte
	nonce  []byte
	sealed []byte
	end    int64 // where the frame after it starts
}

// parseEntry reads an entry frame.
func parseEntry(fr frame) (entry, error) {
	p := fr.payload
	if fr.kind != kindEntry {
		return entry{}, fmt.Errorf("a %q frame after the head", fr.kind)
	}
	if len(p) < entryMin {
		return entry{}, fmt.Errorf("an entry of %d bytes, shorter than %d", len(p), entryMin)
	}
	e := entry{
		s:      binary.BigEndian.Uint64(p),
		what:   p[8],
		nonce:  p[9 : 9+nonceSize],
		sealed: p[9+nonceSize:],
		end:    fr.end,
	}
	if e.s == 0 {
		return entry{}, errors.New("an entry for s = 0")
	}
	return e, nil
}

// ad returns an entry's associated data: u64(s) || u8(what).
func ad(s uint64, what byte) []byte {
	return append(binary.BigEndian.AppendUint64(nil, s), what)
}

// file is what reading a day file found: its whole entries, in order, up
// to the first frame that isn't one, where a crash's torn tail starts.
type file struct {
	day     string
	headEnd int64
	entries []entry
	size    int64
}

// readFile reads the day file name, which has a head of keyID's. Reading
// stops at the first frame that isn't a whole entry in order: of a higher
// number than the one before it, or of the same number and the other kind,
// since a record can have both a text and paths (Keep).
func readFile(name, day, keyID string) (file, error) {
	f, err := os.Open(name)
	if err != nil {
		return file{}, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return file{}, err
	}
	out := file{day: day, size: info.Size()}
	fr := newFrameReader(io.LimitReader(f, out.size))
	first, err := fr.next()
	if errors.Is(err, io.EOF) {
		err = errEmpty
	}
	if err != nil {
		return file{}, err
	}
	got, err := parseHead(first)
	if err != nil {
		return file{}, err
	}
	if got != keyID {
		return file{}, fmt.Errorf("vault: %s holds entries device key %s sealed", name, got)
	}
	out.headEnd = first.end
	for {
		fm, err := fr.next()
		switch {
		case errors.Is(err, io.EOF), errors.Is(err, errTorn):
			return out, nil
		case err != nil:
			return file{}, err
		}
		e, err := parseEntry(fm)
		if err != nil {
			return out, nil
		}
		if n := len(out.entries); n > 0 && (e.s < out.entries[n-1].s || e.s == out.entries[n-1].s && e.what == out.entries[n-1].what) {
			return out, nil
		}
		out.entries = append(out.entries, e)
	}
}

// isDay reports whether s is a day as the files name them.
func isDay(s string) bool {
	t, err := time.Parse(time.DateOnly, s)
	return err == nil && t.Format(time.DateOnly) == s
}

// segPath is the path of day's file in dir.
func segPath(dir, day string) string { return filepath.Join(dir, day+segSuffix) }

// segments lists the days of the files in dir, oldest first.
func segments(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var days []string
	for _, e := range entries {
		day, ok := strings.CutSuffix(e.Name(), segSuffix)
		if ok && isDay(day) && e.Type().IsRegular() {
			days = append(days, day)
		}
	}
	slices.Sort(days)
	return days, nil
}

// Reconcile cuts every entry from s = next on, and any frame a crash left
// half written, so that no entry stays for a number no ledger record
// holds. The ledger writer calls it at its Open, with the number its next
// record takes, before any record takes it (ledger.Options.Vault); the
// entries are in the order of their numbers, so only the newest files can
// hold such an entry. A file left with no entry is deleted. When Reconcile
// fails, the vault keeps nothing more in this run, so no entry it couldn't
// cut is ever followed by another of the same number.
func (v *Vault) Reconcile(next uint64) error {
	v.files.Lock()
	defer v.files.Unlock()
	v.syncMu.Lock()
	defer v.syncMu.Unlock()
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.closed {
		return ErrClosed
	}
	if v.ready {
		return errors.New("vault: Reconcile runs once, at the ledger writer's Open")
	}
	err := v.reconcile(next)
	if err != nil {
		v.off = fmt.Errorf("the vault keeps nothing until the agent starts again, because it couldn't cut the entries a crash left: %w", err)
		v.report(v.off.Error())
	}
	v.ready = true
	return err
}

// ErrClosed is a call on a Vault after Close.
var ErrClosed = errors.New("vault: the vault is closed")

// reconcile is Reconcile. Both locks are held.
func (v *Vault) reconcile(next uint64) error {
	days, err := segments(v.dir)
	if err != nil {
		return err
	}
	for len(days) > 0 {
		day := days[len(days)-1]
		name := segPath(v.dir, day)
		f, err := readFile(name, day, v.keyID)
		if errors.Is(err, errTorn) || errors.Is(err, errHead) {
			// Open deleted such a file; this one came since.
			if err := os.Remove(name); err != nil {
				return err
			}
			home.SyncDir(v.dir)
			days = days[:len(days)-1]
			continue
		}
		if err != nil {
			return err
		}
		keep := len(f.entries)
		for keep > 0 && f.entries[keep-1].s >= next {
			keep--
		}
		cut := f.headEnd
		if keep > 0 {
			cut = f.entries[keep-1].end
		}
		if keep == 0 {
			if err := os.Remove(name); err != nil {
				return err
			}
			home.SyncDir(v.dir)
			days = days[:len(days)-1]
			continue
		}
		if cut < f.size {
			if err := truncate(name, cut); err != nil {
				return err
			}
		}
		v.last = f.entries[keep-1].s
		v.newest = day
		return nil
	}
	v.last, v.newest = 0, ""
	return nil
}

// truncate cuts the file name to size and flushes it.
func truncate(name string, size int64) error {
	f, err := os.OpenFile(name, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	if err := f.Truncate(size); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// SetKeepText turns "keep prompt text" on or off for the entries kept from
// now on (DESIGN 4.4). Turning it off keeps what is kept already: DropText
// deletes that.
func (v *Vault) SetKeepText(on bool) { v.keepText.Store(on) }

// KeepsText reports whether the vault keeps the text of new records.
func (v *Vault) KeepsText() bool { return v.keepText.Load() }

// Keep keeps the content of record s, which the ledger writer has just
// appended to the segment of the UTC day day: text, the bytes the record
// commits, or nil for none, while "keep prompt text" is on, and paths, the
// paths of its edits, for recaps. The writer calls it under the lock its
// records take, in the order of their numbers, so it makes one write call
// and no flush, and never calls the writer. A problem goes to
// Options.Problem: the record stands without its content, which a reveal
// then answers as expired (DESIGN 7.5 step 8).
func (v *Vault) Keep(day string, s uint64, text []byte, paths []string) {
	paths = distinct(paths)
	if text == nil && len(paths) == 0 {
		return
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	// Read under the lock DropText holds, so no text kept while the switch
	// was on can land after DropText has run.
	keepText := text != nil && v.keepText.Load()
	if !keepText && len(paths) == 0 || v.closed || !v.ready || v.off != nil {
		return
	}
	if s <= v.last {
		v.report(fmt.Sprintf("the vault didn't keep the content of record %d, because it keeps records in order and already keeps record %d's", s, v.last))
		return
	}
	var buf []byte
	var err error
	if keepText {
		if buf, err = v.appendEntry(buf, s, whatText, text); err != nil {
			v.report(fmt.Sprintf("the vault didn't keep the text of record %d: %v", s, err))
			return
		}
	}
	if len(paths) > 0 {
		raw, err := json.Marshal(paths)
		if err == nil {
			buf, err = v.appendEntry(buf, s, whatPaths, raw)
		}
		if err != nil {
			v.report(fmt.Sprintf("the vault didn't keep the paths of record %d: %v", s, err))
			return
		}
	}
	if err := v.toDay(day); err != nil {
		v.report(fmt.Sprintf("the vault didn't keep the content of record %d: %v", s, err))
		return
	}
	if v.size == 0 {
		hp, err := json.Marshal(head{V: headVersion, KeyID: v.keyID})
		if err != nil {
			v.report(fmt.Sprintf("the vault didn't keep the content of record %d: %v", s, err))
			return
		}
		// The head goes in the same write as the file's first entry, so a
		// crash leaves the file with both or with no whole frame.
		buf = append(appendFrame(nil, kindHead, hp), buf...)
	}
	if _, err := v.cur.WriteAt(buf, v.size); err != nil {
		if terr := v.cur.Truncate(v.size); terr != nil {
			// The file may end in part of a frame, which only Reconcile
			// cuts: nothing more goes after it in this run.
			v.off = fmt.Errorf("the vault keeps nothing until the agent starts again, because a failed write couldn't be undone: %v, then %v", err, terr)
			v.report(v.off.Error())
			return
		}
		v.report(fmt.Sprintf("the vault didn't keep the content of record %d: %v", s, err))
		return
	}
	v.size += int64(len(buf))
	v.last, v.dirty = s, true
	v.recovered()
}

// appendEntry appends to dst the frame of an entry for record s holding
// content, sealed under a fresh nonce.
func (v *Vault) appendEntry(dst []byte, s uint64, what byte, content []byte) ([]byte, error) {
	if len(content)+entryMin > maxPayload {
		return dst, fmt.Errorf("%d bytes is more than an entry holds", len(content))
	}
	a := ad(s, what)
	nonce := make([]byte, nonceSize)
	rand.Read(nonce)
	p := make([]byte, 0, entryMin+len(content))
	p = append(p, a...)
	p = append(p, nonce...)
	// a is its own slice: cipher.AEAD's Seal takes no dst that overlaps
	// the associated data.
	p, err := v.gcm.seal(p, nonce, content, a)
	if err != nil {
		return dst, err
	}
	return appendFrame(dst, kindEntry, p), nil
}

// distinct returns paths without "" and without repeats, in order.
func distinct(paths []string) []string {
	var out []string
	for _, p := range paths {
		if p != "" && !slices.Contains(out, p) {
			out = append(out, p)
		}
	}
	return out
}

// toDay makes cur the file of day, or of the newest day the vault has
// when that is later: like the ledger's segments, the files never go back
// to an older day, so their names stay in the order of their entries. v.mu
// is held.
func (v *Vault) toDay(day string) error {
	if !isDay(day) {
		return fmt.Errorf("%q isn't a UTC day", day)
	}
	day = max(day, v.newest, v.curDay)
	if v.cur != nil && day == v.curDay {
		return nil
	}
	if v.cur != nil {
		// A file is complete once its day is over.
		if err := v.cur.Sync(); err != nil {
			return err
		}
		v.cur.Close()
		v.cur, v.curDay, v.size, v.dirty = nil, "", 0, false
	}
	f, err := os.OpenFile(segPath(v.dir, day), os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return err
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return err
	}
	if info.Size() == 0 {
		home.SyncDir(v.dir)
	}
	v.cur, v.curDay, v.size, v.newest = f, day, info.Size(), day
	return nil
}

// Sync flushes what Keep wrote. The ledger writer calls it before it
// flushes a batch (ledger.Options.Vault), so the content of every record
// in the batch is on disk first. It holds no lock Keep takes while it
// waits for the disk.
func (v *Vault) Sync() {
	v.syncMu.Lock()
	defer v.syncMu.Unlock()
	v.mu.Lock()
	f, dirty := v.cur, v.dirty
	v.dirty = false
	v.mu.Unlock()
	if f == nil || !dirty {
		return
	}
	err := f.Sync()
	if err == nil || errors.Is(err, os.ErrClosed) {
		// A file closed meanwhile was flushed as its day ended (toDay).
		return
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.cur == f {
		v.dirty = true
	}
	v.report(fmt.Sprintf("the vault couldn't flush its file to disk: %v", err))
}

// Close flushes and closes the vault. Keep does nothing after it.
func (v *Vault) Close() error {
	v.files.Lock()
	defer v.files.Unlock()
	v.syncMu.Lock()
	defer v.syncMu.Unlock()
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.closed {
		return nil
	}
	v.closed = true
	return v.closeCur()
}

// closeCur flushes and closes cur. v.mu is held.
func (v *Vault) closeCur() error {
	if v.cur == nil {
		return nil
	}
	err := v.cur.Sync()
	if cerr := v.cur.Close(); err == nil {
		err = cerr
	}
	v.cur, v.curDay, v.size, v.dirty = nil, "", 0, false
	return err
}

// report hands msg to Options.Problem. v.mu is held, or the vault isn't
// shared yet.
func (v *Vault) report(msg string) {
	v.failed = true
	if v.problem != nil {
		v.problem(msg)
	}
}

// recovered clears the problem report made last, once a write succeeds
// after it. v.mu is held.
func (v *Vault) recovered() {
	if !v.failed {
		return
	}
	v.failed = false
	if v.problem != nil {
		v.problem("")
	}
}

// Text returns the text record s commits, if the vault keeps it: the bytes
// the record's commitment c was made from, which a reveal opens (ledger-v1
// 10). An entry whose text doesn't commit to c, as one a crash left for a
// number another record took, isn't returned. ErrNotKept means the vault
// keeps no text for the record.
func (v *Vault) Text(s uint64, c ledger.Commitment) ([]byte, error) {
	text, err := v.read(s, whatText)
	if err != nil {
		return nil, err
	}
	got, err := v.keys.Commit(s, text)
	if err != nil {
		return nil, err
	}
	if got != c {
		clear(text)
		return nil, fmt.Errorf("%w: the text kept for record %d doesn't commit to the record's c", ErrNotKept, s)
	}
	return text, nil
}

// Paths returns the paths of record s's edits, if the vault keeps them.
// ErrNotKept means it doesn't.
func (v *Vault) Paths(s uint64) ([]string, error) {
	raw, err := v.read(s, whatPaths)
	if err != nil {
		return nil, err
	}
	var paths []string
	if err := json.Unmarshal(raw, &paths); err != nil {
		return nil, fmt.Errorf("vault: the paths kept for record %d don't read: %w", s, err)
	}
	return paths, nil
}

// read returns the content of record s's entry that holds what. The files
// are in the order of their entries, so it reads the newest file whose
// first entry isn't after s.
func (v *Vault) read(s uint64, what byte) ([]byte, error) {
	v.files.RLock()
	defer v.files.RUnlock()
	days, err := segments(v.dir)
	if err != nil {
		return nil, err
	}
	for i := len(days) - 1; i >= 0; i-- {
		f, err := readFile(segPath(v.dir, days[i]), days[i], v.keyID)
		if err != nil {
			return nil, err
		}
		if len(f.entries) == 0 || f.entries[0].s > s {
			continue
		}
		for _, e := range f.entries {
			if e.s == s && e.what == what {
				return v.gcm.open(nil, e.nonce, e.sealed, ad(e.s, e.what))
			}
		}
		break
	}
	return nil, ErrNotKept
}

// DropText deletes every text the vault keeps, as when the teen turns
// "keep prompt text" off (DESIGN 4.4): each file is written again without
// its texts, by atomic rename, and a file left with no entry is deleted.
// Paths stay. It returns how many texts it deleted.
func (v *Vault) DropText() (int, error) {
	return v.filter(func(e entry) bool { return e.what != whatText })
}

// filter writes each of the vault's files again with only the entries
// keep keeps, and deletes a file left with none. The files of other
// device keys, in old/, are left alone. Only the newest file, which Keep
// may append to, is written again under the lock Keep takes, so the
// ledger writer waits for that one file at most.
func (v *Vault) filter(keep func(entry) bool) (int, error) {
	v.files.Lock()
	defer v.files.Unlock()
	v.mu.Lock()
	closed := v.closed
	// Keep never goes back to a day before this one (toDay).
	newest := max(v.newest, v.curDay)
	v.mu.Unlock()
	if closed {
		return 0, ErrClosed
	}
	days, err := segments(v.dir)
	if err != nil {
		return 0, err
	}
	dropped := 0
	var errs []error
	for _, day := range days {
		var n int
		if day < newest {
			n, err = v.filterFile(day, keep)
		} else {
			n, err = v.filterNewest(day, keep)
		}
		dropped += n
		if err != nil {
			errs = append(errs, err)
		}
	}
	return dropped, errors.Join(errs...)
}

// filterNewest is filterFile for a file Keep may append to, which it
// closes first: Windows renames over no open file, and deletes none.
func (v *Vault) filterNewest(day string, keep func(entry) bool) (int, error) {
	v.syncMu.Lock()
	defer v.syncMu.Unlock()
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.cur != nil && v.curDay == day {
		if err := v.closeCur(); err != nil {
			return 0, err
		}
	}
	return v.filterFile(day, keep)
}

// filterFile writes day's file again with only the entries keep keeps, or
// deletes it when none is left. v.files is held for writing, and when the
// file is the newest, syncMu and mu too, with the file closed.
func (v *Vault) filterFile(day string, keep func(entry) bool) (int, error) {
	name := segPath(v.dir, day)
	f, err := readFile(name, day, v.keyID)
	if err != nil {
		return 0, err
	}
	var kept []entry
	for _, e := range f.entries {
		if keep(e) {
			kept = append(kept, e)
		}
	}
	dropped := len(f.entries) - len(kept)
	if dropped == 0 {
		return 0, nil
	}
	if len(kept) == 0 {
		if err := os.Remove(name); err != nil {
			return 0, err
		}
		home.SyncDir(v.dir)
		return dropped, nil
	}
	hp, err := json.Marshal(head{V: headVersion, KeyID: v.keyID})
	if err != nil {
		return 0, err
	}
	buf := appendFrame(nil, kindHead, hp)
	for _, e := range kept {
		p := make([]byte, 0, entryMin+len(e.sealed))
		p = append(p, ad(e.s, e.what)...)
		p = append(p, e.nonce...)
		p = append(p, e.sealed...)
		buf = appendFrame(buf, kindEntry, p)
	}
	if err := home.WriteFile(name, buf); err != nil {
		return 0, err
	}
	return dropped, nil
}
