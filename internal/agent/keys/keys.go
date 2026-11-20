// SPDX-License-Identifier: AGPL-3.0-or-later

// Package keys is the device keystore (DESIGN 3.6; docs/PLAN.md, WP-OS7).
// At an installation's first run it makes K_dev, 32 bytes from the OS's
// random number generator, and keeps it in the OS's own secret store. Every
// run after that reads it back and derives the four ledger keys of
// docs/spec/ledger-v1.md section 3 (K_leaf, K_tok, K_feat and K_vault)
// through pkg/ledger.
//
// # Where K_dev is kept
//
// On Windows, DPAPI encrypts it under the user's logon credentials and the
// blob goes in keys/<install>.kdev.dpapi. On macOS it is an item in the
// login Keychain, and on Linux an item in the Secret Service, both through
// go-keyring (DESIGN 3.12), filed under the service "pairtime" with the
// account <install>.kdev. When that store fails at the first run, as on a
// headless Linux box with no Secret Service, K_dev goes in
// keys/<install>.kdev.key, a file that no other account but an
// administrator can read, and that unlike the OS store doesn't encrypt the
// key at rest. Status then carries a warning that doctor and the dashboard
// repeat (CP 5.2).
//
// keys/keystore.json says which store holds K_dev, with the installation's
// random id and K_dev's id, a public fingerprint: HKDF-SHA256 of K_dev with
// info "pairtime/v1/key-id", 16 bytes. It holds no secret. Inspect reads it
// alone, so doctor can say where the key is without asking for it.
//
// # What the keystore never does
//
//   - It never logs, prints or uploads K_dev or the keys derived from it,
//     and never writes them anywhere but their store. It doesn't log at
//     all; its errors and its Status name stores and files, never key
//     bytes; and a Keystore prints as a placeholder.
//   - It never makes a second K_dev for an installation that has one. When
//     the store named in keystore.json can't be reached, Open returns
//     ErrUnavailable; when the key is gone from it, ErrKeyLost; when what
//     comes back isn't the key, ErrDamaged. A new key would orphan every
//     record the old one wrote, so only Destroy, which a person asks for,
//     clears the way for one.
//   - It never lets a K_dev restored from another installation write
//     records. Archive keeps such a key for reading only (GitHub issue
//     #19).
//
// It compares keys and key ids in constant time and zeroes its copies of
// K_dev once the keys are derived. Two limits remain: go-keyring passes
// secrets as Go strings, which can't be zeroed, and pkg/ledger's Keys has
// no method that zeroes the derived keys.
package keys

import (
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/some-du6e/pairtime/internal/agent/home"
	"github.com/some-du6e/pairtime/pkg/ledger"
)

var (
	// ErrNotFound means a store has no secret under the name.
	ErrNotFound = errors.New("keys: no such secret")
	// ErrUnreadable means a store has a secret under the name that this
	// account can't decrypt, as with a DPAPI blob from before a Windows
	// reinstall.
	ErrUnreadable = errors.New("keys: the secret can't be decrypted by this account")
	// ErrUnavailable means the store that holds the device key couldn't
	// be reached or didn't answer in time, as with a locked keyring whose
	// prompt nobody answered. Trying again later may work. Open never
	// makes a new key because of it.
	ErrUnavailable = errors.New("keys: the store that holds the device key is unavailable")
	// ErrKeyLost means keystore.json says this installation has a device
	// key, but its store no longer has it, or has it in a form this
	// account can't decrypt. The records that key wrote can't be opened
	// here unless `pairtime restore` brings the key back (DESIGN 7.11).
	ErrKeyLost = errors.New("keys: the device key is gone from its store")
	// ErrDamaged means keystore.json, or the key its store gave back, isn't
	// what the keystore wrote.
	ErrDamaged = errors.New("keys: the keystore is damaged")
)

// DefaultTimeout bounds each call to a store when Options.Timeout is zero.
// A locked keyring asks for its password and go-keyring waits for the
// answer, so it gives a person time to type it.
const DefaultTimeout = time.Minute

// Options say where a keystore is and which stores it may use.
type Options struct {
	// Dir is the installation's data directory (home.Dirs.Data), an
	// absolute path. The keystore keeps its files in Dir/keys.
	Dir string
	// Stores, when not nil, replaces this OS's secret store. An empty
	// slice means none, so the key goes in the key file; tests outside
	// this package use that to stay off the OS store. The key file is
	// always the last store tried.
	Stores []Store
	// Timeout bounds each call to a store that may wait for the user,
	// which is every store but the key file. Zero means DefaultTimeout.
	Timeout time.Duration
}

// options are Options resolved.
type options struct {
	dir     string  // Dir/keys
	stores  []Store // first-run order: the OS's stores, then the key file
	timeout time.Duration
}

func (opts Options) resolve() (*options, error) {
	if !filepath.IsAbs(opts.Dir) {
		return nil, fmt.Errorf("keys: the data directory must be an absolute path, not %q", opts.Dir)
	}
	dir := filepath.Join(filepath.Clean(opts.Dir), dirName)
	first := opts.Stores
	if first == nil {
		first = osStores(dir)
	}
	o := &options{
		dir:     dir,
		stores:  slices.Concat(first, []Store{keyFile(dir)}),
		timeout: opts.Timeout,
	}
	if o.timeout <= 0 {
		o.timeout = DefaultTimeout
	}
	return o, nil
}

// storeOf returns the store of kind k, or nil when this system has none.
func (o *options) storeOf(k Kind) Store {
	for _, s := range o.stores {
		if s.Kind() == k {
			return s
		}
	}
	return nil
}

// lockWait is how long Open, Archive and Destroy wait for another process
// that holds the keystore's lock. A first run can take a few store calls,
// each up to the timeout, so this is longer than that.
func (o *options) lockWait() time.Duration { return 4*o.timeout + 10*time.Second }

// lock takes the keystore's lock, which serializes first runs, archives and
// Destroy across processes: the agent, `pairtime install`, `pairtime
// restore`. It is home's single-instance lock taken on the keys directory,
// so it is a file named agent.lock in there, or a named mutex on Windows.
func (o *options) lock() (func(), error) {
	deadline := time.Now().Add(o.lockWait())
	for {
		l, err := home.Acquire(o.dir)
		if err == nil {
			return func() { l.Release() }, nil
		}
		if !errors.Is(err, home.ErrLocked) || time.Now().After(deadline) {
			return nil, fmt.Errorf("keys: locking %s: %w", o.dir, err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// Status says where an installation's device key is kept, without the key.
type Status struct {
	// Store is where K_dev is.
	Store Kind `json:"store"`
	// KeyID is K_dev's public fingerprint, in hex.
	KeyID   string    `json:"key_id"`
	Created time.Time `json:"created"`
	// Fallback is true when K_dev is in the key file, because the OS
	// store failed at the first run or this OS has none.
	Fallback bool `json:"fallback"`
	// Reason says why the OS store wasn't used, when Fallback is true and
	// one was tried.
	Reason string `json:"reason,omitempty"`
	// File is the key file, when Fallback is true.
	File string `json:"file,omitempty"`
	// Archived lists the ids of device keys restored from other
	// installations and kept for reading their records (Archive).
	Archived []string `json:"archived,omitempty"`
}

func (m marker) status(dir string) Status {
	s := Status{
		Store:    m.Store,
		KeyID:    m.KeyID,
		Created:  m.Created,
		Fallback: m.Store == KindFile,
	}
	if s.Fallback {
		s.Reason = m.Reason
		s.File = keyFile(dir).path(m.deviceItem())
	}
	for _, a := range m.Archived {
		if !a.Pending {
			s.Archived = append(s.Archived, a.ID)
		}
	}
	return s
}

// Warning is what doctor and the dashboard say when K_dev is in the key
// file (DESIGN 3.6; CP 5.2), and "" when it is in the OS store.
func (s Status) Warning() string {
	if !s.Fallback {
		return ""
	}
	var b strings.Builder
	b.WriteString("pairtime keeps its device key in a file, not in a secret store")
	if s.File != "" {
		fmt.Fprintf(&b, " (%s)", s.File)
	}
	if s.Reason != "" {
		fmt.Fprintf(&b, ", because %s", s.Reason)
	} else {
		b.WriteString(", because this system has no secret store pairtime can use")
	}
	b.WriteString(". Other non-admin accounts on this computer can't read the file, but administrators can, and so can any program you run. A secret store would also keep the key encrypted at rest, and the file doesn't, so anyone with a copy of your home folder, such as a backup or a synced folder, has the key.")
	return b.String()
}

// Keystore is an open keystore. Its methods are safe to call from any
// goroutine.
type Keystore struct {
	o       *options
	store   Store // holds K_dev
	keys    *ledger.Keys
	created bool

	mu sync.Mutex
	m  marker // keystore.json as this process last read or wrote it
}

// Open opens the keystore of the installation whose data directory is
// opts.Dir. At the first run it makes K_dev and keeps it in the first
// store that takes it and gives it back intact: the OS's, else the key
// file. After that it reads K_dev from the store keystore.json names and
// derives the ledger keys, and it never makes another: see ErrUnavailable,
// ErrKeyLost and ErrDamaged.
func Open(opts Options) (*Keystore, error) {
	return open(opts, randomKey)
}

func randomKey(b []byte) { rand.Read(b) }

// open is Open with the source of a new K_dev given, so tests can make a
// keystore whose K_dev they chose, through the first-run path.
func open(opts Options, newKey func([]byte)) (*Keystore, error) {
	o, err := opts.resolve()
	if err != nil {
		return nil, err
	}
	m, err := readMarker(o.dir)
	switch {
	case err == nil && m.complete():
		return o.load(m)
	case err != nil && !isNotExist(err):
		return nil, err
	}
	// The first run, or one that was cut off. Under the lock, so two
	// processes starting at once make one key.
	if err := os.MkdirAll(o.dir, 0o700); err != nil {
		return nil, err
	}
	unlock, err := o.lock()
	if err != nil {
		return nil, err
	}
	defer unlock()
	m, err = readMarker(o.dir)
	switch {
	case err == nil && m.complete():
		return o.load(m) // another process finished the first run
	case err == nil:
		// A first run that was cut off. Nothing used its key, so a new
		// one replaces it under the same installation id, and with it any
		// item that run left behind.
	case isNotExist(err):
		m = marker{V: markerVersion, Install: newInstallID()}
		// Written first, so a run cut off from here on leaves its id, and
		// whatever it put in a store is named and can be replaced or
		// deleted.
		if err := writeMarker(o.dir, m); err != nil {
			return nil, err
		}
	default:
		return nil, err
	}
	return o.create(m, newKey)
}

// create makes K_dev and keeps it in the first store that takes it. The
// caller holds the lock, and m is keystore.json with its installation id.
// A store that failed but may hold the key anyway (put) goes in
// keystore.json as unconfirmed, so Destroy deletes the key there too.
//
// A run cut off before it writes keystore.json with the store never hands
// its key to anyone, so a copy it left in a store protects no record; the
// next run replaces it under the same item name, or leaves it unrecorded.
func (o *options) create(m marker, newKey func([]byte)) (*Keystore, error) {
	kDev := make([]byte, keySize)
	defer clear(kDev)
	newKey(kDev)
	id, err := keyID(kDev)
	if err != nil {
		return nil, err
	}
	var reasons []string
	m.Unconfirmed = nil
	for _, s := range o.stores {
		held, err := o.put(s, m.deviceItem(), kDev)
		if err != nil {
			if held {
				m.Unconfirmed = append(m.Unconfirmed, s.Kind())
			}
			reasons = append(reasons, fmt.Sprintf("%s isn't available (%v)", s.Kind().Description(), err))
			continue
		}
		m.Store, m.KeyID, m.Created = s.Kind(), id, time.Now().UTC()
		m.Reason = ""
		if s.Kind() == KindFile && len(reasons) > 0 {
			m.Reason = strings.Join(reasons, "; ")
		}
		if err := writeMarker(o.dir, m); err != nil {
			o.remove(s, m.deviceItem())
			return nil, fmt.Errorf("keys: writing keystore.json: %w", err)
		}
		if s.Kind() != KindFile {
			// A first run that was cut off may have left its key in the
			// key file. Nothing used it, and it shouldn't lie around.
			keyFile(o.dir).Delete(m.deviceItem())
		}
		keys, err := ledger.DeriveKeys(kDev)
		if err != nil {
			return nil, err
		}
		return &Keystore{o: o, store: s, keys: keys, created: true, m: m}, nil
	}
	return nil, fmt.Errorf("keys: no store could keep the device key: %s", strings.Join(reasons, "; "))
}

// load reads K_dev from the store m names and derives the ledger keys.
func (o *options) load(m marker) (*Keystore, error) {
	s := o.storeOf(m.Store)
	if s == nil {
		return nil, fmt.Errorf("%w: keystore.json says the device key is in %s, which this system doesn't have", ErrUnavailable, m.Store.Description())
	}
	kDev, err := o.fetch(s, m.deviceItem(), m.KeyID)
	if err != nil {
		return nil, err
	}
	defer clear(kDev)
	keys, err := ledger.DeriveKeys(kDev)
	if err != nil {
		return nil, err
	}
	return &Keystore{o: o, store: s, keys: keys, m: m}, nil
}

// Keys returns the ledger keys derived from K_dev. Hold them by the
// pointer, as pkg/ledger asks, so nothing can print their bytes.
func (k *Keystore) Keys() *ledger.Keys { return k.keys }

// Created reports whether this Open made K_dev, which happens once per
// installation.
func (k *Keystore) Created() bool { return k.created }

// Status says where K_dev is kept.
func (k *Keystore) Status() Status {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.m.status(k.o.dir)
}

// Format writes a placeholder whatever the verb, so printing or logging a
// Keystore never shows key material.
func (k *Keystore) Format(f fmt.State, _ rune) { io.WriteString(f, "keys.Keystore{redacted}") }

// WithDeviceKey reads K_dev from its store again, checks it against
// keystore.json, and calls fn with it. The bytes are zeroed when fn
// returns, so fn must not keep the slice. K_dev has two uses besides the
// ledger keys, both in DESIGN 7.11 (WP-PV2): wrapping it under the
// recovery code and deriving the backup key. The keystore doesn't keep
// K_dev in memory between them.
func (k *Keystore) WithDeviceKey(fn func(kDev []byte) error) error {
	k.mu.Lock()
	m := k.m
	k.mu.Unlock()
	kDev, err := k.o.fetch(k.store, m.deviceItem(), m.KeyID)
	if err != nil {
		return err
	}
	defer clear(kDev)
	return fn(kDev)
}

// Archive keeps kDev, the device key of an earlier installation that
// `pairtime restore` unwrapped with its recovery code (DESIGN 7.11;
// WP-PV2), so this installation can check the nonces of the restored
// records and open their vault. The key goes in the store K_dev is in. It
// returns the key's id, which ArchivedKeys takes; archiving a key twice
// returns the same id. Archive copies kDev and doesn't change it.
//
// keystore.json lists the key, as pending, before the store gets it, so
// Destroy finds the item even if Archive fails or is cut off. A failed
// Archive leaves the entry pending when the store may hold the key, and
// drops it when the store holds nothing. Archiving the key again finishes
// a pending entry.
//
// An archived key is only ever read, never written with. Nonces and token
// salts are keyed by K_dev and the sequence number alone (ledger-v1.md
// 4.4; DESIGN 3.6), so a restored key that wrote a new chain from s = 1
// would repeat every nonce its old chain used, and so would one that went
// on from a backup older than the old device's last records (GitHub issue
// #19). This installation writes its own chain under the K_dev Open made.
func (k *Keystore) Archive(kDev []byte) (string, error) {
	if len(kDev) != keySize {
		return "", fmt.Errorf("keys: a device key is %d bytes, not %d", keySize, len(kDev))
	}
	id, err := keyID(kDev)
	if err != nil {
		return "", err
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	if sameID(id, k.m.KeyID) {
		return "", errors.New("keys: that is this installation's own device key, which it writes with, so it isn't archived")
	}
	unlock, err := k.o.lock()
	if err != nil {
		return "", err
	}
	defer unlock()
	m, err := readMarker(k.o.dir) // another process may have archived since
	if err != nil {
		return "", err
	}
	if !m.sameInstallation(k.m) {
		return "", errChanged
	}
	i := m.archivedIndex(id)
	switch {
	case i < 0:
		m.Archived = append(m.Archived, archivedKey{ID: id, Store: k.store.Kind(), Added: time.Now().UTC(), Pending: true})
		i = len(m.Archived) - 1
		if err := writeMarker(k.o.dir, m); err != nil {
			return "", fmt.Errorf("keys: writing keystore.json: %w", err)
		}
		k.m = m
	case !m.Archived[i].Pending:
		k.m = m
		return id, nil
	case m.Archived[i].Store != k.store.Kind():
		return "", fmt.Errorf("%w: keystore.json lists the archived key as pending in %s, not in %s with the device key", ErrDamaged, m.Archived[i].Store.Description(), k.store.Kind().Description())
	}
	held, err := k.o.put(k.store, m.archivedItem(id), kDev)
	if err != nil {
		err = fmt.Errorf("keys: keeping the restored key in %s: %w", k.store.Kind().Description(), err)
		if held {
			return "", err // the entry stays pending, so Destroy deletes the key
		}
		m.Archived = slices.Delete(m.Archived, i, i+1)
		if werr := writeMarker(k.o.dir, m); werr != nil {
			return "", errors.Join(err, fmt.Errorf("keys: writing keystore.json: %w", werr))
		}
		k.m = m
		return "", err
	}
	m.Archived[i].Pending = false
	if err := writeMarker(k.o.dir, m); err != nil {
		// The entry stays pending: Destroy deletes the key, and archiving
		// it again finishes the entry.
		return "", fmt.Errorf("keys: writing keystore.json: %w", err)
	}
	k.m = m
	return id, nil
}

// errChanged is the error for a keystore.json that another installation,
// or another first run, wrote while this keystore was open.
var errChanged = fmt.Errorf("%w: keystore.json changed under the open keystore, and now names another installation or device key", ErrDamaged)

// ArchivedKeys returns the ledger keys of the archived device key id, for
// reading the records it wrote. Nothing may write records with them.
func (k *Keystore) ArchivedKeys(id string) (*ledger.Keys, error) {
	m, err := readMarker(k.o.dir) // Archive may have run in another process
	if err != nil {
		return nil, err
	}
	// Only keystore.json of this installation and this device key is taken
	// in, or WithDeviceKey would read whatever key a swapped file names.
	k.mu.Lock()
	same := m.sameInstallation(k.m)
	if same {
		k.m = m
	}
	k.mu.Unlock()
	if !same {
		return nil, errChanged
	}
	i := m.archivedIndex(id)
	if i < 0 || m.Archived[i].Pending {
		return nil, fmt.Errorf("%w: no archived device key has the id %q", ErrNotFound, id)
	}
	a := m.Archived[i]
	s := k.o.storeOf(a.Store)
	if s == nil {
		return nil, fmt.Errorf("%w: keystore.json says the archived key is in %s, which this system doesn't have", ErrUnavailable, a.Store.Description())
	}
	kDev, err := k.o.fetch(s, m.archivedItem(a.ID), a.ID)
	if err != nil {
		return nil, err
	}
	defer clear(kDev)
	return ledger.DeriveKeys(kDev)
}

// Inspect returns the Status of the keystore in the data directory dir
// from keystore.json alone, without asking any store for the key, so doctor
// and the dashboard can show where it is and repeat the fallback warning.
// Its error wraps fs.ErrNotExist when the installation has no key yet.
func Inspect(dir string) (Status, error) {
	if !filepath.IsAbs(dir) {
		return Status{}, fmt.Errorf("keys: the data directory must be an absolute path, not %q", dir)
	}
	kdir := filepath.Join(filepath.Clean(dir), dirName)
	m, err := readMarker(kdir)
	if isNotExist(err) || (err == nil && !m.complete()) {
		return Status{}, errNoKeystore
	}
	if err != nil {
		return Status{}, err
	}
	return m.status(kdir), nil
}

// Destroy deletes the installation's device keys, its own and any it
// archived, from every store that may hold them, then the keystore's
// directory. After it, the records those keys wrote can't be opened, and
// their nonces can't be checked, by anyone (crypto-shredding, DESIGN 3.5),
// and the next Open makes a new K_dev. It is "delete everything" (DESIGN
// 8.8; WP-LG6) and uninstall's last step, so a person asks for it.
//
// The stores that may hold a key are the ones keystore.json names: the
// store K_dev is in; any store the first run listed as unconfirmed, because
// a call there missed its deadline, or because the store took the key and
// then failed both to give it back and to delete it; and each archived
// key's store. Other stores never got a key, or refused it, so Destroy
// doesn't ask them, and a headless box without a Secret Service doesn't
// fail on one. Every delete is read back, because a Secret Service delete
// whose prompt was dismissed reports success. When one fails, Destroy
// returns the error and keeps keystore.json, the only record of the items'
// names, so a later Destroy can finish.
//
// A store this system doesn't have can't be reached from here, as when a
// data directory moved from a Mac to a Linux box. DPAPI blobs are files in
// the keystore's directory and go with it. For a key in the Keychain or the
// Secret Service of another system, Destroy deletes everything else and
// returns an error that names the item left there.
//
// Some stores keep what a client deletes. KeePassXC moves the entry to the
// database's recycle bin, which is on by default, so there the key stays
// in the database until the recycle bin is emptied.
func Destroy(opts Options) error {
	o, err := opts.resolve()
	if err != nil {
		return err
	}
	if _, err := os.Stat(o.dir); isNotExist(err) {
		return nil
	}
	unlock, err := o.lock()
	if err != nil {
		return err
	}
	defer unlock()
	m, err := readMarker(o.dir)
	if isNotExist(err) {
		return os.RemoveAll(o.dir)
	}
	if err != nil {
		if rerr := os.RemoveAll(o.dir); rerr != nil {
			return rerr
		}
		return fmt.Errorf("%w; the keystore's files are deleted, but any item it put in the OS's secret store remains, because keystore.json named it", err)
	}
	var failed, elsewhere []error
	for _, h := range m.holders() {
		s := o.storeOf(h.store)
		switch {
		case s != nil:
			if err := o.remove(s, h.item); err != nil {
				failed = append(failed, fmt.Errorf("keys: deleting a device key from %s: %w", h.store.Description(), err))
			}
		case !h.store.inDir():
			elsewhere = append(elsewhere, fmt.Errorf("keys: keystore.json says %s may hold a device key, as the item %q of the service %q, but this system doesn't have that store, so the key is still there", h.store.Description(), h.item, keyringService))
		}
	}
	if len(failed) > 0 {
		return errors.Join(append(failed, elsewhere...)...)
	}
	if err := os.RemoveAll(o.dir); err != nil {
		return err
	}
	return errors.Join(elsewhere...)
}
