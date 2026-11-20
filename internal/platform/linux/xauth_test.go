// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build linux

package linux

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

// xauthRecord encodes one Xauthority record the way xauth writes it.
func xauthRecord(family uint16, address, number, name string, data []byte) []byte {
	var b bytes.Buffer
	binary.Write(&b, binary.BigEndian, family)
	for _, field := range [][]byte{[]byte(address), []byte(number), []byte(name), data} {
		binary.Write(&b, binary.BigEndian, uint16(len(field)))
		b.Write(field)
	}
	return b.Bytes()
}

func TestFindCookie(t *testing.T) {
	mine := bytes.Repeat([]byte{1}, 16)
	other := bytes.Repeat([]byte{2}, 16)
	cases := []struct {
		name    string
		records [][]byte
		number  string
		want    []byte
	}{
		{
			name:    "a local record for this host and display is used",
			records: [][]byte{xauthRecord(familyLocal, "box", "0", cookieAuth, mine)},
			number:  "0", want: mine,
		},
		{
			name:    "a wildcard-family record matches whatever the host",
			records: [][]byte{xauthRecord(familyWild, "", "0", cookieAuth, mine)},
			number:  "0", want: mine,
		},
		{
			name: "a record for another display number is skipped",
			records: [][]byte{
				xauthRecord(familyLocal, "box", "1", cookieAuth, other),
				xauthRecord(familyLocal, "box", "0", cookieAuth, mine),
			},
			number: "0", want: mine,
		},
		{
			name:    "a record with no display number matches every display",
			records: [][]byte{xauthRecord(familyLocal, "box", "", cookieAuth, mine)},
			number:  "7", want: mine,
		},
		{
			name:    "a record for another host is skipped",
			records: [][]byte{xauthRecord(familyLocal, "elsewhere", "0", cookieAuth, other)},
			number:  "0", want: nil,
		},
		{
			name:    "a TCP record is skipped, since only local displays are dialed",
			records: [][]byte{xauthRecord(0, "\x7f\x00\x00\x01", "0", cookieAuth, other)},
			number:  "0", want: nil,
		},
		{
			name: "a record for another scheme is skipped",
			records: [][]byte{
				xauthRecord(familyLocal, "box", "0", "XDM-AUTHORIZATION-1", other),
				xauthRecord(familyLocal, "box", "0", cookieAuth, mine),
			},
			number: "0", want: mine,
		},
		{
			name: "the first fitting record wins",
			records: [][]byte{
				xauthRecord(familyLocal, "box", "0", cookieAuth, mine),
				xauthRecord(familyWild, "", "", cookieAuth, other),
			},
			number: "0", want: mine,
		},
		{
			name: "a damaged tail keeps the records before it",
			records: [][]byte{
				xauthRecord(familyLocal, "box", "0", cookieAuth, mine),
				xauthRecord(familyWild, "", "0", cookieAuth, other)[:5],
			},
			number: "0", want: mine,
		},
		{
			name:    "an empty file has no cookie",
			records: nil,
			number:  "0", want: nil,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := findCookie(parseXauthority(bytes.Join(tc.records, nil)), "box", tc.number)
			if !bytes.Equal(got, tc.want) {
				t.Errorf("cookie %x, want %x", got, tc.want)
			}
		})
	}
}

func TestReadCookie(t *testing.T) {
	dir := t.TempDir()
	cookie := bytes.Repeat([]byte{7}, 16)
	file := filepath.Join(dir, "auth")
	if err := os.WriteFile(file, xauthRecord(familyLocal, "box", "0", cookieAuth, cookie), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".Xauthority"), xauthRecord(familyWild, "", "", cookieAuth, cookie), 0o600); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		env  map[string]string
		want []byte
	}{
		{"XAUTHORITY names the file", map[string]string{"XAUTHORITY": file}, cookie},
		{"without XAUTHORITY the file is ~/.Xauthority", map[string]string{"HOME": dir}, cookie},
		{"a missing file means no cookie", map[string]string{"XAUTHORITY": filepath.Join(dir, "missing")}, nil},
		{"no XAUTHORITY and no HOME means no cookie", map[string]string{}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := readCookie(func(k string) string { return tc.env[k] }, "box", "0")
			if !bytes.Equal(got, tc.want) {
				t.Errorf("cookie %x, want %x", got, tc.want)
			}
		})
	}
}
