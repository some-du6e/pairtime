// SPDX-License-Identifier: AGPL-3.0-or-later

package keys

import (
	"bytes"
	"crypto/subtle"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/some-du6e/pairtime/internal/agent/home"
)

// A Store keeps named secrets for the keystore: the OS's secret store, or a
// file in the keystore's directory. Names are made by the keystore from
// letters, digits, dots and dashes, and each starts with the installation's
// id, so two installations on one account never share an item.
//
// A Store's methods may be slow, and may wait for the user, as when a
// locked keyring asks for its password. The keystore runs each call to a
// store but the key file with a deadline (Options.Timeout).
type Store interface {
	// Kind says which store this is. keystore.json records it, so the
	// key is looked for in the same store next time.
	Kind() Kind
	// Get returns the secret saved under name. Its error wraps
	// ErrNotFound when there is none, ErrUnreadable when there is one
	// this account can't decrypt, and ErrDamaged when the item under the
	// name isn't in the form Set writes.
	Get(name string) ([]byte, error)
	// Set saves secret under name, replacing what was there. It neither
	// keeps nor changes secret. When it returns an error, it saved
	// nothing.
	Set(name string, secret []byte) error
	// Delete removes the secret saved under name. Its error wraps
	// ErrNotFound when there was none. A nil error doesn't prove the
	// secret is gone, so the keystore reads it back (remove).
	Delete(name string) error
}

// Kind names a Store.
type Kind string

// The stores DESIGN 3.6 names, in keystore.json's spelling.
const (
	// KindDPAPI is Windows DPAPI: CryptProtectData under the user's logon
	// credentials, with the blob in a user-only file (keys/*.dpapi).
	KindDPAPI Kind = "dpapi"
	// KindKeychain is the macOS user's default keychain, normally the
	// login keychain, through go-keyring.
	KindKeychain Kind = "keychain"
	// KindSecretService is the Linux desktop's Secret Service over the
	// session D-Bus (GNOME Keyring, KWallet, KeePassXC), through
	// go-keyring.
	KindSecretService Kind = "secret-service"
	// KindFile is the key file of last resort, used when the OS store
	// failed (CP 5.2): the secret as it is, in a file that the user and
	// the system's administrators can open, and no other account
	// (keys/*.key, mode 0600, or a user-only DACL on Windows). Unlike the
	// OS stores, it doesn't encrypt the secret at rest.
	KindFile Kind = "file"
)

// Description names the store in plain words, for doctor and errors.
func (k Kind) Description() string {
	switch k {
	case KindDPAPI:
		return "Windows DPAPI"
	case KindKeychain:
		return "the macOS Keychain"
	case KindSecretService:
		return "the Secret Service"
	case KindFile:
		return "the key file"
	}
	return fmt.Sprintf("an unknown store %q", string(k))
}

func (k Kind) known() bool {
	switch k {
	case KindDPAPI, KindKeychain, KindSecretService, KindFile:
		return true
	}
	return false
}

// inDir reports whether the store keeps its secrets in files in the
// keystore's directory, as DPAPI's blobs and the key file are, so deleting
// the directory deletes them.
func (k Kind) inDir() bool { return k == KindDPAPI || k == KindFile }

// keyringService is the service go-keyring files the keystore's items
// under, in the Keychain and the Secret Service. Each item's account is its
// name, which starts with the installation's id.
const keyringService = "pairtime"

// osStores returns this OS's secret stores, in first-run order, for the
// keystore whose directory is dir. It is platformStores, which each OS's
// store_*.go defines. Tests replace it to watch what reaches the OS store.
var osStores = platformStores

// storeErr turns a platform store's errors into the keystore's: its
// not-found error into ErrNotFound, and its error for an item that isn't in
// the form it writes into ErrDamaged.
func storeErr(err, platformNotFound, platformBadItem error) error {
	switch {
	case errors.Is(err, platformNotFound):
		return fmt.Errorf("%w: %v", ErrNotFound, err)
	case errors.Is(err, platformBadItem):
		return fmt.Errorf("%w: %v", ErrDamaged, err)
	}
	return err
}

// fileStore keeps each secret in its own file in the keystore's directory,
// replaced by atomic rename, which no account but the user's and an
// administrator's can read (home.WritePrivate). With seal and open set it is the DPAPI store and
// its files hold DPAPI blobs; without them it is the key file of last
// resort and its files hold the secret as it is.
type fileStore struct {
	kind Kind
	dir  string
	ext  string
	seal func([]byte) ([]byte, error)
	open func([]byte) ([]byte, error)
}

// keyFile is the store of last resort, which every OS has.
func keyFile(dir string) fileStore {
	return fileStore{kind: KindFile, dir: dir, ext: ".key"}
}

func (f fileStore) Kind() Kind { return f.kind }

func (f fileStore) path(name string) string {
	return filepath.Join(f.dir, name+f.ext)
}

func (f fileStore) Get(name string) ([]byte, error) {
	data, err := os.ReadFile(f.path(name))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("%w: there is no %s", ErrNotFound, f.path(name))
	}
	if err != nil {
		return nil, err
	}
	if f.open == nil {
		return data, nil
	}
	plain, err := f.open(data)
	if err != nil {
		return nil, fmt.Errorf("%w: %s: %v", ErrUnreadable, f.path(name), err)
	}
	return plain, nil
}

func (f fileStore) Set(name string, secret []byte) error {
	data := secret
	if f.seal != nil {
		var err error
		if data, err = f.seal(secret); err != nil {
			return err
		}
	}
	return home.WritePrivate(f.path(name), data)
}

func (f fileStore) Delete(name string) error {
	err := os.Remove(f.path(name))
	if errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("%w: there is no %s", ErrNotFound, f.path(name))
	}
	return err
}

// Calls to stores. A store that may wait for the user, as a locked keyring
// does, is called on its own goroutine under the keystore's deadline. A
// call still running at the deadline is left to finish in the background:
// Go can't stop it, and a keyring prompt may yet be answered. What it
// returns then is thrown away, and a secret it read is zeroed.
//
// The key file never waits for anyone, so it is called directly. A
// deadline there would only turn a slow disk into a failed first run, with
// the write still going on behind it.

// bounded reports whether calls to s run under the deadline: calls to
// every store but the key file.
func bounded(s Store) bool { return s.Kind() != KindFile }

// get calls s.Get, under the deadline when s is bounded.
func (o *options) get(s Store, name string) ([]byte, error) {
	if !bounded(s) {
		return s.Get(name)
	}
	type result struct {
		b   []byte
		err error
	}
	done := make(chan result, 1)
	go func() {
		b, err := s.Get(name)
		done <- result{b, err}
	}()
	t := time.NewTimer(o.timeout)
	defer t.Stop()
	select {
	case r := <-done:
		if r.err != nil {
			clear(r.b)
			return nil, r.err
		}
		return r.b, nil
	case <-t.C:
		go func() { clear((<-done).b) }()
		return nil, o.late(s)
	}
}

// set calls s.Set, under the deadline when s is bounded. A bounded call
// gets its own copy of secret, because it may outlive set, and the caller
// zeroes its own.
func (o *options) set(s Store, name string, secret []byte) error {
	if !bounded(s) {
		return s.Set(name, secret)
	}
	cp := bytes.Clone(secret)
	return o.await(s, func() error {
		defer clear(cp)
		return s.Set(name, cp)
	})
}

// del calls s.Delete, under the deadline when s is bounded.
func (o *options) del(s Store, name string) error {
	return o.await(s, func() error { return s.Delete(name) })
}

// await runs call, on its own goroutine under the deadline when s is
// bounded.
func (o *options) await(s Store, call func() error) error {
	if !bounded(s) {
		return call()
	}
	done := make(chan error, 1)
	go func() { done <- call() }()
	t := time.NewTimer(o.timeout)
	defer t.Stop()
	select {
	case err := <-done:
		return err
	case <-t.C:
		return o.late(s)
	}
}

func (o *options) late(s Store) error {
	return &lateError{kind: s.Kind(), timeout: o.timeout}
}

// lateError is a store call that didn't answer within the deadline. Go
// can't stop the call, so it may still finish: a Set may yet save its
// secret.
type lateError struct {
	kind    Kind
	timeout time.Duration
}

func (e *lateError) Error() string {
	return fmt.Sprintf("%v: %s didn't answer within %v", ErrUnavailable, e.kind.Description(), e.timeout)
}

func (e *lateError) Unwrap() error { return ErrUnavailable }

// isLate reports whether err is a store call that missed its deadline.
func isLate(err error) bool {
	var l *lateError
	return errors.As(err, &l)
}

// put saves secret in s and reads it back, so a store that takes a secret
// and loses or changes it is caught before anything depends on the secret.
// held says whether s may hold the secret afterwards: always when put
// succeeds, and when it fails, if a call missed its deadline or s took the
// secret and the delete that follows a failed read-back didn't work.
//
// A failed Set saved nothing (Store), so it leaves nothing to delete. A
// read-back that missed its deadline isn't followed by a delete either,
// because the delete would most likely wait as long again. The caller
// records such a store in keystore.json, so Destroy deletes the secret
// there.
func (o *options) put(s Store, name string, secret []byte) (held bool, err error) {
	if err := o.set(s, name, secret); err != nil {
		return isLate(err), err
	}
	got, err := o.get(s, name)
	switch {
	case isLate(err):
		return true, fmt.Errorf("reading it back: %w", err)
	case err != nil:
		err = fmt.Errorf("reading it back: %w", err)
	default:
		same := subtle.ConstantTimeCompare(got, secret) == 1
		clear(got)
		if same {
			return true, nil
		}
		err = errors.New("it gave back other bytes than it was given")
	}
	if rerr := o.remove(s, name); rerr != nil {
		return true, fmt.Errorf("%w, and then deleting it failed: %w", err, rerr)
	}
	return false, err
}

// remove deletes the secret saved under name in s and reads it back to
// check that it's gone. A delete that returns nil doesn't prove that: a
// Secret Service delete whose prompt the user dismissed returns nil too,
// because go-keyring drops the prompt's dismissed flag.
func (o *options) remove(s Store, name string) error {
	err := o.del(s, name)
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	b, err := o.get(s, name)
	clear(b)
	switch {
	case errors.Is(err, ErrNotFound):
		return nil
	case err == nil:
		return fmt.Errorf("%s still has the secret after deleting it, as when a prompt to allow the delete is dismissed", s.Kind().Description())
	}
	return fmt.Errorf("checking that the secret is gone: %w", err)
}

// fetch reads the device key saved under name in s, and checks that it is
// 32 bytes and the key whose id is wantID. It never makes or replaces a
// key: what it can't read is lost, unavailable or damaged, and the caller
// decides what to do.
func (o *options) fetch(s Store, name, wantID string) ([]byte, error) {
	where := s.Kind().Description()
	kDev, err := o.get(s, name)
	switch {
	case errors.Is(err, ErrNotFound):
		return nil, fmt.Errorf("%w: %s has no device key for this installation", ErrKeyLost, where)
	case errors.Is(err, ErrUnreadable):
		return nil, fmt.Errorf("%w: %s has the device key, but this account can't decrypt it (%v)", ErrKeyLost, where, err)
	case errors.Is(err, ErrUnavailable), errors.Is(err, ErrDamaged):
		// ErrDamaged: the store has an item under the name that isn't in
		// the form it writes, which waiting won't change.
		return nil, err
	case err != nil:
		return nil, fmt.Errorf("%w: %s: %v", ErrUnavailable, where, err)
	}
	if n := len(kDev); n != keySize {
		clear(kDev)
		return nil, fmt.Errorf("%w: the device key in %s is %d bytes, not %d", ErrDamaged, where, n, keySize)
	}
	id, err := keyID(kDev)
	if err != nil {
		clear(kDev)
		return nil, err
	}
	if !sameID(id, wantID) {
		clear(kDev)
		return nil, fmt.Errorf("%w: the device key in %s isn't the one keystore.json names", ErrDamaged, where)
	}
	return kDev, nil
}
