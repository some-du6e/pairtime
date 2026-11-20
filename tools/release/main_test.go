// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/internal/release"
)

const (
	testVersion = "v0.1.0-alpha.1"
	testCommit  = "68726d8ccf480210ce26078e4d3006120e6b49a1"
	testDate    = "2026-11-19T10:49:00Z"
	testTargets = "windows/386 darwin/arm64 linux/amd64"
	testBase    = "https://github.com/some-du6e/pairtime/releases/download/v0.1.0-alpha.1"
)

// cli runs one command line and returns its exit code and output.
func cli(t *testing.T, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code = run(args, &out, &errOut)
	return code, out.String(), errOut.String()
}

func mustCLI(t *testing.T, args ...string) string {
	t.Helper()
	code, out, errOut := cli(t, args...)
	if code != 0 {
		t.Fatalf("release %s: exit %d\n%s%s", strings.Join(args, " "), code, out, errOut)
	}
	return out
}

func write(t *testing.T, p, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, p string) []byte {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// fixture is what release.yml's build job has before dist runs: a binary
// per target, LICENSE and README.md.
type fixture struct {
	dir, bin, extra, out, payload, manifest, pub string
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	dir := t.TempDir()
	f := fixture{
		dir:     dir,
		bin:     filepath.Join(dir, "bin"),
		out:     filepath.Join(dir, "dist"),
		payload: filepath.Join(dir, "payload.json"),
		pub:     filepath.Join(dir, "snapshot-key.pub"),
	}
	f.manifest = filepath.Join(f.out, "manifest.json")
	for _, s := range strings.Fields(testTargets) {
		goos, goarch, _ := strings.Cut(s, "/")
		tg := target{goos, goarch}
		write(t, filepath.Join(f.bin, goos+"_"+goarch, tg.exe()), "stand-in pairtime binary for "+s)
	}
	license, readme := filepath.Join(dir, "LICENSE"), filepath.Join(dir, "README.md")
	write(t, license, "GNU AFFERO GENERAL PUBLIC LICENSE (stand-in)\n")
	write(t, readme, "# pairtime\n")
	f.extra = license + "," + readme
	return f
}

func (f fixture) distArgs(out, payload string) []string {
	return []string{"dist", "-version", testVersion, "-commit", testCommit, "-date", testDate,
		"-min-version", "v0.0.0", "-base-url", testBase, "-targets", testTargets,
		"-bin", f.bin, "-extra", f.extra, "-out", out, "-payload", payload}
}

// build runs dist, then sign with a throwaway key, as release.yml does.
func (f fixture) build(t *testing.T) {
	t.Helper()
	mustCLI(t, f.distArgs(f.out, f.payload)...)
	mustCLI(t, "sign", "-throwaway", "-payload", f.payload, "-out", f.manifest, "-pub-out", f.pub)
}

func (f fixture) trusted(t *testing.T) []release.PublicKey {
	t.Helper()
	keys, err := release.ParsePublicKeys(string(read(t, f.pub)))
	if err != nil || len(keys) != 1 {
		t.Fatalf("%s: %v keys, err %v", f.pub, len(keys), err)
	}
	return keys
}

type entry struct {
	name  string
	mode  os.FileMode
	mtime time.Time
	data  string
}

// entries lists an archive's files, read without readBinary.
func entries(t *testing.T, p string) []entry {
	t.Helper()
	data := read(t, p)
	var es []entry
	if strings.HasSuffix(p, ".zip") {
		zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			t.Fatal(err)
		}
		for _, f := range zr.File {
			rc, err := f.Open()
			if err != nil {
				t.Fatal(err)
			}
			b, err := io.ReadAll(rc)
			rc.Close()
			if err != nil {
				t.Fatal(err)
			}
			es = append(es, entry{f.Name, f.Mode(), f.Modified.UTC(), string(b)})
		}
		return es
	}
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return es
		}
		if err != nil {
			t.Fatal(err)
		}
		b, err := io.ReadAll(tr)
		if err != nil {
			t.Fatal(err)
		}
		es = append(es, entry{hdr.Name, hdr.FileInfo().Mode(), hdr.ModTime.UTC(), string(b)})
	}
}

func TestBuildWritesARelease(t *testing.T) {
	f := newFixture(t)
	f.build(t)
	out := mustCLI(t, "verify", "-dir", f.out, "-pub", f.pub, "-targets", testTargets)
	if !strings.Contains(out, "signed by trusted key") {
		t.Errorf("verify printed %q", out)
	}
	m, _, err := release.Open(read(t, f.manifest), f.trusted(t))
	if err != nil {
		t.Fatal(err)
	}
	date, _ := time.Parse(time.RFC3339, testDate)

	t.Run("the directory holds one archive per target, checksums.txt and manifest.json", func(t *testing.T) {
		des, err := os.ReadDir(f.out)
		if err != nil {
			t.Fatal(err)
		}
		var names []string
		for _, de := range des {
			names = append(names, de.Name())
		}
		want := []string{
			"checksums.txt", "manifest.json",
			"pairtime_0.1.0-alpha.1_darwin_arm64.tar.gz",
			"pairtime_0.1.0-alpha.1_linux_amd64.tar.gz",
			"pairtime_0.1.0-alpha.1_windows_386.zip",
		}
		if !slices.Equal(names, want) {
			t.Errorf("files = %v, want %v", names, want)
		}
	})

	t.Run("checksums.txt is sha256sum's format, sorted, archives only", func(t *testing.T) {
		lines := strings.Split(strings.TrimSuffix(string(read(t, filepath.Join(f.out, "checksums.txt"))), "\n"), "\n")
		line := regexp.MustCompile(`^[0-9a-f]{64}  pairtime_0\.1\.0-alpha\.1_[a-z0-9]+_[a-z0-9]+\.(zip|tar\.gz)$`)
		for _, l := range lines {
			if !line.MatchString(l) {
				t.Errorf("line %q isn't `<sha256>  <archive>`", l)
			}
		}
		if len(lines) != 3 {
			t.Errorf("want 3 lines, got %q", lines)
		}
		names := make([]string, len(lines))
		for i, l := range lines {
			names[i] = l[66:]
		}
		if !slices.IsSorted(names) {
			t.Errorf("names aren't sorted: %v", names)
		}
	})

	t.Run("the manifest carries the release's version, commit, date and minimum", func(t *testing.T) {
		if m.Version != testVersion || m.Commit != testCommit || m.Date != testDate || m.MinimumVersion != "v0.0.0" {
			t.Errorf("manifest = %+v", m)
		}
		var got []string
		for _, a := range m.Artifacts {
			got = append(got, a.OS+"/"+a.Arch+" "+a.URL)
		}
		want := []string{
			"windows/386 " + testBase + "/pairtime_0.1.0-alpha.1_windows_386.zip",
			"darwin/arm64 " + testBase + "/pairtime_0.1.0-alpha.1_darwin_arm64.tar.gz",
			"linux/amd64 " + testBase + "/pairtime_0.1.0-alpha.1_linux_amd64.tar.gz",
		}
		if !slices.Equal(got, want) {
			t.Errorf("artifacts = %q, want %q", got, want)
		}
	})

	t.Run("every archive holds the binary, LICENSE and README.md at its root with Unix modes and the commit time", func(t *testing.T) {
		for _, a := range m.Artifacts {
			tg := target{a.OS, a.Arch}
			es := entries(t, filepath.Join(f.out, archiveName(testVersion, tg)))
			want := []entry{
				{tg.exe(), 0o755, date, "stand-in pairtime binary for " + tg.String()},
				{"LICENSE", 0o644, date, "GNU AFFERO GENERAL PUBLIC LICENSE (stand-in)\n"},
				{"README.md", 0o644, date, "# pairtime\n"},
			}
			if len(es) != len(want) {
				t.Fatalf("%s: entries %+v", tg, es)
			}
			for i := range want {
				g, w := es[i], want[i]
				if g.name != w.name || g.mode != w.mode || !g.mtime.Equal(w.mtime) || g.data != w.data {
					t.Errorf("%s entry %d = %+v, want %+v", tg, i, g, w)
				}
			}
			if a.BinarySHA256 != sha256Hex([]byte(want[0].data)) {
				t.Errorf("%s: binary_sha256 isn't the binary's", tg)
			}
		}
	})

	t.Run("a tar.gz holds no build time and no owner, so neither the clock nor the user changes its bytes", func(t *testing.T) {
		for _, a := range m.Artifacts {
			tg := target{a.OS, a.Arch}
			if tg.goos == "windows" {
				continue // a zip, whose entries carry only the commit time
			}
			name := archiveName(testVersion, tg)
			gz, err := gzip.NewReader(bytes.NewReader(read(t, filepath.Join(f.out, name))))
			if err != nil {
				t.Fatal(err)
			}
			if h := gz.Header; !h.ModTime.IsZero() || h.Name != "" || h.Comment != "" || h.Extra != nil {
				t.Errorf("%s: gzip header has time %v, name %q, comment %q and extra %q; want none of them", name, h.ModTime, h.Name, h.Comment, h.Extra)
			}
			tr := tar.NewReader(gz)
			for {
				hdr, err := tr.Next()
				if err == io.EOF {
					break
				}
				if err != nil {
					t.Fatal(err)
				}
				if hdr.Uid != 0 || hdr.Gid != 0 || hdr.Uname != "" || hdr.Gname != "" {
					t.Errorf("%s: %s is owned by %d:%d (%q:%q), want 0:0 with no names", name, hdr.Name, hdr.Uid, hdr.Gid, hdr.Uname, hdr.Gname)
				}
			}
		}
	})

	t.Run("the payload in manifest.json is the one dist wrote", func(t *testing.T) {
		var env struct{ Payload string }
		if err := json.Unmarshal(read(t, f.manifest), &env); err != nil {
			t.Fatal(err)
		}
		want := read(t, f.payload)
		if got, _ := base64.StdEncoding.DecodeString(env.Payload); !bytes.Equal(got, want) {
			t.Errorf("payload = %s, want %s", got, want)
		}
	})
}

// TestDistIsReproducible runs dist twice in one process, moments apart, so
// it can't see a header taken from the clock, which gzip keeps in whole
// seconds, or from the user running it. TestBuildWritesARelease checks
// those headers directly.
func TestDistIsReproducible(t *testing.T) {
	f := newFixture(t)
	a, b := filepath.Join(f.dir, "a"), filepath.Join(f.dir, "b")
	mustCLI(t, f.distArgs(a, filepath.Join(f.dir, "a.json"))...)
	mustCLI(t, f.distArgs(b, filepath.Join(f.dir, "b.json"))...)
	des, err := os.ReadDir(a)
	if err != nil {
		t.Fatal(err)
	}
	for _, de := range des {
		if !bytes.Equal(read(t, filepath.Join(a, de.Name())), read(t, filepath.Join(b, de.Name()))) {
			t.Errorf("two dist runs wrote different %s", de.Name())
		}
	}
	if !bytes.Equal(read(t, filepath.Join(f.dir, "a.json")), read(t, filepath.Join(f.dir, "b.json"))) {
		t.Errorf("two dist runs wrote different payloads")
	}
}

// TestOneByteChangeIsRefused is WP-RL1's check: a manifest.json that
// tools/release wrote verifies, and changing any one byte of it doesn't.
func TestOneByteChangeIsRefused(t *testing.T) {
	f := newFixture(t)
	f.build(t)
	data := read(t, f.manifest)
	trusted := f.trusted(t)
	if _, _, err := release.Open(data, trusted); err != nil {
		t.Fatal(err)
	}
	for i := range data {
		bad := bytes.Clone(data)
		bad[i] ^= 0x01
		if _, _, err := release.Open(bad, trusted); err == nil {
			t.Fatalf("byte %d changed from %q to %q, and the manifest still verifies", i, data[i], bad[i])
		}
	}
	// The command refuses it too, with exit 1.
	for _, i := range []int{0, len(data) / 2, len(data) - 2} {
		bad := bytes.Clone(data)
		bad[i] ^= 0x01
		if err := os.WriteFile(f.manifest, bad, 0o644); err != nil {
			t.Fatal(err)
		}
		if code, _, errOut := cli(t, "verify", "-dir", f.out, "-pub", f.pub); code != 1 || !strings.Contains(errOut, "manifest.json:") {
			t.Errorf("verify with byte %d changed: exit %d, %q", i, code, errOut)
		}
	}
}

func copyDir(t *testing.T, src string) string {
	t.Helper()
	dst := t.TempDir()
	des, err := os.ReadDir(src)
	if err != nil {
		t.Fatal(err)
	}
	for _, de := range des {
		if err := os.WriteFile(filepath.Join(dst, de.Name()), read(t, filepath.Join(src, de.Name())), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dst
}

func TestVerifyRefuses(t *testing.T) {
	f := newFixture(t)
	f.build(t)
	linux := "pairtime_0.1.0-alpha.1_linux_amd64.tar.gz"
	windows := "pairtime_0.1.0-alpha.1_windows_386.zip"

	// resign replaces the manifest with one whose payload edit changed,
	// signed by a new throwaway key, and returns that key's file.
	resign := func(t *testing.T, dir string, edit func(m *release.Manifest)) string {
		m, err := release.DecodeManifest(read(t, f.payload))
		if err != nil {
			t.Fatal(err)
		}
		edit(m)
		payload, err := release.EncodeManifest(m)
		if err != nil {
			t.Fatal(err)
		}
		p, pub := filepath.Join(t.TempDir(), "payload.json"), filepath.Join(t.TempDir(), "key.pub")
		if err := os.WriteFile(p, payload, 0o644); err != nil {
			t.Fatal(err)
		}
		mustCLI(t, "sign", "-throwaway", "-payload", p, "-out", filepath.Join(dir, "manifest.json"), "-pub-out", pub)
		return pub
	}
	editFile := func(t *testing.T, p string, edit func([]byte) []byte) {
		if err := os.WriteFile(p, edit(read(t, p)), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	noCompiledKeys := "lists no release key"
	if keys, _ := release.TrustedKeys(); len(keys) > 0 {
		noCompiledKeys = "no signature verifies" // a real key exists, and it didn't sign this snapshot
	}

	tests := []struct {
		name    string
		setup   func(t *testing.T, dir string) (pub string) // "" keeps the snapshot key
		args    []string
		wantErr string
	}{
		{
			name: "an archive with a byte changed fails checksums.txt",
			setup: func(t *testing.T, dir string) string {
				editFile(t, filepath.Join(dir, linux), func(b []byte) []byte { b[len(b)/2] ^= 1; return b })
				return ""
			},
			wantErr: "checksums.txt: " + linux + " has SHA-256",
		},
		{
			name: "an archive swapped along with its checksums.txt line fails the manifest",
			setup: func(t *testing.T, dir string) string {
				other := read(t, filepath.Join(dir, "pairtime_0.1.0-alpha.1_darwin_arm64.tar.gz"))
				if err := os.WriteFile(filepath.Join(dir, linux), other, 0o644); err != nil {
					t.Fatal(err)
				}
				editFile(t, filepath.Join(dir, "checksums.txt"), func(b []byte) []byte {
					lines := strings.Split(string(b), "\n")
					for i, l := range lines {
						if strings.HasSuffix(l, linux) {
							lines[i] = sha256Hex(other) + "  " + linux
						}
					}
					return []byte(strings.Join(lines, "\n"))
				})
				return ""
			},
			wantErr: "but the manifest says",
		},
		{
			name: "checksums.txt without an archive's line",
			setup: func(t *testing.T, dir string) string {
				editFile(t, filepath.Join(dir, "checksums.txt"), func(b []byte) []byte {
					var keep []string
					for _, l := range strings.Split(string(b), "\n") {
						if !strings.HasSuffix(l, windows) {
							keep = append(keep, l)
						}
					}
					return []byte(strings.Join(keep, "\n"))
				})
				return ""
			},
			wantErr: "checksums.txt doesn't list " + windows,
		},
		{
			name: "checksums.txt naming a path outside the directory",
			setup: func(t *testing.T, dir string) string {
				editFile(t, filepath.Join(dir, "checksums.txt"), func(b []byte) []byte {
					return append(b, []byte(strings.Repeat("0", 64)+"  ../outside\n")...)
				})
				return ""
			},
			wantErr: "checksums.txt line 4",
		},
		{
			name: "a missing archive",
			setup: func(t *testing.T, dir string) string {
				if err := os.Remove(filepath.Join(dir, windows)); err != nil {
					t.Fatal(err)
				}
				return ""
			},
			wantErr: windows,
		},
		{
			name: "a manifest signed by a key the verifier doesn't trust",
			setup: func(t *testing.T, dir string) string {
				resign(t, dir, func(m *release.Manifest) {})
				return ""
			},
			wantErr: "no signature verifies",
		},
		{
			name: "a signed manifest whose binary hash doesn't match the archive's binary",
			setup: func(t *testing.T, dir string) string {
				return resign(t, dir, func(m *release.Manifest) { m.Artifacts[2].BinarySHA256 = strings.Repeat("ab", 32) })
			},
			wantErr: "pairtime has SHA-256",
		},
		{
			name: "a signed manifest whose url names another file",
			setup: func(t *testing.T, dir string) string {
				return resign(t, dir, func(m *release.Manifest) { m.Artifacts[0].URL = testBase + "/pairtime.zip" })
			},
			wantErr: `the manifest's url names "pairtime.zip"`,
		},
		{
			name:    "a target the manifest doesn't list",
			args:    []string{"-targets", testTargets + " linux/arm64"},
			wantErr: "the manifest lists darwin/arm64 linux/amd64 windows/386, not",
		},
		{
			name: "no key to trust, from the compiled-in list",
			setup: func(t *testing.T, dir string) string {
				return "-"
			},
			wantErr: noCompiledKeys,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := copyDir(t, f.out)
			pub := f.pub
			if tt.setup != nil {
				if p := tt.setup(t, dir); p != "" {
					pub = p
				}
			}
			args := []string{"verify", "-dir", dir}
			if pub != "-" {
				args = append(args, "-pub", pub)
			}
			args = append(args, tt.args...)
			code, _, errOut := cli(t, args...)
			if code != 1 || !strings.Contains(errOut, tt.wantErr) {
				t.Fatalf("exit %d, stderr %q; want exit 1 and %q", code, errOut, tt.wantErr)
			}
		})
	}
}

func TestKeygen(t *testing.T) {
	dir := t.TempDir()
	list := filepath.Join(dir, "release-keys.txt")
	write(t, list, "# a comment with no final newline")

	code, out, errOut := cli(t, "keygen", "-pub", list)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	secret, err := release.ParseSecretKey(out)
	if err != nil {
		t.Fatalf("stdout isn't a secret key: %v", err)
	}
	text := string(read(t, list))
	keys, err := release.ParsePublicKeys(text)
	tests := []struct {
		name string
		ok   bool
	}{
		{name: "the key list still parses", ok: err == nil},
		{name: "the public half was appended", ok: len(keys) == 1 && keys[0].String() == secret.Public().String()},
		{name: "the old content stays, ended by a newline", ok: strings.HasPrefix(text, "# a comment with no final newline\n")},
		{name: "a comment names the key id", ok: strings.Contains(text, "# Key "+secret.Public().ID()+", made ")},
		{name: "stdout is the secret key and nothing else", ok: out == secret.Text()+"\n"},
		{name: "stderr names the key id", ok: strings.Contains(errOut, secret.Public().ID())},
		{name: "stderr never shows the secret key", ok: !strings.Contains(errOut, strings.TrimPrefix(secret.Text(), "pairtime-release-ed25519-secret:"))},
		{name: "the list never holds the secret key", ok: !strings.Contains(text, strings.TrimPrefix(secret.Text(), "pairtime-release-ed25519-secret:"))},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !tt.ok {
				t.Errorf("failed; stdout %q, stderr %q, list:\n%s", out, errOut, text)
			}
		})
	}

	t.Run("a second key is appended after the first", func(t *testing.T) {
		mustCLI(t, "keygen", "-pub", list)
		keys, err := release.ParsePublicKeys(string(read(t, list)))
		if err != nil || len(keys) != 2 {
			t.Fatalf("%d keys, err %v", len(keys), err)
		}
	})
	t.Run("a missing key list is an error, not a new file", func(t *testing.T) {
		code, out, errOut := cli(t, "keygen", "-pub", filepath.Join(dir, "missing.txt"))
		if code != 1 || out != "" || !strings.Contains(errOut, "repository root") {
			t.Fatalf("exit %d, stdout %q, stderr %q", code, out, errOut)
		}
	})
	t.Run("a key list holding a secret key is refused before a key is made", func(t *testing.T) {
		bad := filepath.Join(dir, "bad.txt")
		write(t, bad, secret.Text()+"\n")
		code, out, errOut := cli(t, "keygen", "-pub", bad)
		if code != 1 || out != "" || !strings.Contains(errOut, "secret key") {
			t.Fatalf("exit %d, stdout %q, stderr %q", code, out, errOut)
		}
	})
}

func TestSignWithKeyEnv(t *testing.T) {
	f := newFixture(t)
	mustCLI(t, f.distArgs(f.out, f.payload)...)
	key, err := release.GenerateSecretKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	const env = "PAIRTIME_TEST_RELEASE_KEY"
	t.Setenv(env, key.Text()+"\n")
	out := mustCLI(t, "sign", "-key-env", env, "-payload", f.payload, "-out", f.manifest, "-pub-out", f.pub)
	if !strings.Contains(out, "release key "+key.Public().ID()) {
		t.Errorf("sign printed %q", out)
	}
	if got := string(read(t, f.pub)); got != key.Public().String()+"\n" {
		t.Errorf("-pub-out wrote %q", got)
	}
	mustCLI(t, "verify", "-dir", f.out, "-pub", f.pub)

	encoded := strings.TrimPrefix(key.Text(), "pairtime-release-ed25519-secret:")
	tests := []struct {
		name    string
		value   string
		unset   bool
		wantErr string
	}{
		{name: "an unset variable is an error", unset: true, wantErr: "$" + env + " is empty or unset"},
		{name: "an empty variable is an error", value: "  \n", wantErr: "$" + env + " is empty or unset"},
		{name: "a public key is refused", value: key.Public().String(), wantErr: "this is a release public key"},
		{name: "a cut-off key is refused without being printed", value: key.Text()[:60], wantErr: "padded standard base64"},
		{name: "a bare seed is refused without being printed", value: encoded, wantErr: "starts with"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(env, tt.value)
			if tt.unset {
				os.Unsetenv(env)
			}
			code, out, errOut := cli(t, "sign", "-key-env", env, "-payload", f.payload, "-out", filepath.Join(t.TempDir(), "m.json"))
			if code != 1 || !strings.Contains(errOut, tt.wantErr) {
				t.Fatalf("exit %d, stderr %q; want exit 1 and %q", code, errOut, tt.wantErr)
			}
			for _, secret := range []string{encoded, encoded[:28], key.Text()[32:60]} {
				if strings.Contains(out+errOut, secret) {
					t.Fatalf("output shows the key: %q %q", out, errOut)
				}
			}
		})
	}
}

func TestCheck(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		wantCode int
		want     string // in stdout for exit 0, in stderr otherwise
	}{
		{name: "an alpha tag is a prerelease", args: []string{"-version", "v0.1.0-alpha.1"}, want: "published as a prerelease"},
		{name: "a plain tag is a release", args: []string{"-version", "v1.0.0"}, want: "published as a release"},
		{name: "the minimum may equal the version", args: []string{"-version", "v1.0.0", "-min-version", "v1.0.0"}, want: "v1.0.0"},
		{name: "a snapshot may support only itself", args: []string{"-version", "v0.0.0-snapshot.7", "-min-version", "v0.0.0-snapshot.7"}, want: "prerelease"},
		{name: "a minimum above the version fails", args: []string{"-version", "v1.0.0", "-min-version", "v1.0.1"}, wantCode: 1, want: "newer than"},
		{name: "a tag without v fails", args: []string{"-version", "1.0.0"}, wantCode: 1, want: "doesn't start with v"},
		{name: "a tag with build metadata fails", args: []string{"-version", "v1.0.0+meta"}, wantCode: 1, want: "build metadata"},
		{name: "a bad minimum fails", args: []string{"-version", "v1.0.0", "-min-version", "x"}, wantCode: 1, want: "-min-version:"},
		{name: "-version is required", args: nil, wantCode: 2, want: "-version is required"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code, out, errOut := cli(t, append([]string{"check"}, tt.args...)...)
			got := out
			if code != 0 {
				got = errOut
			}
			if code != tt.wantCode || !strings.Contains(got, tt.want) {
				t.Fatalf("exit %d, stdout %q, stderr %q; want exit %d and %q", code, out, errOut, tt.wantCode, tt.want)
			}
		})
	}
}

// with returns args with flag's value replaced, or the flag removed when
// value is "<none>".
func with(args []string, flag, value string) []string {
	out := slices.Clone(args)
	i := slices.Index(out, flag)
	if value == "<none>" {
		return slices.Delete(out, i, i+2)
	}
	out[i+1] = value
	return out
}

func TestDistRefuses(t *testing.T) {
	f := newFixture(t)
	exists := t.TempDir()
	tests := []struct {
		name     string
		flag     string
		value    string
		wantCode int
		wantErr  string
	}{
		{name: "a target with no binary", flag: "-targets", value: testTargets + " linux/arm64", wantCode: 1, wantErr: "linux/arm64:"},
		{name: "a target listed twice", flag: "-targets", value: testTargets + " linux/amd64", wantCode: 1, wantErr: "listed twice"},
		{name: "a target that isn't os/arch", flag: "-targets", value: "linux", wantCode: 1, wantErr: "isn't os/arch"},
		{name: "an out directory that already exists", flag: "-out", value: exists, wantCode: 1, wantErr: "exists"},
		{name: "a version without v", flag: "-version", value: "0.1.0", wantCode: 1, wantErr: "version:"},
		{name: "a minimum above the version", flag: "-min-version", value: "v0.2.0", wantCode: 1, wantErr: "newer than version"},
		{name: "an http base URL", flag: "-base-url", value: "http://github.com/some-du6e/pairtime", wantCode: 1, wantErr: "isn't https"},
		{name: "a short commit", flag: "-commit", value: "68726d8", wantCode: 1, wantErr: "commit"},
		{name: "a date with an offset", flag: "-date", value: "2026-11-19T11:49:00+01:00", wantCode: 1, wantErr: "date"},
		{name: "an extra file that doesn't exist", flag: "-extra", value: filepath.Join(f.dir, "NOTICE"), wantCode: 1, wantErr: "NOTICE"},
		{name: "a required flag left out", flag: "-payload", value: "<none>", wantCode: 2, wantErr: "-payload is required"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := filepath.Join(t.TempDir(), "dist")
			args := with(f.distArgs(out, filepath.Join(t.TempDir(), "payload.json")), tt.flag, tt.value)
			code, _, errOut := cli(t, args...)
			if code != tt.wantCode || !strings.Contains(errOut, tt.wantErr) {
				t.Fatalf("exit %d, stderr %q; want exit %d and %q", code, errOut, tt.wantCode, tt.wantErr)
			}
		})
	}
}

func TestUsage(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		wantCode int
		want     string // in stdout+stderr
	}{
		{name: "no command prints usage", args: nil, wantCode: 2, want: "Usage:"},
		{name: "help prints usage", args: []string{"help"}, wantCode: 0, want: "release keygen"},
		{name: "an unknown command fails", args: []string{"publish"}, wantCode: 2, want: `unknown command "publish"`},
		{name: "a stray argument fails", args: []string{"keys", "extra"}, wantCode: 2, want: `unexpected argument "extra"`},
		{name: "an unknown flag fails", args: []string{"verify", "-nope"}, wantCode: 2, want: "flag provided but not defined"},
		{name: "-h lists a command's flags", args: []string{"dist", "-h"}, wantCode: 0, want: "-base-url"},
		{name: "sign needs a key source", args: []string{"sign", "-payload", "p", "-out", "o"}, wantCode: 2, want: "exactly one of -key-env and -throwaway"},
		{name: "sign takes one key source", args: []string{"sign", "-payload", "p", "-out", "o", "-throwaway", "-key-env", "K"}, wantCode: 2, want: "exactly one"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code, out, errOut := cli(t, tt.args...)
			if code != tt.wantCode || !strings.Contains(out+errOut, tt.want) {
				t.Fatalf("exit %d, output %q; want exit %d and %q", code, out+errOut, tt.wantCode, tt.want)
			}
		})
	}
}

func TestKeysAndErrors(t *testing.T) {
	keys, err := release.TrustedKeys()
	if err != nil {
		t.Fatal(err)
	}
	var want strings.Builder
	for _, k := range keys {
		want.WriteString(k.ID() + "\n")
	}
	tests := []struct {
		name          string
		githubActions string
		args          []string
		wantCode      int
		wantOut       string
		wantErrPrefix string
	}{
		{name: "keys prints the compiled-in key ids", args: []string{"keys"}, wantOut: want.String()},
		{name: "in GitHub Actions an error is an annotation", githubActions: "true", args: []string{"check", "-version", "x"}, wantCode: 1, wantErrPrefix: "::error::release check: "},
		{name: "elsewhere an error is plain text", githubActions: "", args: []string{"check", "-version", "x"}, wantCode: 1, wantErrPrefix: "release check: "},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("GITHUB_ACTIONS", tt.githubActions)
			code, out, errOut := cli(t, tt.args...)
			if code != tt.wantCode || out != tt.wantOut || !strings.HasPrefix(errOut, tt.wantErrPrefix) {
				t.Fatalf("exit %d, stdout %q, stderr %q", code, out, errOut)
			}
		})
	}
}
