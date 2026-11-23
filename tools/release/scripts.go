// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/some-du6e/pairtime/internal/release"
)

// cmdScripts writes the release's copies of the install scripts, each the
// repository's copy stamped with the release's version and the keys in
// release-keys.txt (release.InstallScript.Stamp). With -payload, it also
// lists their SHA-256s in the manifest payload dist wrote, as
// install_scripts, before the payload is signed: `curl | sh` runs the
// script first, so the signature covers it as it covers the archives
// (D-immutable-releases; docs/spec/update-manifest.md).
func cmdScripts(args []string, stdout, stderr io.Writer) error {
	flags := newFlags("scripts", stderr)
	version := flags.String("version", "", "the release version the scripts install")
	in := flags.String("in", "install", "the directory holding install.sh and install.ps1")
	out := flags.String("out", "", "the directory to write the release's copies to; it must exist")
	payloadPath := flags.String("payload", "", "the manifest payload dist wrote, to list the scripts' SHA-256s in before it's signed")
	if err := parse(flags, args, "version", "out"); err != nil {
		return err
	}
	var m *release.Manifest
	if *payloadPath != "" {
		data, err := os.ReadFile(*payloadPath)
		if err != nil {
			return err
		}
		if m, err = release.DecodeManifest(data); err != nil {
			return fmt.Errorf("%s: %w", *payloadPath, err)
		}
		if m.Version != *version {
			return fmt.Errorf("%s is the payload for %s, not %s", *payloadPath, m.Version, *version)
		}
	}
	keys, err := release.TrustedKeys()
	if err != nil {
		return fmt.Errorf("internal/release/release-keys.txt: %w", err)
	}
	texts := make([]string, len(keys))
	ids := make([]string, len(keys))
	for i, k := range keys {
		texts[i] = k.String()
		ids[i] = k.ID()
	}
	scripts := release.InstallScripts()
	stamped := make([][]byte, len(scripts))
	for i, s := range scripts {
		src, err := os.ReadFile(filepath.Join(*in, s.Name))
		if err != nil {
			return err
		}
		if stamped[i], err = s.Stamp(src, *version, texts); err != nil {
			return fmt.Errorf("%s: %w", s.Name, err)
		}
	}
	// Both are checked, and the payload encoded, before anything is
	// written.
	var payload []byte
	if m != nil {
		m.InstallScripts = map[string]string{}
		for i, s := range scripts {
			m.InstallScripts[s.Name] = sha256Hex(stamped[i])
		}
		if payload, err = release.EncodeManifest(m); err != nil {
			return err
		}
	}
	for i, s := range scripts {
		if err := os.WriteFile(filepath.Join(*out, s.Name), stamped[i], 0o644); err != nil {
			return err
		}
	}
	trusted := "none, so they refuse its manifest unless told which key to trust"
	if len(ids) > 0 {
		trusted = strings.Join(ids, ", ")
	}
	fmt.Fprintf(stdout, "wrote install.sh and install.ps1 to %s: they install %s and trust release keys %s\n", *out, *version, trusted)
	if m != nil {
		if err := os.WriteFile(*payloadPath, payload, 0o644); err != nil {
			return err
		}
		fmt.Fprintf(stdout, "listed their SHA-256s in %s\n", *payloadPath)
	}
	return nil
}
