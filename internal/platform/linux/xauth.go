// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build linux

package linux

import (
	"bytes"
	"encoding/binary"
	"io"
	"os"
	"path/filepath"
)

// Xauthority address families (X11 Xauth.h).
const (
	familyLocal = 256   // a connection on this host; the address is its hostname
	familyWild  = 65535 // any address
)

// cookieAuth is the one authorization scheme pairtime sends. X servers
// started by display managers hand out MIT-MAGIC-COOKIE-1 cookies; a
// server with no Xauthority entry for the display usually accepts local
// clients without one, so a missing cookie means trying with none.
const cookieAuth = "MIT-MAGIC-COOKIE-1"

// maxXauthority bounds how much of the Xauthority file is read. Real files
// hold a few entries of a few dozen bytes each.
const maxXauthority = 1 << 20

// xauthEntry is one record of an Xauthority file.
type xauthEntry struct {
	family  uint16
	address []byte
	number  []byte // the display number in ASCII; empty matches any display
	name    []byte
	data    []byte
}

// parseXauthority reads the records of an Xauthority file: each is a
// big-endian family, then address, display number, auth name and auth data
// as 16-bit-length-prefixed byte strings (libXau's AuRead.c). A damaged
// tail is ignored, as libXau does, keeping the records before it.
func parseXauthority(b []byte) []xauthEntry {
	var entries []xauthEntry
	r := bytes.NewReader(b)
	for {
		var fam uint16
		if binary.Read(r, binary.BigEndian, &fam) != nil {
			return entries
		}
		e := xauthEntry{family: fam}
		for _, field := range []*[]byte{&e.address, &e.number, &e.name, &e.data} {
			var n uint16
			if binary.Read(r, binary.BigEndian, &n) != nil {
				return entries
			}
			v := make([]byte, n)
			if _, err := io.ReadFull(r, v); err != nil {
				return entries
			}
			*field = v
		}
		entries = append(entries, e)
	}
}

// findCookie returns the MIT-MAGIC-COOKIE-1 data for display number on
// this host, as libxcb picks it: the first record whose family is wild, or
// local with hostname as its address, whose display number is number or
// empty, and whose scheme is MIT-MAGIC-COOKIE-1. It returns nil when no
// record fits.
func findCookie(entries []xauthEntry, hostname, number string) []byte {
	for _, e := range entries {
		if e.family != familyWild && (e.family != familyLocal || string(e.address) != hostname) {
			continue
		}
		if len(e.number) != 0 && string(e.number) != number {
			continue
		}
		if string(e.name) == cookieAuth {
			return e.data
		}
	}
	return nil
}

// xauthorityPath is where Xlib looks for the Xauthority file: $XAUTHORITY,
// else ~/.Xauthority. It returns "" when neither is known.
func xauthorityPath(getenv func(string) string) string {
	if p := getenv("XAUTHORITY"); p != "" {
		return p
	}
	if home := getenv("HOME"); home != "" {
		return filepath.Join(home, ".Xauthority")
	}
	return ""
}

// readCookie returns the cookie for display number from the Xauthority
// file, or nil when there is none to send.
func readCookie(getenv func(string) string, hostname, number string) []byte {
	path := xauthorityPath(getenv)
	if path == "" {
		return nil
	}
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, maxXauthority))
	if err != nil {
		return nil
	}
	return findCookie(parseXauthority(b), hostname, number)
}
