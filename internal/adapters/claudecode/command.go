// SPDX-License-Identifier: AGPL-3.0-or-later

package claudecode

import (
	"path"
	"strings"

	"github.com/some-du6e/pairtime/pkg/event"
)

// commandClass sorts a Bash call by what its command runs (SM 1.2's
// tool_class): test when any of its simple commands runs tests, else build
// when one builds, else bash. The rule reads words only and never runs
// anything:
//
//   - The command splits into simple commands at newlines and at the
//     operators ; & && | ||, without regard to quotes, so a quoted
//     operator splits too. That errs towards bash.
//   - In each, leading variable assignments (NAME=value) and the wrappers
//     sudo, env, time, nice, nohup, command and exec are skipped, each with
//     the flags that follow it, and timeout with its duration too.
//   - The program is the next word, as its base name, with a trailing .exe
//     dropped. python, python3 and py with -m run the module that follows;
//     their other flags are skipped.
//   - testPrograms, testModules and testVerbs say what runs tests,
//     buildPrograms and buildVerbs what builds.
func commandClass(cmd string) event.ToolClass {
	class := event.ToolBash
	for _, simple := range simpleCommands(cmd) {
		switch classOf(strings.Fields(simple)) {
		case event.ToolTest:
			return event.ToolTest
		case event.ToolBuild:
			class = event.ToolBuild
		}
	}
	return class
}

// testPrograms run tests whatever their arguments.
var testPrograms = []string{
	"pytest", "py.test", "tox", "nox", "jest", "vitest", "mocha", "ava", "rspec", "phpunit", "ctest",
}

// testModules run tests under python -m.
var testModules = []string{"pytest", "unittest", "nose2", "tox"}

// testVerbs are the first argument that makes a program run tests.
var testVerbs = map[string][]string{
	"go":      {"test"},
	"cargo":   {"test", "nextest"},
	"npm":     {"test", "t"},
	"yarn":    {"test"},
	"pnpm":    {"test"},
	"bun":     {"test"},
	"deno":    {"test"},
	"dotnet":  {"test"},
	"mvn":     {"test", "verify"},
	"gradle":  {"test", "check"},
	"gradlew": {"test", "check"},
	"make":    {"test", "check"},
	"rake":    {"test", "spec"},
	"mix":     {"test"},
	"swift":   {"test"},
}

// buildPrograms build whatever their arguments.
var buildPrograms = []string{
	"tsc", "gcc", "g++", "clang", "clang++", "cc", "c++", "javac", "rustc", "ninja", "webpack", "esbuild", "msbuild",
}

// buildVerbs are the first argument that makes a program build. make with
// no target builds too.
var buildVerbs = map[string][]string{
	"go":      {"build", "install", "vet"},
	"cargo":   {"build", "check", "clippy"},
	"npm":     {"run-script build", "build"},
	"yarn":    {"build"},
	"pnpm":    {"build"},
	"bun":     {"build"},
	"dotnet":  {"build", "publish"},
	"mvn":     {"compile", "package", "install"},
	"gradle":  {"build", "assemble"},
	"gradlew": {"build", "assemble"},
	"make":    {"build", "all"},
	"cmake":   {"--build"},
	"swift":   {"build"},
	"zig":     {"build"},
	"bazel":   {"build"},
	"docker":  {"build"},
	"vite":    {"build"},
}

// simpleCommands splits a command line at newlines and the operators ; &
// && | ||.
func simpleCommands(cmd string) []string {
	return strings.FieldsFunc(cmd, func(r rune) bool {
		return r == '\n' || r == ';' || r == '&' || r == '|'
	})
}

// wrappers are skipped before the program, with their flags.
var wrappers = []string{"sudo", "env", "time", "nice", "nohup", "command", "exec"}

// classOf classes one simple command, split into words.
func classOf(words []string) event.ToolClass {
	i := 0
	for i < len(words) {
		w := words[i]
		switch {
		case isAssignment(w):
			i++
		case contains(wrappers, w):
			i++
			for i < len(words) && strings.HasPrefix(words[i], "-") {
				i++
			}
		case w == "timeout":
			i++
			for i < len(words) && strings.HasPrefix(words[i], "-") {
				i++
			}
			i++ // the duration
		default:
			return programClass(program(w), words[i+1:])
		}
	}
	return event.ToolBash
}

// isAssignment reports whether w is NAME=value.
func isAssignment(w string) bool {
	eq := strings.IndexByte(w, '=')
	if eq <= 0 {
		return false
	}
	for i := 0; i < eq; i++ {
		b := w[i]
		if !(b == '_' || 'a' <= b && b <= 'z' || 'A' <= b && b <= 'Z' || i > 0 && '0' <= b && b <= '9') {
			return false
		}
	}
	return true
}

// program is a command word's program: its base name, without .exe, and
// gradlew for ./gradlew.
func program(w string) string {
	w = path.Base(strings.ReplaceAll(w, `\`, "/"))
	w = strings.TrimSuffix(strings.TrimSuffix(w, ".exe"), ".bat")
	return w
}

// programClass classes program p run with args.
func programClass(p string, args []string) event.ToolClass {
	switch {
	case contains(testPrograms, p):
		return event.ToolTest
	case contains(buildPrograms, p):
		return event.ToolBuild
	case p == "python" || p == "python3" || p == "py":
		for i := 0; i < len(args); i++ {
			if args[i] == "-m" && i+1 < len(args) {
				if contains(testModules, args[i+1]) {
					return event.ToolTest
				}
				return event.ToolBash
			}
			if !strings.HasPrefix(args[i], "-") || args[i] == "-c" {
				return event.ToolBash
			}
		}
		return event.ToolBash
	}
	verb := ""
	if len(args) > 0 {
		verb = args[0]
		if p == "npm" && verb == "run" && len(args) > 1 {
			verb = "run-script " + args[1]
		}
	}
	switch {
	case contains(testVerbs[p], verb), p == "npm" && strings.HasPrefix(verb, "run-script test"):
		return event.ToolTest
	case contains(buildVerbs[p], verb), p == "make" && (verb == "" || strings.HasPrefix(verb, "-")):
		return event.ToolBuild
	}
	return event.ToolBash
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
