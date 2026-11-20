// SPDX-License-Identifier: AGPL-3.0-or-later

package keys

import (
	"github.com/some-du6e/pairtime/internal/platform/linux"
)

// platformStores is the desktop's Secret Service, through go-keyring
// (DESIGN 3.6). Where there is no session bus it fails at once, and the
// first run puts the key in the key file.
func platformStores(string) []Store {
	return []Store{secretServiceStore{linux.SecretService{Service: keyringService}}}
}

type secretServiceStore struct{ ss linux.SecretService }

func (secretServiceStore) Kind() Kind { return KindSecretService }

func (s secretServiceStore) Get(name string) ([]byte, error) {
	b, err := s.ss.Get(name)
	return b, storeErr(err, linux.ErrNotFound, linux.ErrBadItem)
}

func (s secretServiceStore) Set(name string, secret []byte) error {
	return s.ss.Set(name, secret)
}

func (s secretServiceStore) Delete(name string) error {
	return storeErr(s.ss.Delete(name), linux.ErrNotFound, linux.ErrBadItem)
}
