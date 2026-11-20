#!/bin/sh
# SPDX-License-Identifier: AGPL-3.0-or-later
#
# dump-hook.sh RUN_DIR LABEL [ARG...]
#
# A hook command for capturing harness fixtures (tools/capture/README.md).
# Each call claims the next number under RUN_DIR/hooks/ and writes what the
# harness handed the hook, one file each:
#
#   time     when the call arrived: unix_ns, utc and the clock used
#   argv     the arguments, one per line (RUN_DIR and LABEL first)
#   env      the names of the environment variables, sorted. Never the values
#   cwd      the working directory
#   parents  pid, controlling terminal and executable name of each parent
#            process, nearest first: "1234 pts/3 claude". The terminal is ?
#            (?? on macOS) when the process has none. Never command lines,
#            which can hold prompts
#   stdin    the bytes on stdin, unchanged
#   flags    the harness the hook runs under, the nearest parent with a
#            harness's executable name: name=claude, then print=,
#            stream_json= and bare=, each yes or no, for a print flag (-p,
#            --print, --prompt, codex exec), stream-json input or output,
#            and --bare among its arguments, or ? where it can't tell. The
#            arguments are read in memory and never written. Written last,
#            as flags.tmp renamed into place, so it is whole or missing
#   stderr   only if something in this script failed
#
# LABEL is the hook event the entry was registered for, such as
# UserPromptSubmit, because some harnesses don't name the event in the payload.
#
# It prints nothing, never blocks the harness and always exits 0: harnesses
# feed a hook's stdout to the model on some events, and a non-zero exit can
# block a prompt or a tool call. Nothing is sent anywhere.

# The arrival time comes first, before anything slow.
clock=gnu-date
now_ns=$(date +%s%N 2>/dev/null)
case $now_ns in
'' | *[!0-9]*)
	# BSD date (macOS) has no %N. perl ships with macOS and has microseconds.
	clock=perl
	now_ns=$(perl -MTime::HiRes=gettimeofday -e '($s, $u) = gettimeofday(); printf("%d%06d000\n", $s, $u)' 2>/dev/null)
	case $now_ns in
	'' | *[!0-9]*)
		clock=date-seconds
		now_ns=$(date +%s)000000000
		;;
	esac
	;;
esac
utc=$(date -u +%Y-%m-%dT%H:%M:%SZ)

exec >/dev/null 2>&1
trap 'exit 0' EXIT

if [ "$#" -lt 2 ] || [ -z "$1" ]; then
	exit 0
fi
hooks=$1/hooks
# Whether the folder is there afterwards is what counts, not what mkdir says.
mkdir -p "$hooks"
[ -d "$hooks" ] || exit 0

# entries DIR prints how many entries DIR has. Positional parameters are
# local to a function call, so set -- here leaves the script's alone.
entries() {
	set -- "$1"/*
	if [ -e "$1" ] || [ -L "$1" ]; then
		echo "$#"
	else
		echo 0
	fi
}

# Claim the next number by creating its time file, as dump-hook.ps1 does.
# Under set -C the shell itself opens the file with O_CREAT|O_EXCL, so of
# two hooks that fire at the same moment only one can make it. What mkdir
# says can't decide that: uutils mkdir before 0.11, Ubuntu 26.04's, can tell
# two processes racing for one folder that each of them made it (uutils
# issue 13970), and then both calls wrote into one directory. A number is
# taken only when its time file is really there: any other failure (a full
# disk, a read-only folder, one another account owns) ends the call at
# once, instead of trying every number up to 999999 while the harness waits.
n=$(entries "$hooks")
while :; do
	n=$((n + 1))
	if [ "$n" -gt 999999 ]; then
		exit 0
	fi
	call=$hooks/$(printf '%06d' "$n")
	mkdir "$call"
	if (set -C && printf 'unix_ns=%s\nutc=%s\nclock=%s\n' "$now_ns" "$utc" "$clock" >"$call/time"); then
		break
	elif [ ! -e "$call/time" ]; then
		exit 0
	fi
done
exec 2>>"$call/stderr"

for arg in "$@"; do
	printf '%s\n' "$arg"
done >"$call/argv"

# awk's ENVIRON holds the names apart from the values, so no value can be
# mistaken for a name, whatever it contains.
awk 'BEGIN { for (name in ENVIRON) print name }' | LC_ALL=C sort >"$call/env"

pwd >"$call/cwd"

# Walk up from our parent through one ps listing. tty is the controlling
# terminal, which tells a session someone types into from a headless one
# (SM 1.2 has_tty). comm is the executable name (a full path on macOS, so
# keep only its last part); unlike args it never includes the command line.
# It comes last because it can hold spaces.
ps -A -o pid= -o ppid= -o tty= -o comm= 2>/dev/null | awk -v start="$PPID" '
	{
		pid = $1
		ppid[pid] = $2
		tty[pid] = $3
		line = $0
		sub(/^[ \t]*[0-9]+[ \t]+[0-9]+[ \t]+[^ \t]+[ \t]*/, "", line)
		sub(/.*\//, "", line)
		name[pid] = line
	}
	END {
		p = start
		for (i = 0; i < 12 && p != "" && p != 0; i++) {
			if (!(p in name)) {
				print p " ? ?"
				break
			}
			print p " " tty[p] " " name[p]
			if (ppid[p] == p) {
				break
			}
			p = ppid[p]
		}
	}' >"$call/parents"

# After the quick files, because it can block until the harness closes the
# pipe. A terminal on stdin means a person ran this by hand: write an empty
# file and go.
if [ -t 0 ]; then
	: >"$call/stdin"
else
	cat >"$call/stdin"
fi

# flags comes after everything else, so a hook the harness cuts short while
# this reads a command line has written every other file (E-procs-cmdlines).
# The harness is the nearest parent with one of the executable names that
# internal/platform/appclass.json gives a harness. Its command line goes
# through a pipe into awk, which keeps three answers and drops the rest:
# no argument is ever written. The file is put together first, written as
# flags.tmp and renamed into place, so flags is whole or missing.
harnesses='claude codex cursor-agent copilot gemini devin opencode cline'
# The options codex takes a value with before its subcommand, as in
# codex -c key=value exec or codex -p work exec: the root options in
# codex-rs/cli/src/main.rs, codex-rs/tui/src/cli.rs and codex-rs/utils/cli
# at d64451b. A value joined to its option with = needs no skipping. -i,
# --image isn't here: it takes every argument up to the next option.
codex_values='-c --config --enable --disable --remote --remote-auth-token-env -a --ask-for-approval -m --model --local-provider -p --profile -s --sandbox -C --cd --add-dir'
found=$(awk -v harnesses="$harnesses" '
	BEGIN {
		n = split(harnesses, list, " ")
		for (i = 1; i <= n; i++) {
			known[list[i]] = 1
		}
	}
	{
		name = $0
		sub(/^[^ ]+ [^ ]+ /, "", name)
		key = tolower(name)
		sub(/\.exe$/, "", key)
		if (key in known) {
			print $1 " " key " " name
			exit
		}
	}' "$call/parents")
if [ -n "$found" ]; then
	# The pid, the name as appclass.json lists it, and the name as parents
	# shows it, which is the one written.
	pid=${found%% *}
	rest=${found#* }
	key=${rest%% *}
	name=${rest#* }
	# One argument per line. On Linux each NUL in /proc/PID/cmdline ends an
	# argument, and a newline inside one becomes a carriage return, so each
	# line is one whole argument. macOS has no /proc, and its ps joins the
	# arguments with spaces, so there they are split at whitespace.
	answers=$(
		if [ -r "/proc/$pid/cmdline" ]; then
			tr '\n\000' '\r\n' <"/proc/$pid/cmdline"
		else
			ps -o args= -p "$pid" | awk '{ for (i = 1; i <= NF; i++) print $i }'
		fi | awk -v name="$key" -v codex_values="$codex_values" '
			BEGIN {
				print_flag = "no"
				stream = "no"
				bare = "no"
				n = split(codex_values, list, " ")
				for (i = 1; i <= n; i++) {
					takes_value[list[i]] = 1
				}
			}
			NR == 1 {
				next # the executable
			}
			$0 == "--" {
				exit
			}
			{
				# codex has no print flag; its exec subcommand, or e, is the
				# first argument that is neither an option nor the value of
				# one. -i takes as many values as come before the next option,
				# as clap reads its num_args = 1.. (shared_options.rs).
				if (name == "codex") {
					if (!subcommand) {
						operand = (substr($0, 1, 1) != "-")
						if (value) {
							value = 0
						} else if (!operand) {
							images = ($0 == "-i" || $0 == "--image")
							value = (!images && ($0 in takes_value))
						} else if (!images) {
							subcommand = 1
							if ($0 == "exec" || $0 == "e") {
								print_flag = "yes"
							}
						}
					}
				} else if ($0 == "-p" || $0 == "--print" || $0 == "--prompt" || substr($0, 1, 9) == "--prompt=") {
					print_flag = "yes"
				}
				if ($0 == "--bare") {
					bare = "yes"
				}
				if ($0 == "--input-format=stream-json" || $0 == "--output-format=stream-json") {
					stream = "yes"
				}
				if ($0 == "stream-json" && (previous == "--input-format" || previous == "--output-format")) {
					stream = "yes"
				}
				previous = $0
			}
			END {
				if (NR == 0) {
					# Nothing could be read: the process is gone or hidden.
					print_flag = stream = bare = "?"
				}
				print "print=" print_flag
				print "stream_json=" stream
				print "bare=" bare
			}'
	)
	flags="name=$name
$answers"
else
	flags='name=?
print=?
stream_json=?
bare=?'
fi
printf '%s\n' "$flags" >"$call/flags.tmp" && mv -f "$call/flags.tmp" "$call/flags"

if [ ! -s "$call/stderr" ]; then
	rm -f "$call/stderr"
fi
exit 0
