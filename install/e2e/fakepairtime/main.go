// SPDX-License-Identifier: AGPL-3.0-or-later

// Command fakepairtime stands in for pairtime in the install scripts' end
// to end tests (install/e2e). It has the install command that the real
// binary doesn't have yet (WP-IN2), so the tests can check what the scripts
// hand over to it, and a build whose version fails, which the scripts must
// never put in place (DESIGN 3.12). install/e2e/fixture.sh builds it with
// -ldflags "-X main.version=<the fixture's version>", and with
// "-X main.broken=yes" for the failing build.
//
// It answers:
//
//	version   pairtime's first line, or status 3 when broken
//	help      a command list that includes install
//	install   writes its arguments and what its standard input is to the
//	          file $PAIRTIME_FAKE_RECORD names. Then, as pairtime install
//	          does after the teen's yes, it copies itself to the per-user
//	          path, ~/.local/bin/pairtime or
//	          %LOCALAPPDATA%\pairtime\bin\pairtime.exe, unless it runs
//	          from there, and exits 0, or $PAIRTIME_FAKE_INSTALL_AFTER for a
//	          failure later in the install. A $PAIRTIME_FAKE_INSTALL_EXIT
//	          other than 0 stops it before it copies anything, as a no (3)
//	          or a refusal does. With PAIRTIME_FAKE_INSTALL_INTERRUPT=1 it
//	          sends its parent, the script, an interrupt and exits with
//	          status 130 without copying, as Ctrl-C at the consent screen
//	          does to both (not on Windows)
//	wait      sleeps two minutes, standing in for a running agent
package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

var (
	version = "dev"
	broken  = ""
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

func run(args []string, stdin *os.File, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "fakepairtime: no command")
		return 2
	}
	switch args[0] {
	case "version":
		if broken == "yes" {
			fmt.Fprintln(stderr, "fakepairtime: this build fails on purpose")
			return 3
		}
		fmt.Fprintf(stdout, "pairtime %s (a fake build for install/e2e)\nrelease keys: none\n", version)
		return 0
	case "help":
		fmt.Fprint(stdout, "Usage:\n  pairtime <command>\n\nCommands:\n  install  Set up pairtime (fake)\n  wait     Stand in for a running agent\n  version  Print the version\n")
		return 0
	case "install":
		if p := os.Getenv("PAIRTIME_FAKE_RECORD"); p != "" {
			record := strings.Join(args, " ") + "\nstdin: " + describe(stdin) + "\n"
			if err := os.WriteFile(p, []byte(record), 0o644); err != nil {
				fmt.Fprintln(stderr, "fakepairtime:", err)
				return 1
			}
		}
		if os.Getenv("PAIRTIME_FAKE_INSTALL_INTERRUPT") == "1" {
			parent, err := os.FindProcess(os.Getppid())
			if err == nil {
				err = parent.Signal(os.Interrupt)
			}
			if err != nil {
				fmt.Fprintln(stderr, "fakepairtime: interrupting the script:", err)
				return 1
			}
			return 130
		}
		if code := status("PAIRTIME_FAKE_INSTALL_EXIT"); code != 0 {
			return code
		}
		if err := place(); err != nil {
			fmt.Fprintln(stderr, "fakepairtime:", err)
			return 1
		}
		return status("PAIRTIME_FAKE_INSTALL_AFTER")
	case "wait":
		time.Sleep(2 * time.Minute)
		return 0
	}
	fmt.Fprintf(stderr, "pairtime: unknown command %q\n", args[0])
	return 2
}

// status returns the exit status the environment variable name holds, or
// 0.
func status(name string) int {
	code, err := strconv.Atoi(os.Getenv(name))
	if err != nil {
		return 0
	}
	return code
}

// place copies this binary to the per-user path, as pairtime install does
// after the teen's yes, unless it runs from there already.
func place() error {
	self, err := os.Executable()
	if err != nil {
		return err
	}
	target := filepath.Join(os.Getenv("HOME"), ".local", "bin", "pairtime")
	if runtime.GOOS == "windows" {
		target = filepath.Join(os.Getenv("LOCALAPPDATA"), "pairtime", "bin", "pairtime.exe")
	}
	if a, err := os.Stat(self); err == nil {
		if b, err := os.Stat(target); err == nil && os.SameFile(a, b) {
			return nil
		}
	}
	data, err := os.ReadFile(self)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	tmp := target + ".fake-new"
	if err := os.WriteFile(tmp, data, 0o755); err != nil {
		return err
	}
	return os.Rename(tmp, target)
}

// describe says what f, standard input, is: the null device, a terminal,
// or something else, such as a pipe.
func describe(f *os.File) string {
	info, err := f.Stat()
	if err != nil {
		return "unreadable"
	}
	if null, err := os.Stat(os.DevNull); err == nil && os.SameFile(info, null) {
		return "null"
	}
	if info.Mode()&os.ModeCharDevice != 0 {
		return "terminal"
	}
	return "other"
}
