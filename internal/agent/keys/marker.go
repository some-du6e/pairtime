// SPDX-License-Identifier: AGPL-3.0-or-later

package keys

import (
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/some-du6e/pairtime/internal/agent/home"
)

// The keystore's files, in the data directory.
const (
	// dirName is the keystore's directory. It holds keystore.json, the
	// lock, and the DPAPI blobs or key files.
	dirName = "keys"
	// markerName is keystore.json, which says where the device key is.
	markerName = "keystore.json"
	// markerVersion is keystore.json's format.
	markerVersion = 1
	// lockName is the file home.Acquire locks in the keystore's directory,
	// home's agent.lock. Destroy leaves it.
	lockName = "agent.lock"
)

// Item names. Each is prefixed with the installation's id and a dot.
const (
	itemDevice   = "kdev"  // this installation's K_dev
	itemArchived = "kdev-" // followed by the key's id: a restored K_dev
)

// idLabel is the HKDF-SHA256 info string of a device key's id, a public
// fingerprint that keystore.json stores beside the store's name, so a key
// read back from a store can be checked without keeping a copy. It follows
// ledger-v1.md section 3's rule that every label under pairtime/v1/ is used
// for one value only. The id is 16 bytes of HKDF output, so it reveals
// nothing about K_dev or the keys derived from it.
const idLabel = "pairtime/v1/key-id"

// keySize is the size of K_dev, as pkg/ledger defines it.
const keySize = 32

// idSize is the size of a key id in bytes; keystore.json has it in hex.
const idSize = 16

// marker is keystore.json. It holds no secret. While Store and KeyID are
// empty, a first run is under way, or was cut off and is finished by the
// next Open under the same installation id.
type marker struct {
	V int `json:"v"`
	// Install is the installation's random id, which prefixes every item
	// name, so installations that share an account (PAIRTIME_HOME) keep
	// their keys apart in the Keychain or the Secret Service.
	Install string `json:"install"`
	// Store is where K_dev is.
	Store Kind `json:"store,omitempty"`
	// KeyID is K_dev's id (idLabel).
	KeyID   string    `json:"key_id,omitempty"`
	Created time.Time `json:"created,omitzero"`
	// Reason says why the OS store wasn't used, when Store is KindFile.
	Reason string `json:"fallback_reason,omitempty"`
	// Unconfirmed lists the stores a first run gave K_dev to and then
	// didn't use, without being sure they don't have it: the save didn't
	// answer in time and may yet finish, or a store that saved it
	// couldn't be made to delete it again. Each is written here before the
	// first run tries the next store, and Destroy deletes K_dev from them
	// as from Store.
	Unconfirmed []Kind `json:"unconfirmed,omitempty"`
	// Archived lists the device keys restored from other installations.
	// Archive lists each one here before it puts the key in a store.
	Archived []archivedKey `json:"archived,omitempty"`
	// Chain is the device id of the ledger chain K_dev writes, once the
	// ledger writer has started it (RecordChain). One K_dev writes one
	// chain (ledger-v1 3), and the id is kept here rather than only in the
	// ledger directory, so a ledger directory that is deleted or emptied
	// can't make the key number records from s = 1 again.
	Chain string `json:"chain,omitempty"`
	// Account is the store that holds the device's sign-in, the token and
	// key seed of `pairtime login` (SetAccount), or "" while there is none.
	// It is the store that holds K_dev, and it is written here before the
	// store gets them.
	Account Kind `json:"account,omitempty"`
}

type archivedKey struct {
	ID    string    `json:"key_id"`
	Store Kind      `json:"store"`
	Added time.Time `json:"added"`
}

func (m marker) complete() bool { return m.Store != "" && m.KeyID != "" }

func (m marker) deviceItem() string { return m.Install + "." + itemDevice }

func (m marker) archivedItem(id string) string { return m.Install + "." + itemArchived + id }

func (m marker) archived(id string) (archivedKey, bool) {
	for _, a := range m.Archived {
		if sameID(a.ID, id) {
			return a, true
		}
	}
	return archivedKey{}, false
}

func (m marker) validate() error {
	switch {
	case m.V > markerVersion:
		return fmt.Errorf("%w: keystore.json has format %d, newer than this pairtime reads, so update pairtime", ErrUnavailable, m.V)
	case m.V != markerVersion:
		return fmt.Errorf("%w: keystore.json has format %d", ErrDamaged, m.V)
	case !isHexID(m.Install):
		return fmt.Errorf("%w: keystore.json has no valid installation id", ErrDamaged)
	case (m.Store == "") != (m.KeyID == ""):
		return fmt.Errorf("%w: keystore.json names a store without a key id, or a key id without a store", ErrDamaged)
	case m.Store != "" && !m.Store.known():
		return fmt.Errorf("%w: keystore.json names %s", ErrDamaged, m.Store.Description())
	case m.KeyID != "" && !isHexID(m.KeyID):
		return fmt.Errorf("%w: keystore.json has no valid key id", ErrDamaged)
	case m.Chain != "" && (m.Store == "" || !isDeviceID(m.Chain)):
		return fmt.Errorf("%w: keystore.json names a ledger chain that isn't a device id, or one for a device key it doesn't have", ErrDamaged)
	case m.Account != "" && (m.Store == "" || !m.Account.known()):
		return fmt.Errorf("%w: keystore.json names a store for the device's sign-in that isn't valid, or one without a device key", ErrDamaged)
	}
	for i, k := range m.Unconfirmed {
		if !k.known() || slices.Contains(m.Unconfirmed[:i], k) {
			return fmt.Errorf("%w: keystore.json lists a store that may hold the device key that isn't valid", ErrDamaged)
		}
	}
	seen := make(map[string]bool, len(m.Archived))
	for _, a := range m.Archived {
		if !isHexID(a.ID) || !a.Store.known() || seen[a.ID] {
			return fmt.Errorf("%w: keystore.json lists an archived key that isn't valid", ErrDamaged)
		}
		seen[a.ID] = true
	}
	return nil
}

// isHexID reports whether s is idSize bytes in lowercase hex.
func isHexID(s string) bool {
	if len(s) != 2*idSize {
		return false
	}
	for i := 0; i < len(s); i++ {
		if c := s[i]; (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// isDeviceID reports whether s can be a device id, a pkg/event opaque key:
// 1 to 128 ASCII letters, digits, '_' or '-'.
func isDeviceID(s string) bool {
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

// readMarker reads keystore.json from dir. Its error wraps fs.ErrNotExist
// when there is none, and ErrDamaged when it can't be used.
func readMarker(dir string) (marker, error) {
	data, err := os.ReadFile(filepath.Join(dir, markerName))
	if err != nil {
		return marker{}, err
	}
	var m marker
	if err := json.Unmarshal(data, &m); err != nil {
		return marker{}, fmt.Errorf("%w: keystore.json isn't valid JSON: %v", ErrDamaged, err)
	}
	if err := m.validate(); err != nil {
		return marker{}, err
	}
	return m, nil
}

// writeMarker replaces keystore.json by atomic rename. It is user-only like
// the key files, though it holds no secret.
func writeMarker(dir string, m marker) error {
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return home.WritePrivate(filepath.Join(dir, markerName), append(data, '\n'))
}

// newInstallID returns a random installation id.
func newInstallID() string {
	b := make([]byte, idSize)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// keyID returns the id of the device key kDev: HKDF-SHA256 with a salt of
// 32 zero bytes, as in ledger-v1.md section 3, info idLabel and L = 16.
func keyID(kDev []byte) (string, error) {
	id, err := hkdf.Key(sha256.New, kDev, make([]byte, sha256.Size), idLabel, idSize)
	if err != nil {
		return "", fmt.Errorf("keys: deriving the key id: %w", err)
	}
	return hex.EncodeToString(id), nil
}

// sameID compares two key ids in constant time. Ids are public, but one is
// computed from a secret just read, so nothing about it leaks through
// timing either.
func sameID(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

// errNoKeystore is Inspect's error before the first run has made a key.
var errNoKeystore = fmt.Errorf("keys: this installation has no device key yet: %w", os.ErrNotExist)

// isNotExist reports whether err means there is no keystore.json.
func isNotExist(err error) bool { return errors.Is(err, os.ErrNotExist) }
