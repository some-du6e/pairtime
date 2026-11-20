// SPDX-License-Identifier: AGPL-3.0-or-later

package backup

import (
	"archive/tar"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"

	"filippo.io/age"
	"filippo.io/age/armor"
)

// ParseRecipient reads Karim's age public key, the age1... recipient the
// nightly snapshots and the key backups are encrypted to (DESIGN 10.2,
// 10.3). It refuses an age private key outright, without quoting it: the
// private key must never be on the host.
func ParseRecipient(s string) (age.Recipient, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, errors.New("backup: no age recipient; give Karim's age public key, which starts with age1")
	}
	if upper := strings.ToUpper(s); strings.HasPrefix(upper, "AGE-SECRET-KEY-") || strings.HasPrefix(upper, "AGE-PLUGIN-") {
		return nil, errors.New("backup: that is an age private key, which must never be on this host. Give the public key that goes with it, which starts with age1, and treat the private key as exposed")
	}
	if strings.ContainsAny(s, "\r\n") {
		return nil, errors.New("backup: give one age recipient, on one line")
	}
	rs, err := age.ParseRecipients(strings.NewReader(s))
	if err != nil {
		return nil, fmt.Errorf("backup: the age recipient isn't an age public key such as age1...: %w", err)
	}
	if len(rs) != 1 {
		return nil, errors.New("backup: give exactly one age recipient")
	}
	return rs[0], nil
}

// KeyBackupPrefix is where `pairtimed keys backup -upload` stores key
// backups in the main bucket. Not in the snapshot bucket, whose 30-day
// lifecycle would delete them.
const KeyBackupPrefix = "backups/keys/"

// keyBackupTime is the time format in a key backup's name.
const keyBackupTime = "20060102T150405Z"

// KeyBackupName is the file name of a key backup made at t, such as
// pairtime-keys-20261120T101500Z.tar.age.
func KeyBackupName(t time.Time) string {
	return "pairtime-keys-" + t.UTC().Format(keyBackupTime) + ".tar.age"
}

// EmailKeyFile is the name the email key has in a key backup. It lives in
// secrets.env, not /data/keys, and losing it loses every stored email
// address, so it is backed up with the keys (DESIGN 4.4, 10.2).
const EmailKeyFile = "PAIRTIME_EMAIL_KEY"

// archiveDir is the directory a key backup unpacks into.
const archiveDir = "pairtime-keys"

// keyBackupReadme is the README.txt inside a key backup.
const keyBackupReadme = `This is a backup of a pairtimed server's keys (DESIGN 10.2), made by
pairtimed keys backup. Decrypt it offline, on the machine that holds the
age identity it was encrypted to. That identity never goes near the
server:

  age -d -i <identity file> pairtime-keys-<time>.tar.age | tar -x

Then send only the pairtime-keys/ directory to the server's host, and
there:

  install -d -m 0700 -o 65532 -g 65532 data/keys
  install -m 0600 -o 65532 -g 65532 pairtime-keys/server.ed25519 \
    pairtime-keys/evidence.p256 pairtime-keys/pepper data/keys/

If PAIRTIME_EMAIL_KEY is here, put its one line back into
config/secrets.env as PAIRTIME_EMAIL_KEY=<value>. Then shred the
directory on both machines: it holds the keys in the clear
(deploy/runbook.md, Restoring the keys).
`

// WriteKeyBackup writes the backup of the keys in dir to w: a tar archive
// of the three key files, byte for byte, and of the email key when
// emailKey isn't empty, encrypted with age to r and ASCII-armored, so the
// backup can be pasted as text. Karim restores it offline with the age and
// tar tools alone (keyBackupReadme). The keys are loaded first, so a
// backup never holds a key pairtimed can't read.
func WriteKeyBackup(w io.Writer, dir, emailKey string, r age.Recipient, now time.Time) error {
	if _, err := Load(dir); err != nil {
		return err
	}
	type entry struct {
		name string
		data []byte
	}
	var files []entry
	for _, name := range KeyFiles {
		data, err := readPrivate(filepath.Join(dir, name))
		if err != nil {
			return err
		}
		files = append(files, entry{name, data})
	}
	if emailKey != "" {
		files = append(files, entry{EmailKeyFile, []byte(emailKey + "\n")})
	}
	files = append(files, entry{"README.txt", []byte(keyBackupReadme)})

	aw := armor.NewWriter(w)
	ew, err := age.Encrypt(aw, r)
	if err != nil {
		return fmt.Errorf("backup: %w", err)
	}
	tw := tar.NewWriter(ew)
	mtime := now.UTC().Truncate(time.Second)
	if err := tw.WriteHeader(&tar.Header{Typeflag: tar.TypeDir, Name: archiveDir + "/", Mode: 0o700, ModTime: mtime, Format: tar.FormatUSTAR}); err != nil {
		return fmt.Errorf("backup: %w", err)
	}
	for _, f := range files {
		hdr := &tar.Header{
			Typeflag: tar.TypeReg, Name: archiveDir + "/" + f.name, Mode: 0o600,
			Size: int64(len(f.data)), ModTime: mtime, Format: tar.FormatUSTAR,
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return fmt.Errorf("backup: %w", err)
		}
		if _, err := tw.Write(f.data); err != nil {
			return fmt.Errorf("backup: %w", err)
		}
	}
	if err := tw.Close(); err != nil {
		return fmt.Errorf("backup: %w", err)
	}
	if err := ew.Close(); err != nil {
		return fmt.Errorf("backup: %w", err)
	}
	if err := aw.Close(); err != nil {
		return fmt.Errorf("backup: %w", err)
	}
	return nil
}
