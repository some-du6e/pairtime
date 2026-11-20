// SPDX-License-Identifier: AGPL-3.0-or-later

// Package backup holds what pairtimed keeps outside the database and how it
// gets off the host (DESIGN 10.2, 10.3):
//
//   - the server's keys in /data/keys, which `pairtimed init` makes once
//     (Generate) and `pairtimed keys backup` encrypts to Karim's offline age
//     key (WriteKeyBackup);
//   - the nightly snapshot: a VACUUM INTO copy of the database, encrypted
//     with age as it streams to the snapshot bucket (Snapshot);
//   - how far Litestream's replica is behind the database, for /readyz and
//     pairtimed doctor (Replication).
//
// age (filippo.io/age, BSD-3-Clause) does the encryption, in pairtimed only:
// the standard library has no ChaCha20-Poly1305 and no age format, and the
// point of age is that Karim decrypts with the age tool alone, offline,
// without pairtime's code.
package backup

import (
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// The files in the keys directory (DESIGN 10.2).
const (
	// ServerKeyFile signs receipts, manifests and day roots: an Ed25519
	// private key in PKCS #8 PEM.
	ServerKeyFile = "server.ed25519"
	// EvidenceKeyFile seals tier-2 content (DESIGN 7.6): an ECDH P-256
	// private key in PKCS #8 PEM.
	EvidenceKeyFile = "evidence.p256"
	// PepperFile keys HMAC(pepper, root_sha) and the name HMACs: 32
	// random bytes as 64 hex digits. It never changes; losing it breaks
	// project identity.
	PepperFile = "pepper"
)

// KeyFiles are the key files, in the order Generate writes them.
var KeyFiles = []string{ServerKeyFile, EvidenceKeyFile, PepperFile}

// PepperSize is the pepper's length in bytes.
const PepperSize = 32

// ErrKeysExist is what Generate returns, wrapped, when a key file is
// already there. It never replaces one: a new server key would break every
// receipt, and a new pepper every project's identity.
var ErrKeysExist = errors.New("backup: the keys already exist")

// KeysDir is the keys directory inside data_dir.
func KeysDir(dataDir string) string { return filepath.Join(dataDir, "keys") }

// Keys are the server's private keys.
type Keys struct {
	Server   ed25519.PrivateKey
	Evidence *ecdh.PrivateKey
	Pepper   []byte
}

// Public is what may be shown of Keys: the public halves and their ids.
type Public struct {
	Server   ed25519.PublicKey
	Evidence *ecdh.PublicKey
}

// Public returns the public halves of k.
func (k *Keys) Public() Public {
	return Public{Server: k.Server.Public().(ed25519.PublicKey), Evidence: k.Evidence.PublicKey()}
}

// KeyID names a public key: the first 16 hex digits of the SHA-256 of its
// bytes, the raw 32 bytes for Ed25519 and the uncompressed point for
// P-256. Release keys are named the same way (docs/spec/update-manifest.md).
func KeyID(pub []byte) string {
	sum := sha256.Sum256(pub)
	return hex.EncodeToString(sum[:8])
}

// ServerKeyID is the server key's id.
func (p Public) ServerKeyID() string { return KeyID(p.Server) }

// EvidenceKeyID is the evidence key's id.
func (p Public) EvidenceKeyID() string { return KeyID(p.Evidence.Bytes()) }

// Generate makes the three keys with random and writes them to dir, each
// with mode 0600, creating dir with mode 0700 if it is missing. When any
// key file is already there, it writes none and returns ErrKeysExist. A
// dir that group or others can enter is refused.
func Generate(dir string, random io.Reader) (*Keys, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("backup: %w", err)
	}
	if err := checkPrivateDir(dir); err != nil {
		return nil, err
	}
	var there []string
	for _, name := range KeyFiles {
		if _, err := os.Lstat(filepath.Join(dir, name)); err == nil {
			there = append(there, name)
		} else if !errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("backup: %w", err)
		}
	}
	if len(there) > 0 {
		return nil, fmt.Errorf("%w in %s: %s. pairtimed init never replaces a key: a new server key breaks every receipt, and a new pepper every project's identity (DESIGN 10.2)",
			ErrKeysExist, dir, strings.Join(there, ", "))
	}

	_, server, err := ed25519.GenerateKey(random)
	if err != nil {
		return nil, fmt.Errorf("backup: making the server key: %w", err)
	}
	evidence, err := ecdh.P256().GenerateKey(random)
	if err != nil {
		return nil, fmt.Errorf("backup: making the evidence key: %w", err)
	}
	pepper := make([]byte, PepperSize)
	if _, err := io.ReadFull(random, pepper); err != nil {
		return nil, fmt.Errorf("backup: making the pepper: %w", err)
	}
	k := &Keys{Server: server, Evidence: evidence, Pepper: pepper}
	files, err := k.encode()
	if err != nil {
		return nil, err
	}
	var written []string
	for _, name := range KeyFiles {
		path := filepath.Join(dir, name)
		if err := writeNew(path, files[name]); err != nil {
			for _, w := range written {
				os.Remove(w)
			}
			return nil, err
		}
		written = append(written, path)
	}
	if err := syncDir(dir); err != nil {
		return nil, err
	}
	return k, nil
}

// encode returns each key file's bytes.
func (k *Keys) encode() (map[string][]byte, error) {
	server, err := x509.MarshalPKCS8PrivateKey(k.Server)
	if err != nil {
		return nil, fmt.Errorf("backup: encoding the server key: %w", err)
	}
	evidence, err := x509.MarshalPKCS8PrivateKey(k.Evidence)
	if err != nil {
		return nil, fmt.Errorf("backup: encoding the evidence key: %w", err)
	}
	return map[string][]byte{
		ServerKeyFile:   pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: server}),
		EvidenceKeyFile: pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: evidence}),
		PepperFile:      []byte(hex.EncodeToString(k.Pepper) + "\n"),
	}, nil
}

// Load reads the keys in dir and checks each one: present, mode 0600 or
// tighter, and of the right kind.
func Load(dir string) (*Keys, error) {
	if err := checkPrivateDir(dir); err != nil {
		return nil, err
	}
	data := make(map[string][]byte, len(KeyFiles))
	for _, name := range KeyFiles {
		b, err := readPrivate(filepath.Join(dir, name))
		if err != nil {
			return nil, err
		}
		data[name] = b
	}
	k := &Keys{}
	priv, err := parsePKCS8(data[ServerKeyFile], ServerKeyFile)
	if err != nil {
		return nil, err
	}
	var ok bool
	if k.Server, ok = priv.(ed25519.PrivateKey); !ok {
		return nil, fmt.Errorf("backup: %s holds a %T, not an Ed25519 key", ServerKeyFile, priv)
	}
	if priv, err = parsePKCS8(data[EvidenceKeyFile], EvidenceKeyFile); err != nil {
		return nil, err
	}
	ec, ok := priv.(*ecdsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("backup: %s holds a %T, not a P-256 key", EvidenceKeyFile, priv)
	}
	if k.Evidence, err = ec.ECDH(); err != nil || k.Evidence.Curve() != ecdh.P256() {
		return nil, fmt.Errorf("backup: %s isn't a P-256 key", EvidenceKeyFile)
	}
	pepper, err := hex.DecodeString(strings.TrimSpace(string(data[PepperFile])))
	if err != nil || len(pepper) != PepperSize {
		return nil, fmt.Errorf("backup: %s must hold %d bytes as %d hex digits", PepperFile, PepperSize, 2*PepperSize)
	}
	k.Pepper = pepper
	return k, nil
}

func parsePKCS8(data []byte, name string) (any, error) {
	block, rest := pem.Decode(data)
	if block == nil || block.Type != "PRIVATE KEY" || len(strings.TrimSpace(string(rest))) > 0 {
		return nil, fmt.Errorf("backup: %s must hold one PEM block of type PRIVATE KEY", name)
	}
	priv, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("backup: %s: %w", name, err)
	}
	return priv, nil
}

// readPrivate reads a key file, refusing one that group or others can
// read or that isn't a regular file.
func readPrivate(path string) ([]byte, error) {
	fi, err := os.Lstat(path)
	if err != nil {
		return nil, fmt.Errorf("backup: %w", err)
	}
	if !fi.Mode().IsRegular() {
		return nil, fmt.Errorf("backup: %s isn't a regular file", path)
	}
	if runtime.GOOS != "windows" && fi.Mode().Perm()&0o077 != 0 {
		return nil, fmt.Errorf("backup: %s has mode %04o, and a key must not be readable by group or others: chmod 600 %s", path, fi.Mode().Perm(), path)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("backup: %w", err)
	}
	return b, nil
}

// checkPrivateDir refuses a directory that group or others can enter.
func checkPrivateDir(dir string) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	fi, err := os.Stat(dir)
	if err != nil {
		return fmt.Errorf("backup: %w", err)
	}
	if !fi.IsDir() {
		return fmt.Errorf("backup: %s isn't a directory", dir)
	}
	if fi.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("backup: %s has mode %04o, and the keys directory must be closed to group and others: chmod 700 %s", dir, fi.Mode().Perm(), dir)
	}
	return nil
}

// writeNew writes data to a file that must not exist yet, with mode 0600,
// and syncs it.
func writeNew(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("backup: %w", err)
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(path)
		return fmt.Errorf("backup: writing %s: %w", path, err)
	}
	if err := f.Sync(); err != nil {
		f.Close()
		os.Remove(path)
		return fmt.Errorf("backup: writing %s: %w", path, err)
	}
	if err := f.Close(); err != nil {
		os.Remove(path)
		return fmt.Errorf("backup: writing %s: %w", path, err)
	}
	return nil
}

// syncDir syncs a directory, so the files just made in it survive a power
// cut. Windows can't open a directory for that, and skips it.
func syncDir(dir string) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	d, err := os.Open(dir)
	if err != nil {
		return fmt.Errorf("backup: %w", err)
	}
	defer d.Close()
	if err := d.Sync(); err != nil {
		return fmt.Errorf("backup: syncing %s: %w", dir, err)
	}
	return nil
}

// Rand is the randomness Generate uses outside tests.
var Rand io.Reader = rand.Reader
