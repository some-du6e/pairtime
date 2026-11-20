#!/bin/sh
# SPDX-License-Identifier: AGPL-3.0-or-later
#
# Installs pairtime on macOS or Linux. pairtime counts the hours you spend
# building with AI: https://github.com/some-du6e/pairtime
#
#   curl -fsSL https://github.com/some-du6e/pairtime/releases/download/<version>/install.sh | sh
#
# It stops at the first thing that's wrong, and until step 4 it changes
# nothing on this computer:
#
#   1. Downloads the release's manifest.json and checks that a pairtime
#      release key signed it (docs/spec/update-manifest.md).
#   2. Downloads the archive for this computer and checks it, and the
#      pairtime binary inside it, against the manifest's SHA-256s.
#   3. Runs the new binary's `pairtime version` and gives up if that fails
#      or takes over 60 seconds.
#   4. Puts the binary at ~/.local/bin/pairtime.
#   5. Runs `pairtime install`, which lists every change it would make and
#      asks before making any. It names the folders this script made, so
#      that `pairtime uninstall` takes those away too.
#
# It runs as you and refuses to run as root, so it never uses sudo. It
# sends nothing anywhere: its only requests are the downloads. It needs
# curl, tar, bc and sha256sum or shasum, which macOS and most Linux systems
# come with.
#
# Options go after `sh -s --` when the script comes through a pipe. The
# first four have an environment variable too:
#
#   --version V          PAIRTIME_VERSION=V          install release V instead of this script's
#   --base-url URL       PAIRTIME_BASE_URL=URL       download the release files from URL
#   --release-keys FILE  PAIRTIME_RELEASE_KEYS=FILE  trust the public keys in FILE instead of pairtime's
#   --no-install         PAIRTIME_NO_INSTALL=1       stop after step 4
#   --allow-unsigned                                 install an unsigned release; see below
#   --yes                                            answer yes for `pairtime install`
#
# --allow-unsigned lets a script that trusts no release key install a
# release that no key signed, checked only against its own manifest's
# SHA-256s. Once the script trusts a key, pinned or given, a manifest that
# no trusted key signed is always refused, with the option or without it.
# It has no environment variable, so it lasts for one run.
set -eu

# tools/release scripts fills in these two lines when release.yml attaches
# this script to a release: the release's version, and the release keys its
# binaries trust (internal/release/release-keys.txt). The copy in the
# repository has neither, so it needs --version and --release-keys.
pinned_version=''
pinned_keys=''

repo=https://github.com/some-du6e/pairtime
payload_type=application/vnd.pairtime.update-manifest.v1+json
# How long the new binary's `version` may take (DESIGN 3.12).
version_timeout=60

# RFC 8032's Ed25519 verification (section 5.1.7) in bc, which POSIX
# requires and macOS ships. macOS's openssl can't check Ed25519, and the
# downloaded binary can't vouch for itself, so the signature is checked
# here. Only public values go through it. Points are in extended
# coordinates (X:Y:Z:T), where x = X/Z, y = Y/Z and x*y = T/Z.
ed25519_bc='
scale = 0
p = 2^255 - 19
q = 2^252 + 27742317777372353535851937790883648493

/* x mod p, from 0 to p - 1 */
define md(x) {
  x = x % p
  if (x < 0) x = x + p
  return (x)
}

/* b^n mod p */
define pw(b, n) {
  auto r
  r = 1
  b = md(b)
  while (n > 0) {
    if (n % 2 == 1) r = r * b % p
    b = b * b % p
    n = n / 2
  }
  return (r)
}

d = md(-121665 * pw(121666, p - 2))
m = pw(2, (p - 1) / 4)

/* (x3:y3:z3:t3) = (x1:y1:z1:t1) + (x2:y2:z2:t2), by the formula in RFC
   8032 section 5.1.4, which also doubles a point */
define add() {
  auto a, b, c, e, f, g, h, k
  a = (y1 - x1) * (y2 - x2) % p
  b = (y1 + x1) * (y2 + x2) % p
  c = 2 * t1 * t2 % p * d % p
  k = 2 * z1 * z2 % p
  e = b - a
  f = k - c
  g = k + c
  h = b + a
  x3 = e * f % p
  y3 = g * h % p
  z3 = f * g % p
  t3 = e * h % p
  return (0)
}

/* (rx:ry:rz:rt) = s times (px:py:pz:pt), doubling and adding */
define mul(s) {
  auto j
  rx = 0; ry = 1; rz = 1; rt = 0
  while (s > 0) {
    if (s % 2 == 1) {
      x1 = rx; y1 = ry; z1 = rz; t1 = rt
      x2 = px; y2 = py; z2 = pz; t2 = pt
      j = add()
      rx = x3; ry = y3; rz = z3; rt = t3
    }
    x1 = px; y1 = py; z1 = pz; t1 = pt
    x2 = px; y2 = py; z2 = pz; t2 = pt
    j = add()
    px = x3; py = y3; pz = z3; pt = t3
    s = s / 2
  }
  return (0)
}

/* Decodes n, 32 bytes read as a little-endian number, into (px:py:pz:pt)
   and returns 1, or returns 0 if n encodes no point (section 5.1.3). */
define dec(n) {
  auto y, s, u, v, x
  y = n % 2^255
  s = n / 2^255
  if (y >= p) return (0)
  u = md(y * y - 1)
  v = md(d * y * y + 1)
  x = pw(u * pw(v, p - 2), (p + 3) / 8)
  if (md(v * x * x - u) != 0) x = x * m % p
  if (md(v * x * x - u) != 0) return (0)
  if (x == 0) if (s == 1) return (0)
  if (x % 2 != s) x = p - x
  px = x; py = y; pz = 1; pt = x * y % p
  return (1)
}

/* The base point B: y = 4/5 and x even */
by = 4 * pw(5, p - 2) % p
j = dec(by)
bx = px

/* 1 if R and S, a signature cut in two and read as little-endian
   numbers, are valid under the public key a, where k is SHA-512(R || A
   || message); otherwise 0. As Go does, it computes [S]B - [k]A and
   compares its encoding with R. */
define verify(a, r, s, k) {
  auto j, ax, ay, az, at, sx, sy, sz, st, w
  if (s >= q) return (0)
  if (dec(a) == 0) return (0)
  ax = px; ay = py; az = pz; at = pt
  px = bx; py = by; pz = 1; pt = bx * by % p
  j = mul(s)
  sx = rx; sy = ry; sz = rz; st = rt
  /* -A has the opposite x */
  px = md(-ax); py = ay; pz = az; pt = md(-at)
  j = mul(k % q)
  x1 = sx; y1 = sy; z1 = sz; t1 = st
  x2 = rx; y2 = ry; z2 = rz; t2 = rt
  j = add()
  if (md(z3) == 0) return (0)
  w = pw(z3, p - 2)
  /* encoded: y, with the low bit of x as bit 255 */
  if (md(y3 * w) + md(x3 * w) % 2 * 2^255 == r) return (1)
  return (0)
}
'

say() {
  printf '%s\n' "$*"
}

warn() {
  printf 'install.sh: warning: %s\n' "$*" >&2
}

died=''
die() {
  died=1
  printf 'install.sh: %s\n' "$*" >&2
  exit 1
}

# shell_quote WORD prints WORD so that a shell reads it back as one word:
# as it is when it holds nothing a shell treats specially, otherwise in
# single quotes, each ' in it written as '\''. Commands this script prints
# for you to run go through it.
shell_quote() {
  case $1 in
    '' | *[!A-Za-z0-9_./:@%+,-]*) printf "'%s'" "$(printf '%s' "$1" | sed "s/'/'\\\\''/g")" ;;
    *) printf '%s' "$1" ;;
  esac
}

# Cleanup, and a message for any failure die didn't explain. A failed
# install also removes the folders it made, which are empty then. Once the
# binary is in place it stays, and so do they.
work=''
new=''
made_bin=''
made_local=''
installed=''
finish=''
step='starting'
on_exit() {
  status=$?
  if [ -n "$new" ] && [ -f "$new" ]; then rm -f "$new"; fi
  if [ -n "$work" ] && [ -d "$work" ]; then rm -rf "$work"; fi
  if [ -z "$installed" ]; then
    if [ -n "$made_bin" ]; then rmdir "$HOME/.local/bin" 2> /dev/null || :; fi
    if [ -n "$made_local" ]; then rmdir "$HOME/.local" 2> /dev/null || :; fi
  fi
  if [ "$status" != 0 ] && [ -z "$died" ]; then
    case $status in
      # Ctrl-C, a closed terminal or kill, by the INT, HUP and TERM traps.
      129 | 130 | 143) why="stopped while $step" ;;
      *) why="stopped by an unexpected error while $step (status $status)" ;;
    esac
    if [ -n "$installed" ]; then
      printf 'install.sh: %s. pairtime %s is installed at %s. To finish setting it up, run: %s\n' "$why" "$version" "$target" "$finish" >&2
    else
      printf 'install.sh: %s. Nothing was installed.\n' "$why" >&2
    fi
  fi
}

usage() {
  say "Usage: curl -fsSL $repo/releases/download/<version>/install.sh | sh -s -- [options]"
  say ""
  say "Installs pairtime at ~/.local/bin/pairtime and runs \`pairtime install\`."
  say ""
  say "  --version V          install release V (PAIRTIME_VERSION)"
  say "  --base-url URL       download the release files from URL (PAIRTIME_BASE_URL)"
  say "  --release-keys FILE  trust the release public keys in FILE (PAIRTIME_RELEASE_KEYS)"
  say "  --no-install         don't run \`pairtime install\` (PAIRTIME_NO_INSTALL=1)"
  say "  --allow-unsigned     install a release that no key signed, if this script trusts no release key"
  say "  --yes                answer yes for \`pairtime install\`"
}

# size_of FILE prints FILE's length in bytes.
size_of() {
  wc -c < "$1" | tr -d ' '
}

# sha256 FILE prints FILE's SHA-256 in lowercase hex. The file comes in on
# standard input, so no name is printed beside the hash: for a name with a
# backslash in it, sha256sum and shasum start the line with one.
sha256() {
  if [ "$sha_tool" = coreutils ]; then
    sha256sum < "$1" | cut -d ' ' -f 1
  else
    shasum -a 256 < "$1" | cut -d ' ' -f 1
  fi
}

# sha512 prints the SHA-512 of standard input in lowercase hex.
sha512() {
  if [ "$sha_tool" = coreutils ]; then
    sha512sum | cut -d ' ' -f 1
  else
    shasum -a 512 | cut -d ' ' -f 1
  fi
}

# b64decode TEXT FILE writes the bytes that TEXT encodes in padded
# standard base64, and fails unless TEXT is exactly how base64 writes those
# bytes, as internal/release requires (keys.go, decodeB64): GNU and BusyBox
# base64, left to themselves, ignore the unused bits of the last character
# and read QR== as QQ==. BusyBox's base64 takes only -d, and old macOS's
# only --decode.
b64decode() {
  {
    printf '%s' "$1" | base64 --decode > "$2" 2> /dev/null ||
      printf '%s' "$1" | base64 -d > "$2" 2> /dev/null
  } && [ "$(base64 < "$2" | tr -d '\n')" = "$1" ]
}

# le_hex FILE prints FILE's bytes as one little-endian number in the
# uppercase hex bc reads, the way RFC 8032 reads keys and signatures.
le_hex() {
  od -A n -v -t x1 "$1" | awk '{ for (i = 1; i <= NF; i++) h = toupper($i) h } END { print h }'
}

# hex_le reads a hex string of bytes and prints it as one little-endian
# number, like le_hex.
hex_le() {
  awk '{ for (i = 1; i < length($0); i += 2) h = toupper(substr($0, i, 2)) h } END { print h }'
}

run_bc() {
  # BC_ENV_ARGS could load the math library or POSIX mode, either of which
  # would change what the program means.
  (
    unset BC_ENV_ARGS POSIXLY_CORRECT
    bc -q
  )
}

# ed25519_verify KEY MESSAGE SIGNATURE succeeds if the files hold a 32-byte
# public key, the signed bytes and a valid 64-byte signature of them.
ed25519_verify() {
  [ "$(size_of "$1")" = 32 ] && [ "$(size_of "$3")" = 64 ] || return 1
  dd if="$3" of="$work/sig.r" bs=32 count=1 2> /dev/null || return 1
  dd if="$3" of="$work/sig.s" bs=32 skip=1 count=1 2> /dev/null || return 1
  digest=$(cat "$work/sig.r" "$1" "$2" | sha512) || return 1
  [ "${#digest}" = 128 ] || return 1
  verdict=$(
    {
      printf '%s\n' "$ed25519_bc"
      printf 'ibase = 16\na = 0%s\nr = 0%s\ns = 0%s\nk = 0%s\nibase = A\nverify(a, r, s, k)\n' \
        "$(le_hex "$1")" "$(le_hex "$work/sig.r")" "$(le_hex "$work/sig.s")" "$(printf '%s\n' "$digest" | hex_le)"
    } | run_bc
  ) || return 1
  [ "$verdict" = 1 ]
}

# platform KERNEL MACHINE sets os and arch to Go's names for `uname -s` and
# `uname -m`, or fails if pairtime has no build for them.
platform() {
  case $1 in
    Linux) os=linux ;;
    Darwin) os=darwin ;;
    *) return 1 ;;
  esac
  case $2 in
    x86_64 | amd64) arch=amd64 ;;
    aarch64 | arm64) arch=arm64 ;;
    *) return 1 ;;
  esac
}

# check_version V succeeds if V is a pairtime version, like v0.1.0-alpha.1.
check_version() {
  case $1 in
    '' | *[!0-9A-Za-z.-]*) return 1 ;;
  esac
  printf '%s\n' "$1" | grep -Eq '^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?$'
}

# check_url URL succeeds for an https URL, or for an http one on this
# computer, which tests serve from, and sets proto for curl.
check_url() {
  case $1 in
    *[!A-Za-z0-9:/._~%+-]*) return 1 ;;
  esac
  case $1 in
    https://?*) proto='=https' ;;
    http://127.0.0.1:* | http://127.0.0.1/* | http://localhost:* | http://localhost/*) proto='=http,https' ;;
    *) return 1 ;;
  esac
}

# fetch URL FILE downloads URL into FILE.
fetch() {
  curl --fail --silent --show-error --location --proto "$proto" --proto-redir "$proto" \
    --tlsv1.2 --retry 2 --connect-timeout 30 --user-agent "$agent" --output "$2" "$1"
}

# run_with_timeout SECONDS OUTPUT COMMAND... runs COMMAND with its output in
# the file OUTPUT and kills it after SECONDS. It fails if COMMAND fails or
# is killed, with the status in $status.
run_with_timeout() {
  seconds=$1
  output=$2
  shift 2
  "$@" < /dev/null > "$output" 2>&1 &
  pid=$!
  (
    sleep "$seconds"
    kill -9 "$pid"
  ) > /dev/null 2>&1 &
  watchdog=$!
  status=0
  wait "$pid" || status=$?
  kill "$watchdog" 2> /dev/null || :
  [ "$status" = 0 ]
}

# load_keys puts each trusted public key, from --release-keys or from this
# script, in $work/keys/<key id> as its 32 raw bytes.
load_keys() {
  mkdir "$work/keys"
  if [ -n "$keys_file" ]; then
    [ -f "$keys_file" ] || die "--release-keys: $keys_file isn't a file"
    # Blank lines and lines starting with # don't count, as in
    # internal/release/release-keys.txt.
    grep -v '^[[:space:]]*#' "$keys_file" | tr -d ' \t\r' > "$work/keys.txt" || :
    keys_from=$keys_file
  else
    # One key per line: keys hold no spaces.
    printf '%s\n' "$pinned_keys" | tr ' ' '\n' > "$work/keys.txt"
    keys_from='this script'
  fi
  key_count=0
  key_ids=''
  # The test after || reads a last line that has no newline.
  while IFS= read -r key || [ -n "$key" ]; do
    case $key in
      '') continue ;;
      pairtime-release-ed25519-public:?*) ;;
      # Never print it: it's a secret.
      pairtime-release-ed25519-secret:*) die "$keys_from holds a release secret key, not a public key. Keep it secret, and replace it if anyone else could have seen it." ;;
      *) die "$keys_from holds a line that isn't a release public key (pairtime-release-ed25519-public: and the key in base64)" ;;
    esac
    if ! b64decode "${key#pairtime-release-ed25519-public:}" "$work/key" || [ "$(size_of "$work/key")" != 32 ]; then
      die "$keys_from holds a release public key that isn't 32 bytes in base64"
    fi
    # A key's id is the first 8 bytes of its SHA-256, in hex.
    id=$(sha256 "$work/key" | cut -c 1-16)
    mv "$work/key" "$work/keys/$id"
    key_count=$((key_count + 1))
    key_ids="$key_ids $id"
  done < "$work/keys.txt"
}

# open_manifest checks $work/manifest.json as docs/spec/update-manifest.md's
# Verifying steps 1 to 4 say, and writes its payload to $work/payload.json.
open_manifest() {
  manifest=$work/manifest.json
  bad="manifest.json isn't a pairtime update manifest in the form tools/release writes"
  [ "$(size_of "$manifest")" -le 1048576 ] || die "manifest.json is over 1 MiB, so it isn't a pairtime manifest"
  [ "$(wc -l < "$manifest" | tr -d ' ')" = 1 ] || die "$bad"
  # The envelope is one line of JSON with nothing in it that needs
  # escaping, so splitting it at each quote gives every token. This prints
  # the payload, then one "keyid:sig" line per signature; neither holds a
  # colon, and either may be empty.
  awk -F '"' -v type="$payload_type" '
    NR > 1 { exit 1 }
    {
      if (NF < 19 || $1 != "{" || $2 != "payload" || $3 != ":" || $5 != "," || $6 != "payloadType" || $7 != ":" || $8 != type || $9 != "," || $10 != "signatures" || $11 != ":[{") exit 1
      if ($4 !~ /^[A-Za-z0-9+\/]*=?=?$/) exit 1
      print $4
      for (i = 12; i + 7 <= NF; i += 8) {
        if ($i != "keyid" || $(i + 1) != ":" || $(i + 2) ~ /[^0-9A-Za-z._-]/ || $(i + 3) != "," || $(i + 4) != "sig" || $(i + 5) != ":" || $(i + 6) !~ /^[A-Za-z0-9+\/]*=?=?$/) exit 1
        print $(i + 2) ":" $(i + 6)
        if ($(i + 7) == "}]}") {
          if (i + 7 == NF) ok = 1
          break
        }
        if ($(i + 7) != "},{") exit 1
      }
    }
    END { if (!ok) exit 1 }
  ' "$manifest" > "$work/envelope" || die "$bad"
  b64decode "$(sed -n 1p "$work/envelope")" "$work/payload.json" || die "$bad"
  sed -n '2,$p' "$work/envelope" > "$work/signatures"
  # As in internal/release, every signature has to be 64 bytes in that
  # base64, whichever key it names, or the file isn't in the form.
  n=0
  while IFS=: read -r keyid sig; do
    n=$((n + 1))
    if ! b64decode "$sig" "$work/sig.$n" || [ "$(size_of "$work/sig.$n")" != 64 ]; then die "$bad"; fi
  done < "$work/signatures"

  # DSSE's pre-authentication encoding of the payload is what was signed.
  {
    printf 'DSSEv1 %s %s %s ' "${#payload_type}" "$payload_type" "$(size_of "$work/payload.json")"
    cat "$work/payload.json"
  } > "$work/signed"

  # The key id only picks which key to try (DSSE); any one signature that
  # verifies under a trusted key is enough.
  signer=''
  signed_by=''
  n=0
  while IFS=: read -r keyid sig; do
    n=$((n + 1))
    signed_by="$signed_by $keyid"
    case $keyid in
      '' | *[!0-9a-f]*) continue ;;
    esac
    if [ "${#keyid}" != 16 ] || [ ! -f "$work/keys/$keyid" ]; then continue; fi
    if ed25519_verify "$work/keys/$keyid" "$work/signed" "$work/sig.$n"; then
      signer=$keyid
      break
    fi
  done < "$work/signatures"

  if [ -n "$signer" ]; then
    say "manifest.json is signed by release key $signer."
  elif [ "$key_count" = 0 ] && [ "$allow_unsigned" = 1 ]; then
    # Only a script that trusts no key gets here (D-allow-unsigned-scope).
    warn "no key this script trusts signed manifest.json (signed by:${signed_by:- nobody}; this script trusts none). --allow-unsigned is set, so the install goes on, but only the manifest's SHA-256s check the download: nothing shows the files are pairtime's."
  else
    refusal="manifest.json isn't signed by a key this script trusts (signed by:${signed_by:- nobody}; trusted:$key_ids), so nothing was installed."
    if [ "$allow_unsigned" = 1 ]; then
      refusal="$refusal --allow-unsigned doesn't change that: it applies only when this script trusts no release key."
    fi
    die "$refusal"
  fi
}

# read_artifact finds this platform's entry in the payload, which the
# signature covers, and sets url, sha, size and binsha from it.
read_artifact() {
  # tools/release writes the payload as JSON indented by two spaces: the
  # version at the top, each archive's fields six spaces in.
  awk -v os="$os" -v arch="$arch" '
    function value(line) {
      sub(/^ *"[0-9a-z_]*": /, "", line)
      sub(/,$/, "", line)
      if (line ~ /^".*"$/) line = substr(line, 2, length(line) - 2)
      return line
    }
    /^  "version": / { print "version", value($0) }
    /^    [{]$/ { inside = 1; o = ""; a = ""; u = ""; s = ""; n = ""; b = ""; next }
    /^    [}],?$/ { if (inside && o == os && a == arch) print "artifact", u, s, n, b; inside = 0; next }
    inside && /^      "os": / { o = value($0) }
    inside && /^      "arch": / { a = value($0) }
    inside && /^      "url": / { u = value($0) }
    inside && /^      "sha256": / { s = value($0) }
    inside && /^      "size": / { n = value($0) }
    inside && /^      "binary_sha256": / { b = value($0) }
  ' "$work/payload.json" > "$work/fields" || die "manifest.json's payload isn't the JSON tools/release writes"

  # The manifest must be the requested release's, so an older signed one
  # can't stand in for it.
  got=$(sed -n 's/^version //p' "$work/fields")
  [ "$got" = "$version" ] || die "manifest.json is for pairtime ${got:-no version}, not $version, so nothing was installed"
  found=$(grep -c '^artifact ' "$work/fields" || :)
  [ "$found" != 0 ] || die "pairtime $version has no build for $os/$arch"
  [ "$found" = 1 ] || die "manifest.json lists $os/$arch more than once"
  grep '^artifact ' "$work/fields" > "$work/fields.artifact"
  read -r _ url sha size binsha < "$work/fields.artifact"
  for hash in "$sha" "$binsha"; do
    case $hash in
      *[!0-9a-f]*) die "manifest.json's SHA-256 for $os/$arch isn't lowercase hex" ;;
    esac
    [ "${#hash}" = 64 ] || die "manifest.json's SHA-256 for $os/$arch isn't 64 hex digits"
  done
  case $size in
    '' | *[!0-9]*) die "manifest.json's size for $os/$arch isn't a number" ;;
  esac
}

main() {
  step='reading the options'
  version=${PAIRTIME_VERSION:-$pinned_version}
  base_url=${PAIRTIME_BASE_URL:-}
  keys_file=${PAIRTIME_RELEASE_KEYS:-}
  # Only the option allows an unsigned install, never the environment, so
  # it can't outlast the run it was given for (D-allow-unsigned-scope).
  allow_unsigned=0
  no_install=${PAIRTIME_NO_INSTALL:-0}
  yes=0
  while [ "$#" -gt 0 ]; do
    case $1 in
      --version | --base-url | --release-keys)
        [ "$#" -ge 2 ] || die "$1 needs a value"
        case $1 in
          --version) version=$2 ;;
          --base-url) base_url=$2 ;;
          --release-keys) keys_file=$2 ;;
        esac
        shift 2
        ;;
      --version=*) version=${1#*=} && shift ;;
      --base-url=*) base_url=${1#*=} && shift ;;
      --release-keys=*) keys_file=${1#*=} && shift ;;
      --allow-unsigned) allow_unsigned=1 && shift ;;
      --no-install) no_install=1 && shift ;;
      --yes) yes=1 && shift ;;
      -h | --help) usage && exit 0 ;;
      *) die "unknown option $1; --help lists the options" ;;
    esac
  done
  case $no_install in
    0 | 1) ;;
    *) die "set PAIRTIME_NO_INSTALL to 1, or leave it unset" ;;
  esac

  # pairtime is set up for the person running it, in their own home. Run
  # as root, it would install for root, or under a sudo that keeps HOME it
  # would leave root's files in yours, so it stops before downloading
  # anything. That is stricter than pairtime hook and pairtime install,
  # which refuse root only in a home that root doesn't own: a sudo that
  # sets HOME to root's would otherwise install for root.
  [ "$(id -u)" != 0 ] || die "don't run install.sh as root or with sudo: pairtime installs into your own home folder, so run the command again from a normal terminal, without sudo."

  [ -n "$version" ] || die "this copy of install.sh doesn't name a release; pass --version, such as --version v0.1.0"
  check_version "$version" || die "$version isn't a pairtime version, such as v0.1.0 or v0.1.0-alpha.1"
  base_url=${base_url:-$repo/releases/download/$version}
  base_url=${base_url%/}
  check_url "$base_url" || die "--base-url must be an https URL, without user names, queries or spaces"
  [ -n "${HOME:-}" ] || die "HOME isn't set, so there's no ~/.local/bin to install into"
  # pairtime install takes only full paths for the folders this script made.
  case $HOME in
    /*) ;;
    *) die "HOME is $HOME, which isn't a full path, so there's no ~/.local/bin to install into" ;;
  esac
  agent="pairtime-install.sh/$version (+$repo)"

  step='finding this computer'
  kernel=$(uname -s)
  machine=$(uname -m)
  # A shell that Rosetta runs on an Apple silicon Mac says x86_64, but the
  # arm64 build runs natively.
  if [ "$kernel" = Darwin ] && [ "$(sysctl -n sysctl.proc_translated 2> /dev/null || :)" = 1 ]; then
    machine=arm64
  fi
  platform "$kernel" "$machine" || die "pairtime has no build for $kernel on $machine. It runs on 64-bit macOS and Linux, on x86_64 or arm64; on Windows, use install.ps1."

  for tool in curl tar od awk dd base64 grep sed; do
    command -v "$tool" > /dev/null 2>&1 || die "install.sh needs $tool, which this computer doesn't have. Install it, then run this again."
  done
  if command -v sha256sum > /dev/null 2>&1 && command -v sha512sum > /dev/null 2>&1; then
    sha_tool=coreutils
  elif command -v shasum > /dev/null 2>&1; then
    sha_tool=shasum
  else
    die "install.sh needs sha256sum and sha512sum, or shasum. Install one, then run this again."
  fi

  step='loading the release keys'
  work=$(mktemp -d 2> /dev/null || mktemp -d -t pairtime) || die "couldn't make a temporary directory"
  load_keys
  if [ "$key_count" = 0 ]; then
    if [ "$allow_unsigned" != 1 ]; then
      die "this script trusts no release key, so it can't check that pairtime $version is pairtime's. Releases made before pairtime had a release key are unsigned, and their notes say so. To install one anyway, pass --allow-unsigned: the download is then checked only against the SHA-256s in its own manifest."
    fi
  else
    command -v bc > /dev/null 2>&1 || die "install.sh needs bc to check the release signature, and this computer doesn't have it. Install bc, then run this again."
  fi

  say "Installing pairtime $version for $os/$arch."
  step='downloading manifest.json'
  fetch "$base_url/manifest.json" "$work/manifest.json" || die "couldn't download $base_url/manifest.json"
  step='checking manifest.json'
  open_manifest
  read_artifact

  name=pairtime_${version#v}_${os}_${arch}.tar.gz
  [ "${url##*/}" = "$name" ] || die "manifest.json's url for $os/$arch names ${url##*/}, not $name"
  step="downloading $name"
  fetch "$base_url/$name" "$work/$name" || die "couldn't download $base_url/$name"
  step="checking $name"
  got=$(size_of "$work/$name")
  [ "$got" = "$size" ] || die "$name is $got bytes, but manifest.json says $size, so it was changed or damaged on the way. Nothing was installed."
  [ "$(sha256 "$work/$name")" = "$sha" ] || die "$name doesn't have the SHA-256 manifest.json lists, so it was changed or damaged on the way. Nothing was installed."
  mkdir "$work/unpacked"
  tar -xzf "$work/$name" -C "$work/unpacked" pairtime 2> /dev/null || die "couldn't unpack pairtime from $name"
  if [ ! -f "$work/unpacked/pairtime" ] || [ -h "$work/unpacked/pairtime" ]; then die "$name has no pairtime binary"; fi
  [ "$(sha256 "$work/unpacked/pairtime")" = "$binsha" ] || die "the pairtime binary in $name doesn't have the SHA-256 manifest.json lists. Nothing was installed."
  say "$name and the pairtime binary in it match manifest.json."

  # The binary waits beside its place, on the same file system, until it
  # has shown it runs here, which also keeps a noexec /tmp out of the way.
  step='trying the new binary'
  bin_dir=$HOME/.local/bin
  target=$bin_dir/pairtime
  if [ ! -d "$HOME/.local" ]; then made_local=1; fi
  if [ ! -d "$bin_dir" ]; then made_bin=1; fi
  mkdir -p "$bin_dir" || die "couldn't create $bin_dir"
  [ ! -d "$target" ] || die "$target is a directory, so pairtime can't go there"
  new=$bin_dir/.pairtime.new.$$
  cp "$work/unpacked/pairtime" "$new" || die "couldn't write to $bin_dir"
  chmod 755 "$new"
  [ "$(sha256 "$new")" = "$binsha" ] || die "the copy in $bin_dir doesn't match manifest.json; is the disk full?"
  if ! run_with_timeout "$version_timeout" "$work/version.out" "$new" version; then
    if [ "$status" = 137 ]; then
      die "the new pairtime didn't finish \`pairtime version\` within $version_timeout seconds, so it wasn't installed. Any pairtime you had is still there."
    fi
    die "the new pairtime doesn't run on this computer (\`pairtime version\` exited with status $status: $(head -c 300 "$work/version.out")), so it wasn't installed. Any pairtime you had is still there."
  fi
  case $(head -n 1 "$work/version.out") in
    "pairtime $version "*) ;;
    *) die "the new binary says it's $(head -n 1 "$work/version.out" | head -c 200), not pairtime $version, so it wasn't installed" ;;
  esac
  # The command that finishes the setup, for every message that tells you
  # to run it. A pairtime install run later would find the folders this
  # script made already there, so it names them (pairtime install --made).
  finish="$(shell_quote "$target") install"
  if [ -n "$made_local" ]; then finish="$finish --made $(shell_quote "$HOME/.local")"; fi
  if [ -n "$made_bin" ]; then finish="$finish --made $(shell_quote "$bin_dir")"; fi
  step="moving pairtime to $target"
  mv -f "$new" "$target" || die "couldn't move pairtime to $target"
  new=''
  installed=1
  say "Installed pairtime $version at $target."

  step='running pairtime install'
  if [ "$no_install" = 1 ]; then
    say "To finish setting it up, run: $finish"
  elif "$target" help < /dev/null 2> /dev/null | grep -q '^  install '; then
    set -- install
    if [ "$yes" = 1 ]; then set -- "$@" --yes; fi
    # pairtime install records the folders it makes, so that pairtime
    # uninstall can take them away again. The ones this script made for
    # the binary exist by the time it starts, so it's told which they are
    # (pairtime install --made).
    if [ -n "$made_local" ]; then set -- "$@" --made "$HOME/.local"; fi
    if [ -n "$made_bin" ]; then set -- "$@" --made "$bin_dir"; fi
    say "Running pairtime install. It lists every change it would make and asks before making any."
    # The script itself came through standard input, so pairtime install
    # asks on the terminal instead.
    status=0
    if [ -t 0 ]; then
      "$target" "$@" || status=$?
    elif (: < /dev/tty) 2> /dev/null; then
      "$target" "$@" < /dev/tty || status=$?
    else
      "$target" "$@" < /dev/null || status=$?
    fi
    [ "$status" = 0 ] || die "\`pairtime install\` stopped with status $status. pairtime is at $target; run \`$finish\` to try again."
  else
    say "This build of pairtime has no install command yet, so setup stops here."
  fi

  case ":${PATH:-}:" in
    *":$bin_dir:"*) ;;
    *)
      say "$bin_dir isn't on your PATH. To run pairtime by name, add this line to your shell's startup file, such as ~/.zshrc or ~/.bashrc:"
      say "  export PATH=\"\$HOME/.local/bin:\$PATH\""
      ;;
  esac
}

# Tests source this file with PAIRTIME_INSTALL_SH_TEST=functions to call
# its functions (install/e2e/test.sh). Everything above only defines
# things, so a download cut short runs nothing.
if [ "${PAIRTIME_INSTALL_SH_TEST:-}" != functions ]; then
  trap on_exit EXIT
  trap 'exit 129' HUP
  trap 'exit 130' INT
  trap 'exit 143' TERM
  main "$@"
fi
