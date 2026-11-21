// SPDX-License-Identifier: AGPL-3.0-or-later

package keys

import (
	"crypto/ed25519"
	"errors"
	"fmt"
	"path/filepath"
)

// The account secrets `pairtime login` keeps (WP-SV3;
// docs/spec/device-auth-v1.md section 8): the ptd_ device token the server
// handed out, and the 32-byte seed of the Ed25519 key that signs uploads.
// They go in the store that holds K_dev, under the installation's id like
// every item, and keystore.json lists them before the store gets them, so
// Destroy deletes them with everything else.
const (
	itemToken   = "device-token"
	itemSignKey = "device-signkey"
)

// accountItems are the item names of the account secrets: the token, then
// the seed.
func (m marker) accountItems() [2]string {
	return [2]string{m.Install + "." + itemToken, m.Install + "." + itemSignKey}
}

// maxTokenSize bounds a device token. Real ones are 42 bytes.
const maxTokenSize = 256

// SetAccount keeps the device token and the seed of the device's Ed25519
// key, replacing any it kept before. Each is read back from the store
// before SetAccount returns, as K_dev is. It copies neither and changes
// neither.
func (k *Keystore) SetAccount(token, seed []byte) error {
	if len(token) == 0 || len(token) > maxTokenSize {
		return fmt.Errorf("keys: a device token is 1 to %d bytes, not %d", maxTokenSize, len(token))
	}
	if len(seed) != ed25519.SeedSize {
		return fmt.Errorf("keys: a device key's seed is %d bytes, not %d", ed25519.SeedSize, len(seed))
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	unlock, err := k.o.lock()
	if err != nil {
		return err
	}
	defer unlock()
	m, err := k.reread() // another process may have changed it
	if err != nil {
		return err
	}
	k.m = m
	if m.Account != k.store.Kind() {
		// Listed first, so a store that keeps a secret while the call
		// fails can't leave a copy Destroy doesn't know to delete.
		m.Account = k.store.Kind()
		if err := writeMarker(k.o.dir, m); err != nil {
			return fmt.Errorf("keys: writing keystore.json: %w", err)
		}
		k.m = m
	}
	items := m.accountItems()
	for i, secret := range [][]byte{token, seed} {
		if _, err := k.o.put(k.store, items[i], secret); err != nil {
			return fmt.Errorf("keys: keeping the device's sign-in in %s: %w", k.store.Kind().Description(), err)
		}
	}
	return nil
}

// Account returns the device token and key seed SetAccount kept. Its error
// wraps ErrNotFound when there are none: the installation isn't linked, or
// pairtime logout forgot them. It reads keystore.json again, since
// `pairtime login` runs in another process than the agent that asks.
func (k *Keystore) Account() (token, seed []byte, err error) {
	k.mu.Lock()
	m, err := k.reread()
	if err == nil {
		k.m = m
	}
	k.mu.Unlock()
	if err != nil {
		return nil, nil, err
	}
	if m.Account == "" {
		return nil, nil, fmt.Errorf("%w: this installation isn't linked to a pairtime server", ErrNotFound)
	}
	s := k.o.storeOf(m.Account)
	if s == nil {
		return nil, nil, fmt.Errorf("%w: keystore.json says the device's sign-in is in %s, which this system doesn't have", ErrUnavailable, m.Account.Description())
	}
	items := m.accountItems()
	token, err = k.o.get(s, items[0])
	if err != nil {
		return nil, nil, err
	}
	seed, err = k.o.get(s, items[1])
	if err != nil {
		clear(token)
		return nil, nil, err
	}
	if len(seed) != ed25519.SeedSize || len(token) == 0 || len(token) > maxTokenSize {
		clear(token)
		clear(seed)
		return nil, nil, fmt.Errorf("%w: the device's sign-in in %s isn't what pairtime login wrote", ErrDamaged, s.Kind().Description())
	}
	return token, seed, nil
}

// ForgetAccount deletes the device token and key seed, for pairtime
// logout. Forgetting what isn't there does nothing. When a store won't
// delete them, keystore.json keeps listing them, so a later ForgetAccount
// or Destroy can finish.
func (k *Keystore) ForgetAccount() error {
	k.mu.Lock()
	defer k.mu.Unlock()
	unlock, err := k.o.lock()
	if err != nil {
		return err
	}
	defer unlock()
	m, err := k.reread()
	if err != nil {
		return err
	}
	k.m = m
	if m.Account == "" {
		return nil
	}
	s := k.o.storeOf(m.Account)
	if s == nil {
		return fmt.Errorf("%w: keystore.json says the device's sign-in is in %s, which this system doesn't have", ErrUnavailable, m.Account.Description())
	}
	var errs []error
	for _, item := range m.accountItems() {
		if err := k.o.erase(s, item); err != nil {
			errs = append(errs, fmt.Errorf("keys: deleting the device's sign-in from %s: %w", s.Kind().Description(), err))
		}
	}
	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	m.Account = ""
	if err := writeMarker(k.o.dir, m); err != nil {
		return fmt.Errorf("keys: writing keystore.json: %w", err)
	}
	k.m = m
	return nil
}

// Chain returns the device id of the ledger chain recorded in the
// keystore of the data directory dir (RecordChain), or "" when the ledger
// writer hasn't started one, from keystore.json alone, without asking any
// store for a key. `pairtime login` sends it as the device's id
// (docs/spec/device-auth-v1.md section 1).
func Chain(dir string) (string, error) {
	if !filepath.IsAbs(dir) {
		return "", fmt.Errorf("keys: the data directory must be an absolute path, not %q", dir)
	}
	m, err := readMarker(filepath.Join(filepath.Clean(dir), dirName))
	if isNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return m.Chain, nil
}
