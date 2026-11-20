// SPDX-License-Identifier: AGPL-3.0-or-later

package keys

import (
	"github.com/some-du6e/pairtime/internal/platform/darwin"
)

// platformStores is the user's login Keychain, through go-keyring (DESIGN
// 3.6).
func platformStores(string) []Store {
	return []Store{keychainStore{darwin.Keychain{Service: keyringService}}}
}

type keychainStore struct{ k darwin.Keychain }

func (keychainStore) Kind() Kind { return KindKeychain }

func (s keychainStore) Get(name string) ([]byte, error) {
	b, err := s.k.Get(name)
	return b, storeErr(err, darwin.ErrNotFound, darwin.ErrBadItem)
}

func (s keychainStore) Set(name string, secret []byte) error {
	return s.k.Set(name, secret)
}

func (s keychainStore) Delete(name string) error {
	return storeErr(s.k.Delete(name), darwin.ErrNotFound, darwin.ErrBadItem)
}
