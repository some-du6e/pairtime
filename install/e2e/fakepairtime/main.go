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
//	          file $PAIRTIME_FAKE_RECORD names, then exits with
//	          $PAIRTIME_FAKE_INSTALL_EXIT, or 0. With
//	          PAIRTIME_FAKE_INSTALL_INTERRUPT=1 it first sends its parent,
//	          the script, an interrupt and then exits with status 130, as
//	          Ctrl-C at the consent screen does to both (not on Windows)
//	wait      sleeps two minutes, standing in for a running agent
package main

import (
	"fmt"
	"io"
	"os"
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
		code, err := strconv.Atoi(os.Getenv("PAIRTIME_FAKE_INSTALL_EXIT"))
		if err != nil {
			code = 0
		}
		return code
	case "wait":
		time.Sleep(2 * time.Minute)
		return 0
	}
	fmt.Fprintf(stderr, "pairtime: unknown command %q\n", args[0])
	return 2
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
