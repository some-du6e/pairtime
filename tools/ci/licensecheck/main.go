// SPDX-License-Identifier: AGPL-3.0-or-later

// Command licensecheck checks that every module a build or test of a Go
// module links is MIT, BSD-2-Clause, BSD-3-Clause, Apache-2.0 or ISC
// licensed (DESIGN 3.12; docs/PLAN.md, ground rule 4). ci.yml runs it on
// pairtime on every PR.
//
// It asks the go command for every package the module's packages and tests
// import, for each of the seven targets ci.yml cross-compiles, so a module
// imported only on Windows, only on arm64 or only by a test still counts,
// and so does a module swapped in with a replace directive. The linux
// targets are listed a second time with -tags desktop, the way
// platform.yml builds the *_desktop_test.go tests (AGENTS.md), so a module
// only a desktop test imports counts too. For each package from another
// module it reads the license files in the package's directory and every
// directory above it up to the module root, and the module root must have
// one. Every license file found must be one of the allowed licenses (see
// identify.go).
//
// Usage:
//
//	go run ./tools/ci/licensecheck [-C dir] [-targets list]
//
// The exit status is 1 when a module fails and 2 when the check can't run,
// for example when go.sum lacks an entry (run tidy.yml; AGENTS.md).
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// licenseFiles are the file names, compared without case, read as license
// files. They follow the names pkg.go.dev looks for.
var licenseFiles = map[string]bool{
	"copying": true, "copying.md": true, "copying.markdown": true, "copying.txt": true,
	"licence": true, "licence.md": true, "licence.markdown": true, "licence.txt": true,
	"license": true, "license.md": true, "license.markdown": true, "license.txt": true,
	"license-2.0.txt": true, "licence-2.0.txt": true,
	"license-apache": true, "licence-apache": true,
	"license-apache-2.0.txt": true, "licence-apache-2.0.txt": true,
	"license-mit": true, "licence-mit": true, "license.mit": true, "licence.mit": true,
	"license.code": true, "licence.code": true, "license.docs": true, "licence.docs": true,
	"license.rst": true, "licence.rst": true,
	"mit-license": true, "mit-licence": true, "mit-license.md": true, "mit-licence.md": true,
	"mit-license.markdown": true, "mit-licence.markdown": true,
	"mit-license.txt": true, "mit-licence.txt": true,
	"mit_license": true, "mit_licence": true,
	"unlicense": true, "unlicence": true,
}

// goPackage and goModule hold the fields of `go list -json` output used here.
type goPackage struct {
	ImportPath string
	Dir        string
	Standard   bool
	Module     *goModule
}

type goModule struct {
	Path    string
	Version string
	Dir     string
	Main    bool
	Replace *goModule
}

// String names a module the way go.mod does, with its replacement.
func (m *goModule) String() string {
	s := m.Path
	if m.Version != "" {
		s += "@" + m.Version
	}
	if r := m.Replace; r != nil {
		s += " => " + r.Path
		if r.Version != "" {
			s += "@" + r.Version
		}
	}
	return s
}

// root is where a module's files are: its replacement's directory when it
// has one.
func (m *goModule) root() string {
	if m.Replace != nil && m.Replace.Dir != "" {
		return m.Replace.Dir
	}
	return m.Dir
}

// verdict is what the check found for one module.
type verdict struct {
	module   string
	licenses []string // allowed licenses found, sorted
	problems []string
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("licensecheck", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dir := fs.String("C", ".", "check the module in this directory")
	targets := fs.String("targets", "windows/amd64,windows/386,windows/arm64,darwin/amd64,darwin/arm64,linux/amd64,linux/arm64",
		"comma-separated GOOS/GOARCH pairs to list packages for, the same seven ci.yml cross-compiles")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		fmt.Fprintln(stderr, "usage: licensecheck [-C dir] [-targets list]")
		return 2
	}
	var pkgs []goPackage
	var builds []string
	for _, target := range strings.Split(*targets, ",") {
		goos, goarch, ok := strings.Cut(strings.TrimSpace(target), "/")
		if !ok || goos == "" || goarch == "" {
			fmt.Fprintf(stderr, "licensecheck: target %q isn't GOOS/GOARCH\n", target)
			return 2
		}
		for _, tags := range tagSets(goos) {
			p, err := listPackages(*dir, goos, goarch, tags)
			if err != nil {
				fmt.Fprintf(stderr, "licensecheck: %v\n", err)
				return 2
			}
			pkgs = append(pkgs, p...)
			builds = append(builds, build(goos, goarch, tags))
		}
	}
	verdicts := check(pkgs)
	failed := 0
	for _, v := range verdicts {
		if len(v.problems) == 0 {
			fmt.Fprintf(stdout, "ok    %s: %s\n", v.module, strings.Join(v.licenses, ", "))
			continue
		}
		failed++
		for _, p := range v.problems {
			fmt.Fprintf(stdout, "::error::%s: %s\n", v.module, p)
		}
	}
	fmt.Fprintf(stdout, "licensecheck: %d dependency modules of %s for %s, %d failed\n", len(verdicts), *dir, strings.Join(builds, ", "), failed)
	if failed > 0 {
		fmt.Fprintf(stdout, "Dependencies must be %s (DESIGN 3.12). Replace a failing module, or, if its license really is one of these, extend tools/ci/licensecheck with a test case from that file.\n", orList(Allowed))
		return 1
	}
	return 0
}

// tagSets returns the build tags a target is listed with, "" being the
// default build. platform.yml's Linux job runs the *_desktop_test.go tests
// with -tags desktop, and those files are under //go:build linux && desktop
// (AGENTS.md), so linux targets are listed both ways. Listing the default
// build too keeps any file under //go:build !desktop in the check.
func tagSets(goos string) []string {
	if goos == "linux" {
		return []string{"", "desktop"}
	}
	return []string{""}
}

// build names one listing in messages: linux/amd64, or
// linux/amd64 -tags desktop.
func build(goos, goarch, tags string) string {
	if tags == "" {
		return goos + "/" + goarch
	}
	return goos + "/" + goarch + " -tags " + tags
}

// listPackages lists every package the module in dir builds or tests with
// for one target and set of build tags, with its module.
func listPackages(dir, goos, goarch, tags string) ([]goPackage, error) {
	args := []string{"list", "-deps", "-test", "-json=ImportPath,Dir,Standard,Module"}
	if tags != "" {
		args = append(args, "-tags="+tags)
	}
	cmd := exec.Command("go", append(args, "./...")...)
	cmd.Dir = dir
	// GOWORK=off: a stray go.work must not change the module graph.
	cmd.Env = append(os.Environ(), "GOOS="+goos, "GOARCH="+goarch, "CGO_ENABLED=0", "GOWORK=off")
	var out, errOut bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errOut
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("go list for %s in %s: %v\n%s", build(goos, goarch, tags), dir, err, errOut.String())
	}
	var pkgs []goPackage
	dec := json.NewDecoder(&out)
	for {
		var p goPackage
		err := dec.Decode(&p)
		if errors.Is(err, io.EOF) {
			return pkgs, nil
		}
		if err != nil {
			return nil, fmt.Errorf("reading go list output: %v", err)
		}
		pkgs = append(pkgs, p)
	}
}

// check finds the license files of every non-standard, non-main package's
// module and judges each one.
func check(pkgs []goPackage) []verdict {
	type state struct {
		root     string
		dirs     map[string]bool
		licenses map[string]bool
		problems []string
	}
	mods := make(map[string]*state)
	for _, p := range pkgs {
		if p.Standard || p.Module == nil || p.Module.Main {
			continue
		}
		key := p.Module.String()
		st := mods[key]
		if st == nil {
			st = &state{root: p.Module.root(), dirs: make(map[string]bool), licenses: make(map[string]bool)}
			mods[key] = st
			if st.root == "" {
				st.problems = append(st.problems, "the go command gave no directory for this module, so its license can't be read")
			} else if n := checkDir(st.root, st.root, st.licenses, &st.problems); n == 0 {
				st.problems = append(st.problems, "no license file at the module root, so its license is unknown")
			}
			st.dirs[st.root] = true
		}
		if st.root == "" || p.Dir == "" {
			continue
		}
		// From the package's directory up to the module root, both included.
		for d := p.Dir; !st.dirs[d]; d = filepath.Dir(d) {
			rel, err := filepath.Rel(st.root, d)
			if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				break
			}
			st.dirs[d] = true
			checkDir(st.root, d, st.licenses, &st.problems)
		}
	}
	verdicts := make([]verdict, 0, len(mods))
	for key, st := range mods {
		v := verdict{module: key, problems: st.problems}
		for id := range st.licenses {
			v.licenses = append(v.licenses, id)
		}
		sort.Strings(v.licenses)
		verdicts = append(verdicts, v)
	}
	sort.Slice(verdicts, func(i, j int) bool { return verdicts[i].module < verdicts[j].module })
	return verdicts
}

// checkDir judges the license files directly in dir, recording allowed
// licenses and problems, and returns how many license files it found.
func checkDir(root, dir string, licenses map[string]bool, problems *[]string) int {
	entries, err := os.ReadDir(dir)
	if err != nil {
		*problems = append(*problems, fmt.Sprintf("reading %s: %v", dir, err))
		return 0
	}
	found := 0
	for _, e := range entries {
		if !licenseFiles[strings.ToLower(e.Name())] || e.IsDir() {
			continue
		}
		found++
		name := filepath.Join(dir, e.Name())
		rel, err := filepath.Rel(root, name)
		if err != nil {
			rel = name
		}
		rel = filepath.ToSlash(rel)
		if !e.Type().IsRegular() {
			// A link could point anywhere, and module zips leave links out,
			// so what it shows here isn't what a download gets.
			*problems = append(*problems, fmt.Sprintf("%s is a symbolic link or special file; license files must be regular files", rel))
			continue
		}
		data, err := os.ReadFile(name)
		if err != nil {
			*problems = append(*problems, fmt.Sprintf("reading %s: %v", rel, err))
			continue
		}
		id, reason := identify(string(data))
		if id == "" {
			*problems = append(*problems, fmt.Sprintf("%s: %s", rel, reason))
			continue
		}
		licenses[id] = true
	}
	return found
}
