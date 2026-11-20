// SPDX-License-Identifier: AGPL-3.0-or-later

// Command release turns built pairtime binaries into a release: one archive
// per platform, checksums.txt, and manifest.json, the signed update
// manifest. It also makes release keys and checks a finished release.
// .github/workflows/release.yml runs it, and docs/spec/update-manifest.md
// specifies what it writes.
//
// Usage:
//
//	release keygen [-pub internal/release/release-keys.txt]
//	release keys
//	release min-version [-file tools/release/minimum-version.txt]
//	release check -version v [-min-version v]
//	release dist -version v -commit hash -date time -min-version v -base-url url
//	    -targets "os/arch ..." -bin dir -out dir -payload file [-extra files]
//	release sign -payload file -out manifest.json (-key-env NAME | -throwaway) [-pub-out file]
//	release verify -dir dir [-pub file] [-targets "os/arch ..."]
package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/some-du6e/pairtime/internal/release"
)

const usage = `release builds and checks pairtime releases (docs/spec/update-manifest.md).

Usage:
  release keygen [-pub file]   make a release key: append its public half to the
                               key list, print its secret half once
  release keys                 print the ids of the release keys compiled in
  release min-version          print the one version minimum-version.txt holds
  release check -version v     check a release version (and -min-version)
  release dist ...             write the archives, checksums.txt and the
                               manifest payload
  release sign ...             sign the payload into manifest.json
  release verify -dir dir      check manifest.json, checksums.txt and the archives

"release <command> -h" lists a command's flags.
`

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// usageError is a mistake on the command line. run exits 2 for it.
type usageError string

func (e usageError) Error() string { return string(e) }

var commands = map[string]func(args []string, stdout, stderr io.Writer) error{
	"keygen":      cmdKeygen,
	"keys":        cmdKeys,
	"min-version": cmdMinVersion,
	"check":       cmdCheck,
	"dist":        cmdDist,
	"sign":        cmdSign,
	"verify":      cmdVerify,
}

// run executes one command line and returns the exit code: 0 on success,
// 1 when the command fails, 2 for a usage error.
func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return 2
	}
	switch args[0] {
	case "help", "-h", "--help":
		fmt.Fprint(stdout, usage)
		return 0
	}
	cmd, ok := commands[args[0]]
	if !ok {
		fmt.Fprintf(stderr, "release: unknown command %q\n\n%s", args[0], usage)
		return 2
	}
	err := cmd(args[1:], stdout, stderr)
	var ue usageError
	switch {
	case err == nil, errors.Is(err, flag.ErrHelp):
		return 0
	case errors.As(err, &ue):
		if ue != "" { // "" means the flag package already said what's wrong
			fmt.Fprintf(stderr, "release %s: %v\n", args[0], ue)
		}
		return 2
	}
	prefix := ""
	if os.Getenv("GITHUB_ACTIONS") == "true" {
		prefix = "::error::" // an annotation on the workflow run
	}
	fmt.Fprintf(stderr, "%srelease %s: %v\n", prefix, args[0], err)
	return 1
}

func newFlags(name string, stderr io.Writer) *flag.FlagSet {
	set := flag.NewFlagSet("release "+name, flag.ContinueOnError)
	set.SetOutput(stderr)
	return set
}

// parse parses args and requires every flag in required to be set.
func parse(set *flag.FlagSet, args []string, required ...string) error {
	if err := set.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return err
		}
		return usageError("")
	}
	if set.NArg() > 0 {
		return usageError(fmt.Sprintf("unexpected argument %q", set.Arg(0)))
	}
	for _, name := range required {
		if set.Lookup(name).Value.String() == "" {
			return usageError("-" + name + " is required")
		}
	}
	return nil
}

func cmdKeygen(args []string, stdout, stderr io.Writer) error {
	flags := newFlags("keygen", stderr)
	pubPath := flags.String("pub", "internal/release/release-keys.txt", "the key list to append the public key to; it must already exist")
	if err := parse(flags, args); err != nil {
		return err
	}
	old, err := os.ReadFile(*pubPath)
	if err != nil {
		return fmt.Errorf("%w (run keygen from the repository root, or pass -pub)", err)
	}
	listed, err := release.ParsePublicKeys(string(old))
	if err != nil {
		return fmt.Errorf("%s: %w", *pubPath, err)
	}
	key, err := release.GenerateSecretKey(rand.Reader)
	if err != nil {
		return err
	}
	pub := key.Public()
	var add strings.Builder
	if len(old) > 0 && !bytes.HasSuffix(old, []byte("\n")) {
		add.WriteString("\n")
	}
	fmt.Fprintf(&add, "\n# Key %s, made %s with tools/release keygen.\n%s\n", pub.ID(), time.Now().UTC().Format(time.DateOnly), pub)
	f, err := os.OpenFile(*pubPath, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		return err
	}
	if _, err := f.WriteString(add.String()); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	// The one place a secret key is ever written: standard output, once.
	if _, err := fmt.Fprintln(stdout, key.Text()); err != nil {
		return fmt.Errorf("printing the secret key: %w; delete key %s from %s and run keygen again", err, pub.ID(), *pubPath)
	}
	if len(listed) == 0 {
		fmt.Fprintf(stderr, "Added release public key %s to %s. Commit that file.\n"+
			"The secret key went to standard output and is stored nowhere else.\n"+
			"Save it as the RELEASE_SIGNING_KEY secret of the release environment, and keep an offline copy.\n"+
			"See docs/spec/update-manifest.md, Making the key.\n", pub.ID(), *pubPath)
		return nil
	}
	// A replacement. Installed clients trust only the keys already listed,
	// so storing this key as RELEASE_SIGNING_KEY now would sign the next
	// release with a key none of them trusts, and GitHub never shows the
	// old secret again.
	var ids []string
	for _, k := range listed {
		ids = append(ids, k.ID())
	}
	fmt.Fprintf(stderr, "Added release public key %s to %s, which already listed %s. Commit that file.\n"+
		"The secret key went to standard output and is stored nowhere else. Keep it offline for now.\n"+
		"Leave RELEASE_SIGNING_KEY as it is: installed clients trust only the keys already listed, so the release that adds this one must be signed with the current key.\n"+
		"Move this key into RELEASE_SIGNING_KEY once clients run a build that trusts it.\n"+
		"See docs/spec/update-manifest.md, Replacing a key.\n", pub.ID(), *pubPath, strings.Join(ids, ", "))
	return nil
}

func cmdKeys(args []string, stdout, stderr io.Writer) error {
	flags := newFlags("keys", stderr)
	if err := parse(flags, args); err != nil {
		return err
	}
	keys, err := release.TrustedKeys()
	if err != nil {
		return fmt.Errorf("internal/release/release-keys.txt: %w", err)
	}
	for _, k := range keys {
		fmt.Fprintln(stdout, k.ID())
	}
	return nil
}

func cmdMinVersion(args []string, stdout, stderr io.Writer) error {
	flags := newFlags("min-version", stderr)
	file := flags.String("file", "tools/release/minimum-version.txt", "the file holding the oldest client version a tagged release supports")
	if err := parse(flags, args); err != nil {
		return err
	}
	v, err := readMinVersion(*file)
	if err != nil {
		return err
	}
	fmt.Fprintln(stdout, v)
	return nil
}

// readMinVersion returns the version a minimum-version.txt holds. Blank
// lines and lines starting with # are skipped, and exactly one other line
// must be left, holding a pairtime version and nothing else.
func readMinVersion(p string) (release.Version, error) {
	data, err := os.ReadFile(p)
	if err != nil {
		return release.Version{}, err
	}
	var versions []release.Version
	for i, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		v, err := release.ParseVersion(line)
		if err != nil {
			return release.Version{}, fmt.Errorf("%s line %d: %w", p, i+1, err)
		}
		versions = append(versions, v)
	}
	if len(versions) != 1 {
		return release.Version{}, fmt.Errorf("%s holds %d versions; it must hold exactly one", p, len(versions))
	}
	return versions[0], nil
}

func cmdCheck(args []string, stdout, stderr io.Writer) error {
	flags := newFlags("check", stderr)
	version := flags.String("version", "", "the release version, such as v0.1.0-alpha.1")
	minVersion := flags.String("min-version", "", "the oldest client version the release supports, at most -version")
	if err := parse(flags, args, "version"); err != nil {
		return err
	}
	v, err := release.ParseVersion(*version)
	if err != nil {
		return err
	}
	if *minVersion != "" {
		m, err := release.ParseVersion(*minVersion)
		if err != nil {
			return fmt.Errorf("-min-version: %w", err)
		}
		if m.Compare(v) > 0 {
			return fmt.Errorf("-min-version %s is newer than %s", m, v)
		}
	}
	kind := "release"
	if v.Prerelease() {
		kind = "prerelease"
	}
	fmt.Fprintf(stdout, "%s is a pairtime version, published as a %s\n", v, kind)
	return nil
}

type target struct{ goos, goarch string }

func (t target) String() string { return t.goos + "/" + t.goarch }

// exe is the binary's file name, at the archive's root.
func (t target) exe() string {
	if t.goos == "windows" {
		return "pairtime.exe"
	}
	return "pairtime"
}

// archiveName is a target's archive file name, such as
// pairtime_0.1.0-alpha.1_windows_amd64.zip. Windows gets zip, the rest
// tar.gz.
func archiveName(version string, t target) string {
	ext := ".tar.gz"
	if t.goos == "windows" {
		ext = ".zip"
	}
	return "pairtime_" + strings.TrimPrefix(version, "v") + "_" + t.goos + "_" + t.goarch + ext
}

func parseTargets(s string) ([]target, error) {
	var ts []target
	for _, f := range strings.Fields(s) {
		goos, goarch, ok := strings.Cut(f, "/")
		t := target{goos, goarch}
		if !ok || !isName(goos) || !isName(goarch) {
			return nil, fmt.Errorf("target %q isn't os/arch, like linux/amd64", f)
		}
		if slices.Contains(ts, t) {
			return nil, fmt.Errorf("target %s is listed twice", t)
		}
		ts = append(ts, t)
	}
	if len(ts) == 0 {
		return nil, errors.New("no targets")
	}
	return ts, nil
}

func isName(s string) bool {
	for i := 0; i < len(s); i++ {
		if c := s[i]; (c < 'a' || c > 'z') && (c < '0' || c > '9') {
			return false
		}
	}
	return s != ""
}

func cmdDist(args []string, stdout, stderr io.Writer) error {
	flags := newFlags("dist", stderr)
	version := flags.String("version", "", "the release version the binaries were stamped with")
	commit := flags.String("commit", "", "the full git commit hash the binaries were built from")
	date := flags.String("date", "", "the commit time the binaries were stamped with, RFC 3339 in UTC")
	minVersion := flags.String("min-version", "", "the oldest client version the release supports")
	baseURL := flags.String("base-url", "", "the URL the archives are published under")
	targetList := flags.String("targets", "", "the targets, space-separated os/arch pairs")
	binDir := flags.String("bin", "", "the directory holding <os>_<arch>/pairtime[.exe] for each target")
	extra := flags.String("extra", "", "comma-separated files every archive carries beside the binary, such as LICENSE,README.md")
	outDir := flags.String("out", "", "the directory to create for the archives and checksums.txt; it must not exist")
	payloadPath := flags.String("payload", "", "the file to write the unsigned manifest payload to")
	if err := parse(flags, args, "version", "commit", "date", "min-version", "base-url", "targets", "bin", "out", "payload"); err != nil {
		return err
	}
	targets, err := parseTargets(*targetList)
	if err != nil {
		return err
	}
	base := strings.TrimSuffix(*baseURL, "/")
	m := &release.Manifest{Version: *version, Commit: *commit, Date: *date, MinimumVersion: *minVersion}

	// Check every flag the manifest carries before building anything.
	zero := strings.Repeat("0", 64)
	probe := *m
	probe.Artifacts = []release.Artifact{{OS: "linux", Arch: "amd64", URL: base + "/probe", SHA256: zero, Size: 1, BinarySHA256: zero}}
	if err := probe.Validate(); err != nil {
		return err
	}
	mtime, err := time.Parse(time.RFC3339, *date)
	if err != nil {
		return err
	}

	var extras []archiveFile
	if *extra != "" {
		for _, p := range strings.Split(*extra, ",") {
			data, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			extras = append(extras, archiveFile{name: filepath.Base(p), mode: 0o644, data: data})
		}
	}
	if err := os.Mkdir(*outDir, 0o755); err != nil {
		return err
	}
	sums := map[string]string{}
	for _, t := range targets {
		bin, err := os.ReadFile(filepath.Join(*binDir, t.goos+"_"+t.goarch, t.exe()))
		if err != nil {
			return fmt.Errorf("%s: %w", t, err)
		}
		files := append([]archiveFile{{name: t.exe(), mode: 0o755, data: bin}}, extras...)
		var buf bytes.Buffer
		if t.goos == "windows" {
			err = writeZip(&buf, files, mtime)
		} else {
			err = writeTarGz(&buf, files, mtime)
		}
		if err != nil {
			return fmt.Errorf("%s: %w", t, err)
		}
		name := archiveName(*version, t)
		if err := os.WriteFile(filepath.Join(*outDir, name), buf.Bytes(), 0o644); err != nil {
			return err
		}
		sum := sha256Hex(buf.Bytes())
		sums[name] = sum
		m.Artifacts = append(m.Artifacts, release.Artifact{
			OS:           t.goos,
			Arch:         t.goarch,
			URL:          base + "/" + name,
			SHA256:       sum,
			Size:         int64(buf.Len()),
			BinarySHA256: sha256Hex(bin),
		})
		fmt.Fprintf(stdout, "%s  %d bytes  sha256 %s\n", name, buf.Len(), sum)
	}
	payload, err := release.EncodeManifest(m)
	if err != nil {
		return err
	}
	if err := writeChecksums(filepath.Join(*outDir, "checksums.txt"), sums); err != nil {
		return err
	}
	return os.WriteFile(*payloadPath, payload, 0o644)
}

// archiveFile is one file in an archive. Every file sits at the root.
type archiveFile struct {
	name string
	mode fs.FileMode
	data []byte
}

// writeTarGz writes a reproducible tar.gz: fixed order, owner 0, the
// commit time on every file, and no time or name in the gzip header.
func writeTarGz(w io.Writer, files []archiveFile, mtime time.Time) error {
	gz, err := gzip.NewWriterLevel(w, gzip.BestCompression)
	if err != nil {
		return err
	}
	tw := tar.NewWriter(gz)
	for _, f := range files {
		hdr := &tar.Header{
			Typeflag: tar.TypeReg,
			Name:     f.name,
			Mode:     int64(f.mode.Perm()),
			Size:     int64(len(f.data)),
			ModTime:  mtime,
			Format:   tar.FormatUSTAR,
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		if _, err := tw.Write(f.data); err != nil {
			return err
		}
	}
	if err := tw.Close(); err != nil {
		return err
	}
	return gz.Close()
}

// writeZip writes a reproducible zip with Unix modes, so the binary stays
// executable when unpacked anywhere.
func writeZip(w io.Writer, files []archiveFile, mtime time.Time) error {
	zw := zip.NewWriter(w)
	for _, f := range files {
		hdr := &zip.FileHeader{Name: f.name, Method: zip.Deflate, Modified: mtime.UTC()}
		hdr.SetMode(f.mode.Perm())
		fw, err := zw.CreateHeader(hdr)
		if err != nil {
			return err
		}
		if _, err := fw.Write(f.data); err != nil {
			return err
		}
	}
	return zw.Close()
}

// writeChecksums writes sha256sum's format, sorted by file name, so
// `sha256sum -c checksums.txt` checks the release.
func writeChecksums(p string, sums map[string]string) error {
	var b strings.Builder
	for _, name := range sortedKeys(sums) {
		fmt.Fprintf(&b, "%s  %s\n", sums[name], name)
	}
	return os.WriteFile(p, []byte(b.String()), 0o644)
}

func readChecksums(p string) (map[string]string, error) {
	data, err := os.ReadFile(p)
	if err != nil {
		return nil, err
	}
	sums := map[string]string{}
	text := strings.TrimSuffix(string(data), "\n")
	for i, line := range strings.Split(text, "\n") {
		sum, name, ok := strings.Cut(line, "  ")
		if !ok || !isSHA256(sum) || !isBaseName(name) {
			return nil, fmt.Errorf("checksums.txt line %d isn't a SHA-256 in lowercase hex, two spaces and a file name", i+1)
		}
		if _, dup := sums[name]; dup {
			return nil, fmt.Errorf("checksums.txt lists %s twice", name)
		}
		sums[name] = sum
	}
	if text == "" {
		return nil, errors.New("checksums.txt is empty")
	}
	return sums, nil
}

// isBaseName reports whether name is a plain file name, with no directory.
func isBaseName(name string) bool {
	return name != "" && name != "." && name != ".." && !strings.ContainsAny(name, "/\\")
}

func isSHA256(s string) bool {
	if len(s) != 64 {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil && strings.ToLower(s) == s
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

func cmdSign(args []string, stdout, stderr io.Writer) error {
	flags := newFlags("sign", stderr)
	payloadPath := flags.String("payload", "", "the manifest payload dist wrote")
	outPath := flags.String("out", "", "where to write manifest.json")
	keyEnv := flags.String("key-env", "", "the environment variable that holds the secret key, such as RELEASE_SIGNING_KEY")
	throwaway := flags.Bool("throwaway", false, "sign with a new key that is used once and never stored, for snapshots")
	pubOut := flags.String("pub-out", "", "a file to write the signing key's public half to")
	if err := parse(flags, args, "payload", "out"); err != nil {
		return err
	}
	if (*keyEnv != "") == *throwaway {
		return usageError("give exactly one of -key-env and -throwaway")
	}
	payload, err := os.ReadFile(*payloadPath)
	if err != nil {
		return err
	}
	var key release.SecretKey
	kind := "throwaway key"
	if *throwaway {
		key, err = release.GenerateSecretKey(rand.Reader)
		if err != nil {
			return err
		}
	} else {
		kind = "release key"
		text := os.Getenv(*keyEnv)
		if strings.TrimSpace(text) == "" {
			return fmt.Errorf("$%s is empty or unset", *keyEnv)
		}
		// ParseSecretKey's errors never quote the key.
		if key, err = release.ParseSecretKey(text); err != nil {
			return fmt.Errorf("$%s: %w", *keyEnv, err)
		}
	}
	data, err := release.Sign(payload, key)
	if err != nil {
		return err
	}
	if err := os.WriteFile(*outPath, data, 0o644); err != nil {
		return err
	}
	if *pubOut != "" {
		if err := os.WriteFile(*pubOut, []byte(key.Public().String()+"\n"), 0o644); err != nil {
			return err
		}
	}
	fmt.Fprintf(stdout, "signed %s with %s %s\n", *outPath, kind, key.Public().ID())
	return nil
}

func cmdVerify(args []string, stdout, stderr io.Writer) error {
	flags := newFlags("verify", stderr)
	dir := flags.String("dir", "", "the release directory: manifest.json, checksums.txt and the archives")
	pubPath := flags.String("pub", "", "a key list to trust instead of the keys compiled in")
	targetList := flags.String("targets", "", "if set, the targets the manifest must list, space-separated")
	if err := parse(flags, args, "dir"); err != nil {
		return err
	}
	var trusted []release.PublicKey
	source := "internal/release/release-keys.txt"
	var err error
	if *pubPath != "" {
		source = *pubPath
		text, err := os.ReadFile(*pubPath)
		if err != nil {
			return err
		}
		trusted, err = release.ParsePublicKeys(string(text))
		if err != nil {
			return fmt.Errorf("%s: %w", *pubPath, err)
		}
	} else if trusted, err = release.TrustedKeys(); err != nil {
		return fmt.Errorf("%s: %w", source, err)
	}
	if len(trusted) == 0 {
		return fmt.Errorf("%s lists no release key, so nothing can be verified", source)
	}

	data, err := os.ReadFile(filepath.Join(*dir, "manifest.json"))
	if err != nil {
		return err
	}
	m, signer, err := release.Open(data, trusted)
	if err != nil {
		return fmt.Errorf("manifest.json: %w", err)
	}
	sums, err := readChecksums(filepath.Join(*dir, "checksums.txt"))
	if err != nil {
		return err
	}
	for _, name := range sortedKeys(sums) {
		b, err := os.ReadFile(filepath.Join(*dir, name))
		if err != nil {
			return fmt.Errorf("checksums.txt: %w", err)
		}
		if got := sha256Hex(b); got != sums[name] {
			return fmt.Errorf("checksums.txt: %s has SHA-256 %s, not %s", name, got, sums[name])
		}
	}
	var got []string
	for _, a := range m.Artifacts {
		t := target{a.OS, a.Arch}
		got = append(got, t.String())
		name := archiveName(m.Version, t)
		u, err := url.Parse(a.URL)
		if err != nil {
			return err
		}
		if path.Base(u.Path) != name {
			return fmt.Errorf("%s: the manifest's url names %q, not %s", t, path.Base(u.Path), name)
		}
		b, err := os.ReadFile(filepath.Join(*dir, name))
		if err != nil {
			return err
		}
		if int64(len(b)) != a.Size || sha256Hex(b) != a.SHA256 {
			return fmt.Errorf("%s: %d bytes with SHA-256 %s, but the manifest says %d bytes with %s", name, len(b), sha256Hex(b), a.Size, a.SHA256)
		}
		if sums[name] != a.SHA256 {
			return fmt.Errorf("checksums.txt doesn't list %s with the manifest's SHA-256", name)
		}
		bin, err := readBinary(b, t)
		if err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
		if sha256Hex(bin) != a.BinarySHA256 {
			return fmt.Errorf("%s: %s has SHA-256 %s, but the manifest says %s", name, t.exe(), sha256Hex(bin), a.BinarySHA256)
		}
	}
	if *targetList != "" {
		want, err := parseTargets(*targetList)
		if err != nil {
			return err
		}
		var wantNames []string
		for _, t := range want {
			wantNames = append(wantNames, t.String())
		}
		slices.Sort(wantNames)
		slices.Sort(got)
		if !slices.Equal(got, wantNames) {
			return fmt.Errorf("the manifest lists %s, not %s", strings.Join(got, " "), strings.Join(wantNames, " "))
		}
	}
	fmt.Fprintf(stdout, "%s: manifest.json for %s is signed by trusted key %s, and its %d archives match it and checksums.txt\n",
		*dir, m.Version, signer.ID(), len(m.Artifacts))
	return nil
}

// readBinary returns the pairtime binary from an archive, and checks that
// every entry is a file at the archive's root and the binary is
// executable.
func readBinary(archive []byte, t target) ([]byte, error) {
	var bin []byte
	found := false
	check := func(name string, mode fs.FileMode, open func() ([]byte, error)) error {
		if !isBaseName(name) || !mode.IsRegular() {
			return fmt.Errorf("entry %q isn't a file at the archive's root", name)
		}
		if name != t.exe() {
			return nil
		}
		if found {
			return fmt.Errorf("two entries are named %s", name)
		}
		if mode.Perm() != 0o755 {
			return fmt.Errorf("%s has mode %v, not 0755", name, mode.Perm())
		}
		b, err := open()
		bin, found = b, true
		return err
	}
	if t.goos == "windows" {
		zr, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
		if err != nil {
			return nil, err
		}
		for _, f := range zr.File {
			err := check(f.Name, f.Mode(), func() ([]byte, error) {
				rc, err := f.Open()
				if err != nil {
					return nil, err
				}
				defer rc.Close()
				return io.ReadAll(rc) // checks the entry's CRC-32 at the end
			})
			if err != nil {
				return nil, err
			}
		}
	} else {
		gz, err := gzip.NewReader(bytes.NewReader(archive))
		if err != nil {
			return nil, err
		}
		tr := tar.NewReader(gz)
		for {
			hdr, err := tr.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				return nil, err
			}
			if err := check(hdr.Name, hdr.FileInfo().Mode(), func() ([]byte, error) { return io.ReadAll(tr) }); err != nil {
				return nil, err
			}
		}
	}
	if !found {
		return nil, fmt.Errorf("no %s at the archive's root", t.exe())
	}
	return bin, nil
}
