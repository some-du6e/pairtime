#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-or-later
#
# Tests install/install.sh against the releases install/e2e/fixture.sh
# builds (docs/PLAN.md, WP-RL2), served over HTTP on this computer. Each
# install runs in a new, empty HOME and TMPDIR with nothing else in its
# environment but PATH, and each case checks the exit status, the output,
# every file left in HOME and that TMPDIR is empty again. It runs on Linux
# and macOS, from the repository root:
#
#   bash install/e2e/test.sh <fixture dir>
#
# It needs curl, jq and python3, which runs the HTTP server,
# install/e2e/serve.py. install.sh runs with /bin/sh, the shell
# `curl ... | sh` gets, unless a case says otherwise.
set -euo pipefail

if [ "$#" -ne 1 ]; then
  echo "usage: bash install/e2e/test.sh <fixture dir>" >&2
  exit 2
fi
fixture=$(cd "$1" && pwd)
repo_script=$(pwd)/install/install.sh
stamped=$fixture/real/install.sh
version=$(cat "$fixture/version.txt")
keys=$fixture/keys
case $(uname -s) in
  Linux) os=linux ;;
  Darwin) os=darwin ;;
  *) echo "install/e2e/test.sh runs on Linux and macOS" >&2 && exit 2 ;;
esac
case $(uname -m) in
  x86_64) arch=amd64 ;;
  arm64 | aarch64) arch=arm64 ;;
esac
# Scratch space. CI runners are thrown away after the job, so it stays.
scratch=$(mktemp -d)
port=47615
server=http://127.0.0.1:$port
failures=0

python3 -I install/e2e/serve.py "$port" "$fixture" > "$scratch/server.log" 2>&1 &
server_pid=$!
trap 'kill "$server_pid" 2> /dev/null || :' EXIT
for _ in $(seq 100); do
  if curl -fsS -o /dev/null "$server/version.txt" 2> /dev/null; then break; fi
  sleep 0.1
done

sha256() {
  if command -v sha256sum > /dev/null; then sha256sum < "$1"; else shasum -a 256 < "$1"; fi | cut -d ' ' -f 1
}

# key_id FILE prints the id of the last public key in FILE: the first 8
# bytes of its SHA-256, in hex.
key_id() {
  sed -n 's/^pairtime-release-ed25519-public://p' "$1" | tail -n 1 | base64 --decode > "$scratch/key.raw"
  sha256 "$scratch/key.raw" | cut -c 1-16
}

fail() {
  echo "FAIL $1"
  echo "::error::install.sh e2e: $1"
  failures=$((failures + 1))
}

# fresh makes the next case's empty HOME and TMPDIR, and names the file
# fakepairtime's install writes to.
fresh() {
  home=$(mktemp -d "$scratch/home.XXXXXX")
  tmp=$(mktemp -d "$scratch/tmp.XXXXXX")
  record=$home.record
}

# run COMMAND... runs COMMAND in the case's HOME and TMPDIR, with nothing
# else in its environment but PATH. Leading NAME=VALUE words set more.
run() {
  env -i PATH="$PATH" HOME="$home" TMPDIR="$tmp" PAIRTIME_FAKE_RECORD="$record" "$@"
}

# tree DIR prints every path under DIR, sorted, on one line.
tree() {
  (cd "$1" && find . -mindepth 1 | LC_ALL=C sort | tr '\n' ' ' | sed 's/ $//')
}

installed='./.local ./.local/bin ./.local/bin/pairtime'

# The README's one-liner, for bash -c: the stamped script from the real
# release, installing the release in directory $2 of the server at $0 with
# the key file $1, and PAIRTIME_NO_INSTALL=$3.
# shellcheck disable=SC2016 # bash -c expands them
one_liner='curl -fsSL "$0/real/install.sh" | PAIRTIME_BASE_URL="$0/$2" PAIRTIME_RELEASE_KEYS="$1" PAIRTIME_NO_INSTALL="$3" sh'

# The real binary's cases stop before pairtime install with --no-install,
# so they don't change when it gains that command (WP-IN2). The cases just
# below run it, for real once it has the command.
#
# The cases that need a script trusting no release key run the repository's
# copy, which pins none: the stamped copies pin whatever release-keys.txt
# holds, which stops being nothing once Karim commits the release key.

# check NAME WANT PATTERN FILES COMMAND... runs COMMAND and checks that it
# exits 0 (WANT ok) or doesn't (WANT fails), that its output matches the
# extended regular expression PATTERN, that HOME then holds exactly FILES,
# and that TMPDIR is empty.
check() {
  local name=$1 want=$2 pattern=$3 files=$4 status=0 problems=() got
  shift 4
  "$@" > "$scratch/output" 2>&1 < /dev/null || status=$?
  if [ "$want" = ok ] && [ "$status" != 0 ]; then problems+=("exited $status, want 0"); fi
  if [ "$want" = fails ] && [ "$status" = 0 ]; then problems+=("exited 0, want a failure"); fi
  if ! grep -Eq -- "$pattern" "$scratch/output"; then problems+=("its output doesn't match /$pattern/"); fi
  got=$(tree "$home")
  if [ "$got" != "$files" ]; then problems+=("HOME holds [$got], want [$files]"); fi
  got=$(tree "$tmp")
  if [ -n "$got" ]; then problems+=("TMPDIR holds [$got], want nothing"); fi
  if [ "${#problems[@]}" = 0 ]; then
    echo "ok   $name"
  else
    fail "$name: ${problems[*]}"
    sed 's/^/     | /' "$scratch/output"
  fi
}

# not_in_output TEXT fails the case if the last check's output holds TEXT.
not_in_output() {
  if grep -qF -- "$1" "$scratch/output"; then fail "the output says \"$1\""; fi
}

# check_binary RELEASE checks that HOME's pairtime is RELEASE's binary for
# this platform and that it runs.
check_binary() {
  local want got
  want=$(jq -r --arg os "$os" --arg arch "$arch" \
    '.payload | @base64d | fromjson | .artifacts[] | select(.os == $os and .arch == $arch) | .binary_sha256' \
    "$fixture/$1/manifest.json")
  got=$(sha256 "$home/.local/bin/pairtime" 2> /dev/null || :)
  if [ "$got" != "$want" ]; then
    fail "the installed pairtime has SHA-256 ${got:-none}, not $1's $want"
  elif ! "$home/.local/bin/pairtime" version | grep -q "^pairtime $version "; then
    fail "the installed pairtime doesn't say it's $version"
  fi
}

# check_record WANT checks what fakepairtime's install wrote: its arguments,
# then what its standard input was. WANT is empty if it shouldn't have run.
check_record() {
  local got=''
  if [ -f "$record" ]; then got=$(cat "$record"); fi
  if [ "$got" != "$1" ]; then
    fail "fakepairtime install recorded [${got//$'\n'/; }], want [${1//$'\n'/; }]"
  fi
}

# install_then_uninstall runs the stamped script with --yes, which hands
# over to the real pairtime install, then the pairtime it put in place with
# uninstall --yes --delete-data, both in the case's home.
install_then_uninstall() {
  run sh "$stamped" --base-url "$server/real" --release-keys "$keys/real.pub" --yes || return
  run "$home/.local/bin/pairtime" uninstall --yes --delete-data
}

# check_no_agent fails if a process still runs from the case's home, as an
# agent that pairtime uninstall didn't stop would, and then stops it.
check_no_agent() {
  local real left=''
  real=$(cd "$home" && pwd -P)
  for _ in $(seq 50); do
    ps -A -o pid= -o command= > "$scratch/ps"
    left=$(grep -F -e "$home/" -e "$real/" "$scratch/ps" || :)
    if [ -z "$left" ]; then return 0; fi
    sleep 0.2
  done
  fail "a process from the case's home still runs after pairtime uninstall: $left"
  awk '{ print $1 }' <<< "$left" | xargs kill 2> /dev/null || :
}

echo "== install.sh, against the fixture for $version, on $os/$arch"

fresh
check "curl | sh installs a release signed by a key it was given" \
  ok 'Installed pairtime' "$installed" \
  run bash -c "$one_liner" "$server" "$keys/real.pub" real 1
check_binary real

mkdir "$scratch/real-binary"
tar -xzf "$fixture/real/pairtime_${version#v}_${os}_${arch}.tar.gz" -C "$scratch/real-binary" pairtime
if "$scratch/real-binary/pairtime" help | grep -q '^  install '; then
  # The real pairtime install and uninstall, through the script
  # (docs/PLAN.md, WP-RL2): set up with --yes in an empty home, then taken
  # out with --delete-data, they must leave the home as empty as they found
  # it, the folders the script made included, and no agent running.
  fresh
  check "with --yes in an empty home, the script and pairtime install set pairtime up, and pairtime uninstall --yes --delete-data leaves the home empty again" \
    ok 'Running pairtime install' '' \
    install_then_uninstall
  check_no_agent
else
  fresh
  check "without --no-install, setup stops before pairtime install while the binary has none" \
    ok 'has no install command yet' "$installed" \
    run sh "$stamped" --base-url "$server/real" --release-keys "$keys/real.pub"
fi

fresh
mkdir -p "$home/.local/bin"
echo 'an old pairtime' > "$home/.local/bin/pairtime"
check "a pairtime already in ~/.local/bin is replaced, and a PATH without it gets a hint" \
  ok "isn't on your PATH" "$installed" \
  run sh "$stamped" --base-url "$server/real" --release-keys "$keys/real.pub" --no-install
check_binary real

fresh
check "bash runs it too" ok 'Installed pairtime' "$installed" \
  run bash "$stamped" --base-url="$server/real" --release-keys="$keys/real.pub" --no-install

fresh
check "by default a script that trusts no key refuses the release, before anything is downloaded" \
  fails 'trusts no release key' '' \
  run sh "$repo_script" --version "$version" --base-url "$server/real"

fresh
check "--allow-unsigned has a script that trusts no key install an unsigned release, with a warning" \
  ok 'warning: no key this script trusts signed manifest.json' "$installed" \
  run sh "$repo_script" --version "$version" --base-url "$server/real" --allow-unsigned --no-install
check_binary real

fresh
check "PAIRTIME_ALLOW_UNSIGNED does nothing: a script that trusts no key still refuses the release" \
  fails 'pass --allow-unsigned' '' \
  run PAIRTIME_ALLOW_UNSIGNED=1 sh "$repo_script" --version "$version" --base-url "$server/real"

fresh
check "--allow-unsigned doesn't apply once the script trusts a key: a release no trusted key signed is refused" \
  fails "isn't signed by a key this script trusts.*--allow-unsigned doesn't change that" '' \
  run sh "$stamped" --base-url "$server/real" --release-keys "$keys/other.pub" --allow-unsigned

for refused in \
  "other.pub real a release signed by a key it doesn't trust" \
  "real.pub bad-signature another key's signature under the trusted key's id" \
  "real.pub bad-payload a payload changed after signing" \
  "real.pub big-s a signature whose S isn't reduced mod L"; do
  read -r key dir what <<< "$refused"
  fresh
  check "$what is refused, and nothing is installed" \
    fails "isn't signed by a key this script trusts" '' \
    run sh "$stamped" --base-url "$server/$dir" --release-keys "$keys/$key"
done

fresh
check "a signature whose base64 isn't canonical is refused, as internal/release refuses it, and nothing is installed" \
  fails "manifest.json isn't a pairtime update manifest in the form tools/release writes" '' \
  run sh "$stamped" --base-url "$server/noncanonical-signature" --release-keys "$keys/real.pub"

fresh
check "one good signature is enough, after one that fails under a trusted key's id" \
  ok "manifest.json is signed by release key $(key_id "$keys/two.pub")" "$installed" \
  run sh "$stamped" --base-url "$server/two-signatures" --release-keys "$keys/two.pub" --no-install

fresh
check "an archive with a wrong SHA-256 is refused, and nothing is installed" \
  fails "doesn't have the SHA-256 manifest.json lists" '' \
  run sh "$stamped" --base-url "$server/bad-archive" --release-keys "$keys/real.pub"

fresh
check "a binary with a wrong SHA-256 is refused, and nothing is installed" \
  fails "the pairtime binary in .* doesn't have the SHA-256 manifest.json lists" '' \
  run sh "$stamped" --base-url "$server/bad-binary" --release-keys "$keys/bad-binary.pub"

fresh
check "a binary whose version fails is never put in place, and the folders made for it go again" \
  fails "doesn't run on this computer" '' \
  run sh "$stamped" --base-url "$server/broken" --release-keys "$keys/broken.pub"

fresh
mkdir -p "$home/.local/bin"
echo 'an old pairtime' > "$home/.local/bin/pairtime"
check "a binary whose version fails leaves the pairtime that was there" \
  fails 'Any pairtime you had is still there' "$installed" \
  run sh "$stamped" --base-url "$server/broken" --release-keys "$keys/broken.pub"
if [ "$(cat "$home/.local/bin/pairtime")" != 'an old pairtime' ]; then fail "the old pairtime was changed"; fi

fresh
check "a signed manifest for another release is refused" \
  fails "manifest.json is for pairtime $version, not v0.0.0-snapshot.999999999" '' \
  run sh "$stamped" --version v0.0.0-snapshot.999999999 --base-url "$server/real" --release-keys "$keys/real.pub"

fresh
check "a build with an install command gets handed over to, told which folders the script made, with stdin from /dev/null when there's no terminal" \
  ok 'Running pairtime install' "$installed" \
  run sh "$stamped" --base-url "$server/fake" --release-keys "$keys/fake.pub"
check_record "install --made $home/.local --made $home/.local/bin"$'\nstdin: null'

fresh
mkdir "$home/.local"
check "--yes is passed on to pairtime install, and a ~/.local that was there isn't named as made" \
  ok 'Running pairtime install' "$installed" \
  run sh "$stamped" --base-url "$server/fake" --release-keys "$keys/fake.pub" --yes
check_record "install --yes --made $home/.local/bin"$'\nstdin: null'

fresh
mkdir -p "$home/.local/bin"
check "when ~/.local/bin was there, pairtime install hears of no folder the script made" \
  ok 'Running pairtime install' "$installed" \
  run sh "$stamped" --base-url "$server/fake" --release-keys "$keys/fake.pub"
check_record $'install\nstdin: null'

fresh
check "under curl | sh in a terminal, pairtime install reads the terminal, not the pipe" \
  ok 'Running pairtime install' "$installed" \
  run python3 -I -c 'import pty, sys; sys.exit(pty.spawn(sys.argv[1:]) >> 8)' \
  bash -c "$one_liner" "$server" "$keys/fake.pub" fake ''
check_record "install --made $home/.local --made $home/.local/bin"$'\nstdin: terminal'

fresh
check "--no-install stops once the binary is in place" ok 'To finish setting it up, run' "$installed" \
  run sh "$stamped" --base-url "$server/fake" --release-keys "$keys/fake.pub" --no-install
check_record ''

fresh
check "a failing pairtime install fails the script, and the binary stays" \
  fails 'pairtime install. stopped with status 4' "$installed" \
  run PAIRTIME_FAKE_INSTALL_EXIT=4 sh "$stamped" --base-url "$server/fake" --release-keys "$keys/fake.pub"

fresh
check "Ctrl-C during pairtime install says where pairtime is, not that nothing was installed" \
  fails "stopped while running pairtime install. pairtime $version is installed at $home/.local/bin/pairtime" "$installed" \
  run PAIRTIME_FAKE_INSTALL_INTERRUPT=1 sh "$stamped" --base-url "$server/fake" --release-keys "$keys/fake.pub"
not_in_output 'Nothing was installed'

fresh
home="$home/it's a home"
mkdir "$home"
check "the command it prints is quoted for a home with an apostrophe and a space in it" \
  ok 'To finish setting it up, run' "$installed" \
  run sh "$stamped" --base-url "$server/real" --release-keys "$keys/real.pub" --no-install
printed=$(sed -n 's/^To finish setting it up, run: //p' "$scratch/output")
# shlex reads the line as sh would, without running anything.
words=$(python3 -I -c 'import shlex, sys; print("\n".join(shlex.split(sys.argv[1])))' "$printed" 2>&1 || :)
if [ "$words" != "$home/.local/bin/pairtime"$'\ninstall' ]; then
  fail "the printed command [$printed] reads as [${words//$'\n'/, }], not $home/.local/bin/pairtime and install"
fi

if sudo -n true 2> /dev/null; then
  fresh
  check "as root, under sudo, it stops before downloading anything" \
    fails "don't run install.sh as root or with sudo" '' \
    sudo -n env -i PATH="$PATH" HOME="$home" TMPDIR="$tmp" sh "$stamped" --base-url "$server/real" --release-keys "$keys/real.pub"
  not_in_output 'Installing pairtime'
else
  echo "::notice::no passwordless sudo here, so install.sh's refusal to run as root goes untested"
fi

fresh
printf '%s' "$(cat "$keys/real.pub")" > "$scratch/no-newline.pub"
check "a key file without a final newline still counts its last key" ok 'Installed pairtime' "$installed" \
  run sh "$stamped" --base-url "$server/real" --release-keys "$scratch/no-newline.pub" --no-install

fresh
secret=$(head -c 32 /dev/zero | base64)
echo "pairtime-release-ed25519-secret:$secret" > "$scratch/secret.pub"
check "a secret key given as a public key is refused" fails 'holds a release secret key' '' \
  run sh "$stamped" --base-url "$server/real" --release-keys "$scratch/secret.pub"
if grep -qF -- "$secret" "$scratch/output"; then fail "install.sh printed the secret key"; fi

fresh
check "a base URL that isn't https or this computer is refused" fails 'must be an https URL' '' \
  run sh "$stamped" --base-url http://example.com/pairtime --release-keys "$keys/real.pub"

fresh
check "an unknown option is refused" fails 'unknown option --frobnicate' '' \
  run sh "$stamped" --frobnicate

fresh
check "the repository's copy names no release, so it needs --version" fails "doesn't name a release" '' \
  run sh "$repo_script" --base-url "$server/real" --release-keys "$keys/real.pub"

fresh
check "the repository's copy installs with --version and --release-keys" ok 'Installed pairtime' "$installed" \
  run sh "$repo_script" --version "$version" --base-url "$server/real" --release-keys "$keys/real.pub" --no-install

echo "== install.sh's functions"

# unit NAME runs standard input with install.sh's functions loaded, under
# set -eu, in sh. It must exit 0.
unit() {
  if { printf '. "%s"\n' "$repo_script" && cat; } | env -i PATH="$PATH" PAIRTIME_INSTALL_SH_TEST=functions VECTORS="$scratch/vectors" SCRATCH="$scratch" sh -s > "$scratch/unit" 2>&1; then
    echo "ok   $1"
  else
    fail "$1"
    sed 's/^/     | /' "$scratch/unit"
  fi
}

# RFC 8032 section 7.1's vectors, read from internal/release's test, with
# changed copies: a bit of R, a bit of S, a byte more message, another key.
python3 -I - "$scratch/vectors" internal/release/keys_test.go << 'EOF'
import os, re, sys
out, source = sys.argv[1], sys.argv[2]
with open(source) as f:
    text = f.read()
block = text[text.index('var rfc8032 = '):]
block = block[:block.index('\n}\n')]
found = re.findall(r'name:\s*"([^"]+)",\s*secret:\s*"[0-9a-f]*",\s*public:\s*"([0-9a-f]+)",\s*msg:\s*"([0-9a-f]*)",\s*sig:\s*"([0-9a-f]+)"', block)
assert len(found) == 5, found
os.mkdir(out)
keys = [bytes.fromhex(v[1]) for v in found]
names = []
for i, (name, public, msg, sig) in enumerate(found):
    key, msg, sig = bytes.fromhex(public), bytes.fromhex(msg), bytes.fromhex(sig)
    flip_r = bytes([sig[0] ^ 1]) + sig[1:]
    flip_s = sig[:40] + bytes([sig[40] ^ 0x10]) + sig[41:]
    cases = {
        'good': (key, msg, sig),
        'r': (key, msg, flip_r),
        's': (key, msg, flip_s),
        'msg': (key, msg + b'\0', sig),
        'key': (keys[(i + 1) % len(keys)], msg, sig),
    }
    n = 'v%d' % i
    names.append(n)
    for case, files in cases.items():
        for suffix, data in zip(('key', 'msg', 'sig'), files):
            with open(os.path.join(out, '%s.%s.%s' % (n, case, suffix)), 'wb') as f:
                f.write(data)
with open(os.path.join(out, 'names'), 'w') as f:
    f.write(' '.join(names) + '\n')
EOF

# A base64 like BusyBox's, which takes -d but not --decode, and BusyBox's
# own where this computer has it.
mkdir "$scratch/busybox-like"
cat > "$scratch/busybox-like/base64" << EOF
#!/bin/sh
for a in "\$@"; do
  if [ "\$a" = --decode ]; then echo "base64: unrecognized option '--decode'" >&2; exit 1; fi
done
exec $(command -v base64) "\$@"
EOF
chmod +x "$scratch/busybox-like/base64"
if command -v busybox > /dev/null; then
  mkdir "$scratch/busybox"
  ln -s "$(command -v busybox)" "$scratch/busybox/base64"
fi

unit "RFC 8032's five vectors verify, and each fails with a bit of R or S, the message or the key changed" << 'EOF'
if command -v sha512sum > /dev/null 2>&1; then sha_tool=coreutils; else sha_tool=shasum; fi
work=$(mktemp -d)
for v in $(cat "$VECTORS/names"); do
  for c in good r s msg key; do
    if ed25519_verify "$VECTORS/$v.$c.key" "$VECTORS/$v.$c.msg" "$VECTORS/$v.$c.sig"; then got=valid; else got=invalid; fi
    want=invalid
    if [ "$c" = good ]; then want=valid; fi
    if [ "$got" != "$want" ]; then echo "$v $c: $got, want $want"; exit 1; fi
  done
done
EOF

unit "a key or signature of the wrong length is invalid" << 'EOF'
sha_tool=shasum
if command -v sha512sum > /dev/null 2>&1; then sha_tool=coreutils; fi
work=$(mktemp -d)
head -c 31 "$VECTORS/v0.good.key" > "$work/short.key"
head -c 63 "$VECTORS/v0.good.sig" > "$work/short.sig"
if ed25519_verify "$work/short.key" "$VECTORS/v0.good.msg" "$VECTORS/v0.good.sig"; then exit 1; fi
if ed25519_verify "$VECTORS/v0.good.key" "$VECTORS/v0.good.msg" "$work/short.sig"; then exit 1; fi
EOF

unit "base64 decodes with GNU's, macOS's and BusyBox's tool, and only canonical base64 is accepted, as internal/release accepts it" << 'EOF'
out=$(mktemp)
for dir in '' "$SCRATCH/busybox-like" "$SCRATCH/busybox"; do
  if [ -n "$dir" ] && [ ! -d "$dir" ]; then continue; fi
  (
    if [ -n "$dir" ]; then PATH=$dir:$PATH; fi
    for good in aGk=:hi QQ==:A Zm9v:foo; do
      if ! b64decode "${good%%:*}" "$out" || [ "$(cat "$out")" != "${good#*:}" ]; then
        echo "${dir:-this base64}: ${good%%:*} didn't decode to ${good#*:}"
        exit 1
      fi
    done
    for bad in QR== QQ QQ=== aGk=aGk= 'aG k=' 'aGk=
'; do
      if b64decode "$bad" "$out"; then echo "${dir:-this base64}: accepted [$bad]"; exit 1; fi
    done
  ) || exit 1
done
EOF

unit "a SHA-256 is right for a path with a backslash in it, from sha256sum and from shasum" << 'EOF'
dir=$(mktemp -d)
printf x > "$dir/a\\b"
want=2d711642b726b04401627ca9fbac32f5c8530fb1903cc4db02258717921a4881
tried=0
for tool in coreutils shasum; do
  case $tool in
    coreutils) command -v sha256sum > /dev/null 2>&1 || continue ;;
    shasum) command -v shasum > /dev/null 2>&1 || continue ;;
  esac
  sha_tool=$tool
  got=$(sha256 "$dir/a\\b")
  [ "$got" = "$want" ] || { echo "$tool: $got"; exit 1; }
  tried=$((tried + 1))
done
[ "$tried" -gt 0 ]
EOF

unit "a path in a printed command reads back as one word, and a plain one isn't quoted" << 'EOF'
for w in /home/teen/.local/bin/pairtime "/Users/it's me/.local/bin/pairtime" 'a b' '$HOME' 'x"y' 'back\slash' "two''quotes" ''; do
  quoted=$(shell_quote "$w")
  eval "set -- $quoted"
  if [ "$#" != 1 ] || [ "$1" != "$w" ]; then echo "[$w] came back as $# words, [$*], from $quoted"; exit 1; fi
done
[ "$(shell_quote /home/teen/.local/bin/pairtime)" = /home/teen/.local/bin/pairtime ]
EOF

unit "uname's names map to Go's, and other platforms have no build" << 'EOF'
for row in "Linux x86_64 linux/amd64" "Linux aarch64 linux/arm64" "Darwin arm64 darwin/arm64" "Darwin x86_64 darwin/amd64"; do
  set -- $row
  platform "$1" "$2"
  [ "$os/$arch" = "$3" ] || { echo "$1 $2 gave $os/$arch, want $3"; exit 1; }
done
for row in "Linux i686" "Linux armv7l" "FreeBSD amd64" "MINGW64_NT-10.0 x86_64"; do
  set -- $row
  if platform "$1" "$2"; then echo "$1 $2 has a build?"; exit 1; fi
done
EOF

unit "only https URLs, and http ones on this computer, are downloaded from" << 'EOF'
for good in https://github.com/some-du6e/pairtime/releases/download/v0.1.0 http://127.0.0.1:8080/x http://localhost:1/; do
  check_url "$good" || { echo "refused $good"; exit 1; }
done
for bad in http://example.com/x https://user@example.com/x 'https://example.com/a b' 'https://example.com/?a=b' ftp://example.com/x http://127.0.0.1.example.com/x https://; do
  if check_url "$bad"; then echo "accepted $bad"; exit 1; fi
done
EOF

unit "versions must be pairtime's" << 'EOF'
for good in v0.1.0 v0.1.0-alpha.1 v10.20.30 v0.0.0-snapshot.12; do
  check_version "$good" || { echo "refused $good"; exit 1; }
done
for bad in 0.1.0 v0.1 v01.0.0 v0.1.0+build v0.1.0- 'v0.1.0
v0.2.0' "v1.0.0'"; do
  if check_version "$bad"; then echo "accepted $bad"; exit 1; fi
done
EOF

unit "a command that outlasts its time is killed, with status 137, and one that doesn't keeps its status" << 'EOF'
out=$(mktemp)
start=$(date +%s)
if run_with_timeout 2 "$out" sleep 30; then echo "sleep 30 finished in 2 seconds?"; exit 1; fi
[ "$status" = 137 ] || { echo "status $status, want 137"; exit 1; }
[ $(($(date +%s) - start)) -lt 15 ] || { echo "the kill took too long"; exit 1; }
run_with_timeout 5 "$out" sh -c 'echo hi; exit 0' || { echo "a quick command failed"; exit 1; }
[ "$(cat "$out")" = hi ] || { echo "output: $(cat "$out")"; exit 1; }
if run_with_timeout 5 "$out" sh -c 'exit 7'; then exit 1; fi
[ "$status" = 7 ] || { echo "status $status, want 7"; exit 1; }
EOF

if [ "$failures" != 0 ]; then
  echo "$failures checks failed"
  exit 1
fi
echo "every check passed"
