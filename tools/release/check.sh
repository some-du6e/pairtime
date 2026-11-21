#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-or-later
#
# Checks a release directory with no pairtime code at all: coreutils, jq,
# tar, unzip and OpenSSL. release.yml runs it after `tools/release verify`,
# so the Go verifier and this script check each other
# (docs/spec/update-manifest.md).
#
#   bash tools/release/check.sh <release dir> <public key file>
#
# The key file holds one line, pairtime-release-ed25519-public:<base64>.
set -euo pipefail

if [ "$#" -ne 2 ]; then
  echo "usage: bash tools/release/check.sh <release dir> <public key file>" >&2
  exit 2
fi
dist=$1
keyfile=$2
type=application/vnd.pairtime.update-manifest.v1+json
# Scratch space. CI runners are thrown away after the job, so it stays.
work=$(mktemp -d)

fail() {
  echo "::error::check.sh: $*" >&2
  exit 1
}

# 1. Every file checksums.txt lists has that SHA-256.
(cd "$dist" && sha256sum --check --strict checksums.txt)

# b64 TEXT FILE decodes TEXT into FILE, and fails unless TEXT is exactly
# how base64 writes those bytes: padded, and with the unused bits of the
# last character zero, as internal/release requires.
b64() {
  printf '%s' "$1" | base64 -d > "$2" 2> /dev/null && [ "$(base64 -w 0 < "$2")" = "$1" ]
}

# 2. The envelope's signature, checked by OpenSSL over DSSE's
#    pre-authentication encoding: "DSSEv1 <len> <type> <len> <payload>".
manifest=$dist/manifest.json
[ "$(jq -r .payloadType "$manifest")" = "$type" ] || fail "manifest.json's payloadType isn't $type"
# $(...) drops trailing newlines, which a JSON escape can put in the
# string, so the text must be as long as jq says the string is.
payload_b64=$(jq -r .payload "$manifest")
[ "$(jq '.payload | length' "$manifest")" -eq "${#payload_b64}" ] || fail "manifest.json's payload isn't in canonical base64"
b64 "$payload_b64" "$work/payload.json" || fail "manifest.json's payload isn't in canonical base64"
# Every signature, whichever key made it, is 64 bytes in canonical base64.
# Read untrimmed, one per line, a newline in one makes an extra line that
# fails too.
while IFS= read -r any; do
  if ! b64 "$any" "$work/any.sig" || [ "$(stat -c %s "$work/any.sig")" -ne 64 ]; then
    fail "a signature in manifest.json isn't 64 bytes in canonical base64"
  fi
done < <(jq -r '.signatures[].sig' "$manifest")
length=$(stat -c %s "$work/payload.json")
{
  printf 'DSSEv1 %d %s %d ' "${#type}" "$type" "$length"
  cat "$work/payload.json"
} > "$work/pae"

[ "$(grep -c '^pairtime-release-ed25519-public:' "$keyfile")" -eq 1 ] || fail "$keyfile should hold exactly one public key"
key=$(sed -n 's/^pairtime-release-ed25519-public://p' "$keyfile")
base64 -d <<< "$key" > "$work/key.raw"
[ "$(stat -c %s "$work/key.raw")" -eq 32 ] || fail "$keyfile doesn't hold a 32-byte key"
# An Ed25519 SubjectPublicKeyInfo is a fixed 12-byte DER prefix and the key
# (RFC 8410 section 4).
{
  printf '\x30\x2a\x30\x05\x06\x03\x2b\x65\x70\x03\x21\x00'
  cat "$work/key.raw"
} > "$work/key.der"
openssl pkey -pubin -inform DER -in "$work/key.der" -out "$work/key.pem"
keyid=$(sha256sum < "$work/key.raw" | cut -c1-16)

# $id below is jq's variable, not the shell's.
# shellcheck disable=SC2016
sig=$(jq -r --arg id "$keyid" '.signatures[] | select(.keyid == $id) | .sig' "$manifest")
[ -n "$sig" ] || fail "manifest.json has no signature with key id $keyid"
b64 "$sig" "$work/sig" || fail "the signature by key $keyid isn't in canonical base64"
openssl pkeyutl -verify -pubin -inkey "$work/key.pem" -rawin -in "$work/pae" -sigfile "$work/sig" \
  || fail "OpenSSL says the signature by key $keyid doesn't verify"

# 3. Each artifact: the archive has the manifest's name, size and SHA-256,
#    checksums.txt agrees, and the binary inside has binary_sha256.
version=$(jq -r .version "$work/payload.json")
total=$(jq -r '.artifacts | length' "$work/payload.json")
count=0
while IFS=$'\t' read -r os arch url sha size binsha; do
  case "$os" in
    windows) ext=zip exe=pairtime.exe ;;
    *) ext=tar.gz exe=pairtime ;;
  esac
  name=${url##*/}
  want=pairtime_${version#v}_${os}_${arch}.$ext
  [ "$name" = "$want" ] || fail "$os/$arch: the url names $name, not $want"
  [ "$(stat -c %s "$dist/$name")" = "$size" ] || fail "$name isn't $size bytes"
  echo "$sha  $dist/$name" | sha256sum --check --strict --quiet - || fail "$name doesn't have the manifest's SHA-256"
  grep -qxF "$sha  $name" "$dist/checksums.txt" || fail "checksums.txt doesn't list $name with the manifest's SHA-256"
  mkdir "$work/$os-$arch"
  if [ "$ext" = zip ]; then
    unzip -q "$dist/$name" "$exe" -d "$work/$os-$arch"
  else
    tar -xzf "$dist/$name" -C "$work/$os-$arch" "$exe"
  fi
  [ -x "$work/$os-$arch/$exe" ] || fail "$name: $exe isn't executable"
  echo "$binsha  $work/$os-$arch/$exe" | sha256sum --check --strict --quiet - || fail "$name: $exe doesn't have binary_sha256"
  count=$((count + 1))
done < <(jq -r '.artifacts[] | [.os, .arch, .url, .sha256, (.size | tostring), .binary_sha256] | @tsv' "$work/payload.json")
if [ "$count" -eq 0 ] || [ "$count" -ne "$total" ]; then
  fail "checked $count of $total artifacts"
fi

# 4. The install scripts: each the payload lists has its SHA-256, and each
#    in the directory is listed, since `curl | sh` runs it first
#    (docs/spec/update-manifest.md, Install scripts).
listed=$(jq -r '(.install_scripts // {}) | keys[]' "$work/payload.json")
for name in install.sh install.ps1; do
  if [ -e "$dist/$name" ] && ! grep -qxF -- "$name" <<< "$listed"; then
    fail "$name is in $dist, but manifest.json doesn't list its SHA-256"
  fi
done
scripts=0
while IFS=$'\t' read -r name sha; do
  case "$name" in
    '' | . | .. | */* | *\\*) fail "manifest.json lists an install script named \"$name\", which isn't a file name" ;;
  esac
  [ -f "$dist/$name" ] || fail "manifest.json lists install script $name, which $dist doesn't have"
  echo "$sha  $dist/$name" | sha256sum --check --strict --quiet - || fail "$name doesn't have the SHA-256 manifest.json lists"
  scripts=$((scripts + 1))
done < <(jq -r '(.install_scripts // {}) | to_entries[] | [.key, .value] | @tsv' "$work/payload.json")
# A jq failure in the loop's input would leave it checking nothing.
[ "$scripts" -eq "$(jq '(.install_scripts // {}) | length' "$work/payload.json")" ] || fail "checked $scripts of the install scripts manifest.json lists"

echo "check.sh: manifest.json for $version is signed by key $keyid (checked with OpenSSL), and its $count archives match it and checksums.txt, and its $scripts install scripts match it"
