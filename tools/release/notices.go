// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bytes"
	"cmp"
	"debug/buildinfo"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime/debug"
	"slices"
	"strings"
)

// noticesName is the third-party notices file: beside the binary in each
// target's -bin directory, and at the root of each archive.
const noticesName = "THIRD_PARTY_NOTICES.txt"

// cmdNotices writes THIRD_PARTY_NOTICES.txt for one built binary: the
// license and notice files of Go and of every module the binary links, and
// of the embedded material extra-notices.txt lists. The licenses of Go and
// of pairtime's modules ask for their notices to come with a binary
// (docs/spec/update-manifest.md). It runs the go command, so it runs from
// the repository root with the Go that built the binary.
func cmdNotices(args []string, stdout, stderr io.Writer) error {
	flags := newFlags("notices", stderr)
	binPath := flags.String("bin", "", "the built binary to write the notices for")
	outPath := flags.String("out", "", "the file to write, such as "+noticesName+" beside the binary")
	extraPath := flags.String("extra", "tools/release/extra-notices.txt", "the list of notices for embedded material that isn't a Go module")
	if err := parse(flags, args, "bin", "out"); err != nil {
		return err
	}
	info, err := buildinfo.ReadFile(*binPath)
	if err != nil {
		return fmt.Errorf("%s: reading its build info: %w", *binPath, err)
	}
	extras, err := readExtraNotices(*extraPath, ".")
	if err != nil {
		return err
	}
	env, err := readGoEnv()
	if err != nil {
		return err
	}
	pkgs, err := listPackages(info)
	if err != nil {
		return fmt.Errorf("%s: %w", *binPath, err)
	}
	n, err := buildNotices(info, env, pkgs, extras)
	if err != nil {
		return fmt.Errorf("%s: %w", *binPath, err)
	}
	if err := os.WriteFile(*outPath, n.render(), 0o644); err != nil {
		return err
	}
	files := 0
	for _, p := range n.parts {
		files += len(p.files)
	}
	fmt.Fprintf(stdout, "%s: %d files for %s on %s, from Go, %d modules and %d extras\n",
		*outPath, files, n.binary, n.platform, n.modules, len(n.parts)-n.modules-1)
	return nil
}

// notices is what one THIRD_PARTY_NOTICES.txt holds.
type notices struct {
	module    string // the main module, whose own license is LICENSE
	binary    string // the main package
	platform  string // GOOS/GOARCH
	goVersion string
	parts     []noticePart // Go, then the modules by path, then the extras
	modules   int
}

// noticePart is one piece of software and the notice files it comes with.
type noticePart struct {
	name  string
	files []noticeFile
}

type noticeFile struct {
	path string // within its module, GOROOT or the repository, with slashes
	data []byte
}

// rule frames each file's header in the notices.
const rule = "================================================================================\n"

// render writes the notices: a header and a list of the parts, then every
// file word for word under a header naming its part and path. The same
// inputs give the same bytes, and nothing names a directory on the
// machine that built it.
func (n *notices) render() []byte {
	var b bytes.Buffer
	fmt.Fprintf(&b, "Third-party notices\n\n"+
		"Besides the code of %s, whose license is in LICENSE,\n"+
		"this binary contains the software listed below. After the list come the\n"+
		"license and notice files that software comes with, word for word.\n\n", n.module)
	fmt.Fprintf(&b, "Binary:   %s\nPlatform: %s\nGo:       %s\n\nSoftware:\n", n.binary, n.platform, n.goVersion)
	for _, p := range n.parts {
		fmt.Fprintf(&b, "  %s\n", p.name)
	}
	for _, p := range n.parts {
		for _, f := range p.files {
			fmt.Fprintf(&b, "\n%s%s: %s\n%s\n", rule, p.name, f.path, rule)
			b.Write(f.data)
			if !bytes.HasSuffix(f.data, []byte("\n")) {
				b.WriteByte('\n')
			}
		}
	}
	return b.Bytes()
}

// goEnv is what `go env` reports about the Go toolchain here.
type goEnv struct {
	GOROOT    string
	GOVERSION string
}

func readGoEnv() (goEnv, error) {
	out, err := goCommand(nil, "env", "-json", "GOROOT", "GOVERSION")
	if err != nil {
		return goEnv{}, err
	}
	var env goEnv
	if err := json.Unmarshal(out, &env); err != nil {
		return goEnv{}, fmt.Errorf("reading the output of go env: %w", err)
	}
	if env.GOROOT == "" || env.GOVERSION == "" {
		return goEnv{}, errors.New("go env reported no GOROOT or GOVERSION")
	}
	return env, nil
}

// goCommand runs the go command with env added to this process's
// environment, and returns its standard output.
func goCommand(env []string, args ...string) ([]byte, error) {
	cmd := exec.Command("go", args...)
	cmd.Env = append(os.Environ(), env...)
	var out, errOut bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errOut
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("go %s: %v\n%s", strings.Join(args, " "), err, errOut.String())
	}
	return out.Bytes(), nil
}

// listedPackage and listedModule hold the fields of `go list -json` output
// used here.
type listedPackage struct {
	ImportPath string
	Dir        string
	Standard   bool
	Module     *listedModule
}

type listedModule struct {
	Path    string
	Version string
	Dir     string
	Main    bool
	Replace *listedModule
}

// key names a module and its replacement the way moduleKey does for a
// module in a binary's build info, so the two lists can be compared.
func (m *listedModule) key() string {
	k := m.Path + "@" + m.Version
	if r := m.Replace; r != nil {
		k += " => " + r.Path
		if r.Version != "" {
			k += "@" + r.Version
		}
	}
	return k
}

// title names a module in the notices, as go.mod would.
func (m *listedModule) title() string {
	s := m.Path + " " + m.Version
	if r := m.Replace; r != nil {
		s += " => " + r.Path
		if r.Version != "" {
			s += " " + r.Version
		}
	}
	return s
}

// root is the directory a module's files are in: its replacement's, when
// it has one.
func (m *listedModule) root() string {
	if m.Replace != nil {
		return m.Replace.Dir
	}
	return m.Dir
}

// moduleKey names a module from a binary's build info like
// listedModule.key. Build info gives a directory replacement the version
// "(devel)", where go list gives it none.
func moduleKey(m *debug.Module) string {
	k := m.Path + "@" + m.Version
	if r := m.Replace; r != nil {
		k += " => " + r.Path
		if r.Version != "" && r.Version != "(devel)" {
			k += "@" + r.Version
		}
	}
	return k
}

// listPackages asks go list for every package in the binary's build, with
// the GOOS, GOARCH, cgo setting and tags its build info records.
func listPackages(info *debug.BuildInfo) ([]listedPackage, error) {
	if info.Path == "" || strings.HasPrefix(info.Path, "-") {
		return nil, fmt.Errorf("its build info names the main package %q", info.Path)
	}
	env, tags := buildEnv(info)
	args := []string{"list", "-deps", "-json=ImportPath,Dir,Standard,Module"}
	if tags != "" {
		args = append(args, "-tags", tags)
	}
	out, err := goCommand(env, append(args, info.Path)...)
	if err != nil {
		return nil, err
	}
	var pkgs []listedPackage
	dec := json.NewDecoder(bytes.NewReader(out))
	for {
		var p listedPackage
		err := dec.Decode(&p)
		if errors.Is(err, io.EOF) {
			return pkgs, nil
		}
		if err != nil {
			return nil, fmt.Errorf("reading the output of go list: %w", err)
		}
		pkgs = append(pkgs, p)
	}
}

// buildEnv returns the environment and the -tags value that make go list
// see the build a binary's build info describes. GOFLAGS is cleared and
// go.work ignored, so the recorded settings alone decide.
func buildEnv(info *debug.BuildInfo) (env []string, tags string) {
	env = []string{"GOWORK=off", "GOFLAGS="}
	for _, s := range info.Settings {
		switch {
		case s.Key == "-tags":
			tags = s.Value
		case s.Key == "CGO_ENABLED", strings.HasPrefix(s.Key, "GO"):
			env = append(env, s.Key+"="+s.Value)
		}
	}
	return env, tags
}

// setting returns the value a binary's build info records for key.
func setting(info *debug.BuildInfo, key string) string {
	for _, s := range info.Settings {
		if s.Key == key {
			return s.Value
		}
	}
	return ""
}

// buildNotices gathers the notices of the binary info describes: Go's
// LICENSE and PATENTS, the license and notice files of every module go
// list says it links, and the extras for the packages of the main module
// it links. It fails unless go list and the build info agree on the
// modules, and unless every module has a license file at its root.
func buildNotices(info *debug.BuildInfo, env goEnv, pkgs []listedPackage, extras []extraNotice) (*notices, error) {
	goVersion, _, _ := strings.Cut(info.GoVersion, " ")
	if goVersion == "" {
		return nil, errors.New("its build info names no Go version")
	}
	if goVersion != env.GOVERSION {
		return nil, fmt.Errorf("it was built with %s, but the go command here is %s, whose LICENSE may not be the one that applies", goVersion, env.GOVERSION)
	}
	n := &notices{
		module:    info.Main.Path,
		binary:    info.Path,
		platform:  setting(info, "GOOS") + "/" + setting(info, "GOARCH"),
		goVersion: goVersion,
	}

	goPart := noticePart{name: "Go " + goVersion + ", the runtime and standard library"}
	for _, name := range []string{"LICENSE", "PATENTS"} {
		data, err := os.ReadFile(filepath.Join(env.GOROOT, name))
		if name == "PATENTS" && errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("reading Go's %s: %w", name, err)
		}
		goPart.files = append(goPart.files, noticeFile{path: name, data: data})
	}
	n.parts = append(n.parts, goPart)

	mods := map[string]*moduleFiles{}
	linkedMain := map[string]bool{} // import paths of the main module's packages
	for _, p := range pkgs {
		switch {
		case p.Standard:
			continue
		case p.Module == nil:
			return nil, fmt.Errorf("go list gave the package %s no module", p.ImportPath)
		case p.Module.Main:
			if p.Module.Path != info.Main.Path {
				return nil, fmt.Errorf("go list's main module is %s, but the binary's is %s", p.Module.Path, info.Main.Path)
			}
			linkedMain[p.ImportPath] = true
			continue
		}
		key := p.Module.key()
		mf := mods[key]
		if mf == nil {
			if p.Module.root() == "" {
				return nil, fmt.Errorf("go list gave the module %s no directory, so its license can't be read; go mod download fetches it", p.Module.title())
			}
			mf = &moduleFiles{mod: p.Module, files: map[string][]byte{}, read: map[string]bool{}}
			mods[key] = mf
		}
		if err := mf.addPackage(p); err != nil {
			return nil, err
		}
	}
	if !linkedMain[info.Path] {
		return nil, fmt.Errorf("go list didn't list the binary's main package %s", info.Path)
	}

	listed := slices.Sorted(maps.Keys(mods))
	var linked []string
	for _, d := range info.Deps {
		linked = append(linked, moduleKey(d))
	}
	slices.Sort(linked)
	if !slices.Equal(listed, linked) {
		return nil, fmt.Errorf("go list and the binary's build info name different modules (only go list: %s; only the build info: %s); build the binary from this checkout, and run notices with the Go that built it",
			orNone(missing(listed, linked)), orNone(missing(linked, listed)))
	}
	for _, key := range listed {
		mf := mods[key]
		if !mf.hasRootLicense() {
			return nil, fmt.Errorf("the module %s has no license file at its root", mf.mod.title())
		}
		part := noticePart{name: mf.mod.title()}
		for _, p := range sortedPaths(mf.files) {
			part.files = append(part.files, noticeFile{path: p, data: mf.files[p]})
		}
		n.parts = append(n.parts, part)
		n.modules++
	}

	for _, e := range extras {
		pkg := info.Main.Path
		if e.pkg != "." {
			pkg += "/" + e.pkg
		}
		if linkedMain[pkg] {
			n.parts = append(n.parts, noticePart{name: e.title, files: []noticeFile{{path: e.file, data: e.data}}})
		}
	}
	return n, nil
}

// missing returns the strings of a that b lacks.
func missing(a, b []string) []string {
	var out []string
	for _, s := range a {
		if !slices.Contains(b, s) {
			out = append(out, s)
		}
	}
	return out
}

func orNone(s []string) string {
	if len(s) == 0 {
		return "none"
	}
	return strings.Join(s, ", ")
}

// moduleFiles gathers the notice files of one module the binary links.
type moduleFiles struct {
	mod   *listedModule
	files map[string][]byte // by path within the module, with slashes
	read  map[string]bool   // the directories already read
}

// addPackage reads the notice files in a linked package's directory and in
// each directory above it, up to the module root: the directories
// tools/ci/licensecheck reads. A license there covers the package, such as
// one beside code a module vendors.
func (mf *moduleFiles) addPackage(p listedPackage) error {
	root := mf.mod.root()
	if p.Dir == "" {
		return fmt.Errorf("go list gave the package %s no directory", p.ImportPath)
	}
	for d := p.Dir; ; d = filepath.Dir(d) {
		rel, err := filepath.Rel(root, d)
		if err != nil || !filepath.IsLocal(rel) {
			return fmt.Errorf("the package %s is in %s, outside its module %s in %s", p.ImportPath, p.Dir, mf.mod.title(), root)
		}
		if !mf.read[d] {
			mf.read[d] = true
			if err := mf.readDir(root, d); err != nil {
				return err
			}
		}
		if rel == "." {
			return nil
		}
	}
}

// readDir reads the notice files directly in dir.
func (mf *moduleFiles) readDir(root, dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.IsDir() || !isNoticeFile(e.Name()) {
			continue
		}
		p := filepath.Join(dir, e.Name())
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if !e.Type().IsRegular() {
			// Module zips leave links out, so what a link shows here isn't
			// what a download of the module gets.
			return fmt.Errorf("%s in the module %s is a symbolic link or special file; notice files must be regular files", rel, mf.mod.title())
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		mf.files[rel] = data
	}
	return nil
}

// hasRootLicense reports whether a license file, and not only a NOTICE or
// PATENTS, sits at the module root.
func (mf *moduleFiles) hasRootLicense() bool {
	for p := range mf.files {
		if !strings.Contains(p, "/") && isLicenseFile(p) {
			return true
		}
	}
	return false
}

// sortedPaths orders a module's notice files: those at its root first, then
// the deeper ones, each by path.
func sortedPaths(files map[string][]byte) []string {
	paths := slices.Collect(maps.Keys(files))
	slices.SortFunc(paths, func(a, b string) int {
		if c := cmp.Compare(strings.Count(a, "/"), strings.Count(b, "/")); c != 0 {
			return c
		}
		return strings.Compare(a, b)
	})
	return paths
}

// licenseStems and noticeStems begin the names, compared without case, of
// license files and of the other notice files. The license names cover the
// ones pkg.go.dev and tools/ci/licensecheck read. NOTICE is Apache-2.0's
// (its section 4(d)), and PATENTS is the grant beside Go's license.
var (
	licenseStems = []string{"license", "licence", "copying", "unlicense", "unlicence", "mit-license", "mit-licence", "mit_license", "mit_licence"}
	noticeStems  = []string{"notice", "patents"}
)

// codeExts are the extensions of the files the go command compiles, so
// that a file such as lib/pq's notice.go counts as code, not a notice.
var codeExts = []string{".go", ".s", ".sx", ".c", ".cc", ".cpp", ".cxx", ".h", ".hh", ".hpp", ".hxx", ".m", ".f", ".for", ".f90", ".swig", ".swigcxx", ".syso"}

// isLicenseFile reports whether name is a license file's: a license stem,
// alone or followed by a part that starts with ".", "-" or "_", as in
// LICENSE.md, LICENSE-APACHE or COPYING_LGPL.
func isLicenseFile(name string) bool { return hasStem(name, licenseStems) }

// isNoticeFile reports whether name is a license file's or another notice
// file's, such as NOTICE.txt.
func isNoticeFile(name string) bool {
	return hasStem(name, licenseStems) || hasStem(name, noticeStems)
}

func hasStem(name string, stems []string) bool {
	lower := strings.ToLower(name)
	if slices.Contains(codeExts, path.Ext(lower)) {
		return false
	}
	for _, stem := range stems {
		if rest, ok := strings.CutPrefix(lower, stem); ok && (rest == "" || strings.ContainsAny(rest[:1], ".-_")) {
			return true
		}
	}
	return false
}

// extraNotice is one line of extra-notices.txt: the notice file of
// material one of the main module's packages embeds or was ported from,
// which isn't a Go module, so no build info points at it.
type extraNotice struct {
	pkg   string // the package's directory, from the repository root, with slashes
	file  string // the notice file, the same way
	title string // what the material is, as the notices name it
	data  []byte
}

// readExtraNotices reads a list of extra notices whose paths are relative
// to root. It checks every line, including those for packages no binary
// links, so a typo fails at once instead of quietly dropping a notice. A
// package directory must hold a Go file that a build compiles: binaries
// link packages, so a notice keyed by any other directory, such as
// pkg/redact/rules, which holds the rules pkg/redact embeds, would never
// ship.
func readExtraNotices(listPath, root string) ([]extraNotice, error) {
	data, err := os.ReadFile(listPath)
	if err != nil {
		return nil, err
	}
	var extras []extraNotice
	seen := map[string]bool{}
	for i, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 3 {
			return nil, fmt.Errorf("%s line %d: want a package directory, a notice file and a title", listPath, i+1)
		}
		e := extraNotice{pkg: fields[0], file: fields[1], title: strings.Join(fields[2:], " ")}
		for _, p := range []string{e.pkg, e.file} {
			if strings.Contains(p, `\`) || path.Clean(p) != p || !filepath.IsLocal(filepath.FromSlash(p)) {
				return nil, fmt.Errorf("%s line %d: %q isn't a clean path inside the repository, with slashes", listPath, i+1, p)
			}
		}
		if seen[e.pkg+" "+e.file] {
			return nil, fmt.Errorf("%s line %d: %s for %s is listed twice", listPath, i+1, e.file, e.pkg)
		}
		seen[e.pkg+" "+e.file] = true
		dir := filepath.Join(root, filepath.FromSlash(e.pkg))
		if st, err := os.Stat(dir); err != nil || !st.IsDir() {
			return nil, fmt.Errorf("%s line %d: the package directory %s doesn't exist", listPath, i+1, e.pkg)
		}
		ok, err := holdsPackage(dir)
		if err != nil {
			return nil, fmt.Errorf("%s line %d: %w", listPath, i+1, err)
		}
		if !ok {
			return nil, fmt.Errorf("%s line %d: %s holds no Go file that a build compiles, so no binary links it and its notice would never ship; name the directory of the package that embeds the material", listPath, i+1, e.pkg)
		}
		if e.data, err = os.ReadFile(filepath.Join(root, filepath.FromSlash(e.file))); err != nil {
			return nil, fmt.Errorf("%s line %d: %w", listPath, i+1, err)
		}
		extras = append(extras, e)
	}
	return extras, nil
}

// holdsPackage reports whether dir holds a .go file that a build compiles:
// one that isn't a test, and whose name doesn't start with "_" or ".",
// which the go command ignores. Build constraints aren't read.
func holdsPackage(dir string) (bool, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false, err
	}
	for _, e := range entries {
		name := e.Name()
		if !e.IsDir() && strings.HasSuffix(name, ".go") && !strings.HasSuffix(name, "_test.go") &&
			!strings.HasPrefix(name, "_") && !strings.HasPrefix(name, ".") {
			return true, nil
		}
	}
	return false, nil
}
