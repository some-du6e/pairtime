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
// keys/<install>.kdev.key, a file only the user can read, and Status
// carries a warning that doctor and the dashboard repeat (CP 5.2).
//
// keys/keystore.json says which store holds K_dev, with the installation's
// random id and K_dev's id, a public fingerprint: HKDF-SHA256 of K_dev with
// info "pairtime/v1/key-id", 16 bytes. Once the ledger writer has started
// K_dev's chain, it also holds that chain's device id (RecordChain). It
// holds no secret. Inspect reads it alone, so doctor can say where the key
// is without asking for it.
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
//   - It never puts a key where keystore.json can't lead Destroy to it.
//     keystore.json gets the installation id, which names every item,
//     before a first run saves anything. A store the first run leaves
//     behind that may still have K_dev, because its save timed out or it
//     wouldn't let go of the key, is recorded before the next store is
//     tried, and Archive records a restored key before saving it.
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

// quickTimeout bounds Destroy's calls to stores that keystore.json doesn't
// name.
const quickTimeout = 5 * time.Second

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

// thisOSStores is osStores. Tests that need to see every call that reaches
// this OS's store replace it.
var thisOSStores = osStores

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
		first = thisOSStores(dir)
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
// that holds the keystore's lock. Saving a key in one store can take four
// calls, each up to the timeout: the save, the read back, and when that
// fails, the delete and the check that it worked. So this is longer.
func (o *options) lockWait() time.Duration { return 6*o.timeout + 10*time.Second }

// lock takes the keystore's lock, which serializes first runs, archives and
// Destroy across processes: the agent, `pairtime install`, `pairtime
// restore`. It is home's single-instance lock taken on the keys directory,
// a lock on the file agent.lock in there, and on Windows a named mutex
// too.
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
		s.Archived = append(s.Archived, a.ID)
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
	b.WriteString("pairtime keeps its device key in a file that only your account has permission to read")
	if s.File != "" {
		fmt.Fprintf(&b, " (%s)", s.File)
	}
	if s.Reason != "" {
		fmt.Fprintf(&b, ", because %s", s.Reason)
	} else {
		b.WriteString(", because this system has no secret store pairtime can use")
	}
	b.WriteString(". Other non-admin accounts on this computer can't read it, but administrators can, any program you run can, and so can anyone with a copy of your home folder, such as a backup or a synced folder. The OS's secret store would also keep the key encrypted on disk, and the file doesn't.")
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
func (o *options) create(m marker, newKey func([]byte)) (*Keystore, error) {
	kDev := make([]byte, keySize)
	defer clear(kDev)
	newKey(kDev)
	id, err := keyID(kDev)
	if err != nil {
		return nil, err
	}
	var reasons []string
	for _, s := range o.stores {
		mayHold, err := o.put(s, m.deviceItem(), kDev)
		if err != nil {
			reasons = append(reasons, fmt.Sprintf("%s isn't available (%v)", s.Kind().Description(), err))
			if mayHold && !slices.Contains(m.Unconfirmed, s.Kind()) {
				// The store may have K_dev, or get it from a call that is
				// still running. keystore.json says so before the next
				// store is tried, so Destroy deletes K_dev from it too.
				m.Unconfirmed = append(slices.Clip(m.Unconfirmed), s.Kind())
				if err := writeMarker(o.dir, m); err != nil {
					return nil, fmt.Errorf("keys: writing keystore.json: %w", err)
				}
			}
			continue
		}
		m.Store, m.KeyID, m.Created = s.Kind(), id, time.Now().UTC()
		m.Unconfirmed = slices.DeleteFunc(slices.Clone(m.Unconfirmed), func(k Kind) bool { return k == s.Kind() })
		m.Reason = ""
		if s.Kind() == KindFile && len(reasons) > 0 {
			m.Reason = strings.Join(reasons, "; ")
		}
		if err := writeMarker(o.dir, m); err != nil {
			o.del(s, m.deviceItem())
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

// Chain returns the device id of the ledger chain this installation's K_dev
// writes, as RecordChain recorded it, or "" while the ledger writer hasn't
// started one.
func (k *Keystore) Chain() string {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.m.Chain
}

// RecordChain records in keystore.json that K_dev writes the ledger chain
// of the device deviceID. One K_dev writes one chain (ledger-v1 3), because
// the nonces and token salts are keyed by the sequence number alone, and a
// second chain would number its records from 1 again. The ledger writer
// records each chain before the chain's first record, and a chain it finds
// that keystore.json doesn't name yet before it writes to it, so a ledger
// directory that is deleted or emptied leaves the key's chain on record
// here (internal/agent/ledger's ErrKeyReuse). Recording the same id again
// does nothing, and recording another one fails.
func (k *Keystore) RecordChain(deviceID string) error {
	if !isDeviceID(deviceID) {
		return fmt.Errorf("keys: %q isn't a device id", deviceID)
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.m.Chain == deviceID {
		return nil
	}
	unlock, err := k.o.lock()
	if err != nil {
		return err
	}
	defer unlock()
	m, err := k.reread() // Archive may have run in another process
	if err != nil {
		return err
	}
	k.m = m
	switch m.Chain {
	case deviceID:
		return nil
	case "":
	default:
		return fmt.Errorf("keys: the device key already writes the ledger chain of device %s, so it can't start the chain of %s", m.Chain, deviceID)
	}
	m.Chain = deviceID
	if err := writeMarker(k.o.dir, m); err != nil {
		return fmt.Errorf("keys: writing keystore.json: %w", err)
	}
	k.m = m
	return nil
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
// records and open their vault. The key goes in the store K_dev is in,
// and keystore.json lists it before the store gets it, so Destroy deletes
// it even when Archive fails halfway. It returns the key's id, which
// ArchivedKeys takes; archiving a key twice returns the same id. Archive
// copies kDev and doesn't change it.
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
	m, err := k.reread() // another process may have archived since
	if err != nil {
		return "", err
	}
	k.m = m
	if a, ok := m.archived(id); ok {
		// Archived before. Its item is checked, because an Archive that
		// failed after keystore.json listed the key may have left none.
		s := k.o.storeOf(a.Store)
		if s == nil {
			return "", fmt.Errorf("%w: keystore.json says the archived key is in %s, which this system doesn't have", ErrUnavailable, a.Store.Description())
		}
		kept, err := k.o.fetch(s, m.archivedItem(a.ID), a.ID)
		clear(kept)
		switch {
		case err == nil:
			return a.ID, nil
		case errors.Is(err, ErrUnavailable):
			return "", err
		}
		if _, err := k.o.put(s, m.archivedItem(a.ID), kDev); err != nil {
			return "", fmt.Errorf("keys: keeping the restored key in %s: %w", s.Kind().Description(), err)
		}
		return a.ID, nil
	}
	// keystore.json lists the key first, so a store that keeps it while
	// the call fails, or a process that dies after the store has it,
	// can't leave a copy that Destroy doesn't know to delete.
	listed := m
	listed.Archived = append(slices.Clip(m.Archived), archivedKey{ID: id, Store: k.store.Kind(), Added: time.Now().UTC()})
	if err := writeMarker(k.o.dir, listed); err != nil {
		return "", fmt.Errorf("keys: writing keystore.json: %w", err)
	}
	k.m = listed
	mayHold, err := k.o.put(k.store, listed.archivedItem(id), kDev)
	if err != nil {
		if !mayHold && writeMarker(k.o.dir, m) == nil {
			// The store doesn't have the key, so the entry goes again.
			k.m = m
		}
		return "", fmt.Errorf("keys: keeping the restored key in %s: %w", k.store.Kind().Description(), err)
	}
	return id, nil
}

// reread reads keystore.json again, as another process may have changed
// it, and checks that it is still this installation's, with the same
// device key. The caller holds k.mu.
func (k *Keystore) reread() (marker, error) {
	m, err := readMarker(k.o.dir)
	if err != nil {
		return marker{}, err
	}
	if m.Install != k.m.Install || !sameID(m.KeyID, k.m.KeyID) {
		return marker{}, fmt.Errorf("%w: keystore.json changed under the open keystore: it isn't this installation's, or names another device key", ErrDamaged)
	}
	return m, nil
}

// ArchivedKeys returns the ledger keys of the archived device key id, for
// reading the records it wrote. Nothing may write records with them.
func (k *Keystore) ArchivedKeys(id string) (*ledger.Keys, error) {
	k.mu.Lock()
	m, err := k.reread() // Archive may have run in another process
	if err == nil {
		k.m = m
	}
	k.mu.Unlock()
	if err != nil {
		return nil, err
	}
	a, ok := m.archived(id)
	if !ok {
		return nil, fmt.Errorf("%w: no archived device key has the id %q", ErrNotFound, id)
	}
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
// files: everything in its directory but the lock, keystore.json last.
// After it, the records those keys wrote can't be opened, and their nonces
// can't be checked, by anyone (crypto-shredding, DESIGN 3.5), and the next
// Open makes a new K_dev. It is "delete everything" (DESIGN 8.8; WP-LG6)
// and uninstall's last step, so a person asks for it.
//
// The stores keystore.json names for a key are the one that holds it, any
// a first run couldn't be sure doesn't have it (marker.Unconfirmed), and
// for an archived key the one Archive put it in. Each must delete the key
// within the full timeout, and must then answer ErrNotFound when asked for
// it, because a delete that returns nil may not have happened: go-keyring
// returns nil for a Secret Service delete whose prompt was dismissed. When
// any of them fails, Destroy returns the error and leaves keystore.json,
// so a later Destroy can finish. When keystore.json names a keyring this
// system doesn't have, as when the data directory came from another OS,
// Destroy still deletes the rest and the keystore's files, and its error
// says where the key stays.
//
// Stores keystore.json doesn't name were never given a key, except by a
// first run that died between saving the key and writing keystore.json.
// Destroy asks them to delete the device key too, with a short deadline,
// and their errors don't stop it, so a keyring that refused the key at
// the first run, or a box with no session bus, can't block "delete
// everything".
//
// What a store does with a deleted item is up to it. KeePassXC, which
// provides the Secret Service on some desktops, by default moves a
// deleted entry to its database's recycle bin, where K_dev stays until the
// recycle bin is emptied, though the Secret Service no longer shows it.
//
// The directory and its lock file stay. home never deletes a lock file,
// because that races with the next process to open it, and Windows can't
// delete one that is open, as Destroy's own is.
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
		return o.clearDir()
	}
	if err != nil {
		if rerr := o.clearDir(); rerr != nil {
			return rerr
		}
		return fmt.Errorf("%w; the keystore's files are deleted, but any item it put in the OS's secret store remains, because keystore.json named it", err)
	}
	type key struct {
		item  string
		store Kind
	}
	var named []key
	if m.Store != "" {
		named = append(named, key{m.deviceItem(), m.Store})
	}
	for _, k := range m.Unconfirmed {
		named = append(named, key{m.deviceItem(), k})
	}
	for _, a := range m.Archived {
		named = append(named, key{m.archivedItem(a.ID), a.Store})
	}
	if m.Account != "" {
		for _, item := range m.accountItems() {
			named = append(named, key{item, m.Account})
		}
	}
	var retry, stays []error
	for _, k := range named {
		s := o.storeOf(k.store)
		switch {
		case s != nil:
			if err := o.erase(s, k.item); err != nil {
				retry = append(retry, fmt.Errorf("keys: deleting a device key from %s: %w", s.Kind().Description(), err))
			}
		case k.store.inDir():
			// A DPAPI blob from another system goes with the keystore's
			// files.
		default:
			stays = append(stays, fmt.Errorf("%w: keystore.json names %s for a device key (service %q, account %q), which this system doesn't have, so the key stays there", ErrUnavailable, k.store.Description(), keyringService, k.item))
		}
	}
	// A short deadline here, so a locked keyring can't hold up "delete
	// everything" for a key it was never given.
	quick := *o
	quick.timeout = min(o.timeout, quickTimeout)
	for _, s := range o.stores {
		if !slices.Contains(named, key{m.deviceItem(), s.Kind()}) {
			quick.del(s, m.deviceItem())
		}
		// The device's sign-in too: a save that timed out may have
		// landed after ForgetAccount stopped listing it.
		for _, item := range m.accountItems() {
			if !slices.Contains(named, key{item, s.Kind()}) {
				quick.del(s, item)
			}
		}
	}
	if len(retry) > 0 {
		return errors.Join(append(retry, stays...)...)
	}
	if err := o.clearDir(); err != nil {
		return err
	}
	return errors.Join(stays...)
}

// clearDir deletes everything in the keystore's directory but the lock
// file, and keystore.json last, so a Destroy cut off halfway leaves
// keystore.json naming whatever is left. The caller holds the lock.
func (o *options) clearDir() error {
	entries, err := os.ReadDir(o.dir)
	if err != nil {
		return err
	}
	var errs []error
	for _, e := range entries {
		if n := e.Name(); n != lockName && n != markerName {
			if err := os.RemoveAll(filepath.Join(o.dir, n)); err != nil {
				errs = append(errs, err)
			}
		}
	}
	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	if err := os.Remove(filepath.Join(o.dir, markerName)); err != nil && !isNotExist(err) {
		return err
	}
	return nil
}
