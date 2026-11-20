#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-or-later
#
# Builds the releases that install/e2e/test.sh and test.ps1 install
# (docs/PLAN.md, WP-RL2) the way release.yml's build job builds a snapshot:
# its -ldflags, tools/release notices and dist, the scripts tools/release
# scripts writes, with their SHA-256s in the payload, and a throwaway key
# from tools/release sign. It builds only the three platforms the e2e jobs
# run on. Beside that release it makes the broken copies the scripts must
# refuse.
#
#   bash install/e2e/fixture.sh <out dir> <version>
#
# It runs from the repository root and needs Go, jq and python3. In <out
# dir>, each directory is a release, and keys/ holds each one's throwaway
# public key:
#
#   real/            the release, with install.sh and install.ps1
#   repo/            the repository's install.sh and install.ps1, which
#                    pin no version and no key, whatever release-keys.txt
#                    holds
#   fake/            install/e2e/fakepairtime, which has an install command
#   broken/          fakepairtime built so that its version fails
#   bad-archive/     real/ with one byte of each archive changed
#   bad-binary/      real/'s archives, with a manifest that gives each binary
#                    a wrong SHA-256, signed by keys/bad-binary.pub's key
#   bad-payload/     real/ with its manifest's payload changed after signing
#   bad-signature/   real/ with keys/other.pub's signature under real's key id
#   big-s/           real/ with L added to its signature's S, which still
#                    satisfies the equation but isn't a canonical signature
#   two-signatures/  real/ with a manifest that keys/two.pub's first key
#                    didn't sign under its own id, then a good signature by
#                    the second
#   noncanonical-signature/
#                    real/ with one of the unused bits of its signature's
#                    last base64 character set, which decodes to the same
#                    64 bytes everywhere but internal/release
set -euo pipefail

if [ "$#" -ne 2 ]; then
  echo "usage: bash install/e2e/fixture.sh <out dir> <version>" >&2
  exit 2
fi
out=$1
version=$2
targets="linux/amd64 darwin/arm64 windows/amd64"
base=https://github.com/some-du6e/pairtime/releases/download/$version
commit=$(git rev-parse HEAD)
date=$(TZ=UTC git log -1 --format=%cd --date=format-local:%Y-%m-%dT%H:%M:%SZ)
# Scratch space. CI runners are thrown away after the job, so it stays.
scratch=$(mktemp -d)
tool=$scratch/release

mkdir "$out" "$out/keys"
echo "$version" > "$out/version.txt"
go build -o "$tool" ./tools/release

# build NAME PACKAGE LDFLAGS: a binary and its notices per target, in
# $scratch/NAME/bin, as release.yml's Build and Third-party notices steps
# lay them out.
build() {
  local name=$1 pkg=$2 ldflags=$3 target goos goarch ext dir
  for target in $targets; do
    goos=${target%/*}
    goarch=${target#*/}
    ext=""
    if [ "$goos" = windows ]; then ext=.exe; fi
    dir=$scratch/$name/bin/${goos}_${goarch}
    GOOS=$goos GOARCH=$goarch go build -trimpath -buildvcs=false -ldflags="$ldflags" -o "$dir/pairtime$ext" "$pkg"
    if [ "$name" = real ]; then
      "$tool" notices -bin "$dir/pairtime$ext" -out "$dir/THIRD_PARTY_NOTICES.txt"
    else
      echo "A test build of install/e2e/fakepairtime, never released." > "$dir/THIRD_PARTY_NOTICES.txt"
    fi
  done
}

# release NAME [scripts]: the archives, checksums.txt and a manifest signed
# with a new throwaway key in $out/NAME, and the key in $out/keys/NAME.pub.
# With scripts, also the install scripts, whose SHA-256s go into the
# payload before it's signed, as in release.yml.
release() {
  local name=$1
  "$tool" dist -version "$version" -commit "$commit" -date "$date" -min-version "$version" \
    -base-url "$base" -targets "$targets" -bin "$scratch/$name/bin" -extra LICENSE,README.md \
    -out "$out/$name" -payload "$scratch/$name.payload.json"
  if [ "${2:-}" = scripts ]; then
    "$tool" scripts -version "$version" -out "$out/$name" -payload "$scratch/$name.payload.json"
  fi
  "$tool" sign -throwaway -payload "$scratch/$name.payload.json" \
    -out "$out/$name/manifest.json" -pub-out "$out/keys/$name.pub"
  "$tool" verify -dir "$out/$name" -pub "$out/keys/$name.pub" -targets "$targets"
}

pkg=github.com/some-du6e/pairtime/internal/version
build real ./cmd/pairtime "-s -w -X $pkg.Version=$version -X $pkg.Commit=$commit -X $pkg.Date=$date"
release real scripts
bash tools/release/check.sh "$out/real" "$out/keys/real.pub"
mkdir "$out/repo"
cp install/install.sh install/install.ps1 "$out/repo/"

build fake ./install/e2e/fakepairtime "-s -w -X main.version=$version"
release fake
build broken ./install/e2e/fakepairtime "-s -w -X main.version=$version -X main.broken=yes"
release broken

copy() {
  cp -R "$out/real" "$out/$1"
}

copy bad-archive
python3 -I - "$out"/bad-archive/pairtime_* << 'EOF'
import sys
for path in sys.argv[1:]:
    with open(path, 'rb') as f:
        data = bytearray(f.read())
    data[len(data) // 2] ^= 0xff
    with open(path, 'wb') as f:
        f.write(data)
EOF

copy bad-binary
sed -E "s/(\"binary_sha256\": \")[0-9a-f]{64}/\1$(printf '%064d' 0)/" "$scratch/real.payload.json" > "$scratch/bad-binary.payload.json"
"$tool" sign -throwaway -payload "$scratch/bad-binary.payload.json" \
  -out "$out/bad-binary/manifest.json" -pub-out "$out/keys/bad-binary.pub"

copy bad-payload
jq -c '.payload |= (@base64d | sub("\"size\": "; "\"size\": 1") | @base64)' \
  "$out/real/manifest.json" > "$out/bad-payload/manifest.json"

copy bad-signature
"$tool" sign -throwaway -payload "$scratch/real.payload.json" -out "$scratch/other.json" -pub-out "$out/keys/other.pub"
jq -c --arg sig "$(jq -r '.signatures[0].sig' "$scratch/other.json")" '.signatures[0].sig = $sig' \
  "$out/real/manifest.json" > "$out/bad-signature/manifest.json"

copy big-s
python3 -I - "$out/real/manifest.json" > "$out/big-s/manifest.json" << 'EOF'
import base64, json, sys
L = 2**252 + 27742317777372353535851937790883648493
with open(sys.argv[1]) as f:
    envelope = json.load(f)
sig = base64.b64decode(envelope['signatures'][0]['sig'])
s = int.from_bytes(sig[32:], 'little') + L
envelope['signatures'][0]['sig'] = base64.b64encode(sig[:32] + s.to_bytes(32, 'little')).decode()
sys.stdout.write(json.dumps(envelope, separators=(',', ':')) + '\n')
EOF

copy noncanonical-signature
python3 -I - "$out/real/manifest.json" > "$out/noncanonical-signature/manifest.json" << 'EOF'
import json, sys
alphabet = 'ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/'
with open(sys.argv[1]) as f:
    envelope = json.load(f)
sig = envelope['signatures'][0]['sig']
# 64 bytes are 86 characters and ==; the last of the 86 carries 2 bits of
# the signature and 4 that base64 leaves 0.
assert len(sig) == 88 and sig.endswith('=='), sig
last = alphabet[alphabet.index(sig[85]) ^ 1]
envelope['signatures'][0]['sig'] = sig[:85] + last + '=='
sys.stdout.write(json.dumps(envelope, separators=(',', ':')) + '\n')
EOF

copy two-signatures
"$tool" sign -throwaway -payload "$scratch/real.payload.json" -out "$scratch/first.json" -pub-out "$scratch/first.pub"
"$tool" sign -throwaway -payload "$scratch/real.payload.json" -out "$scratch/second.json" -pub-out "$scratch/second.pub"
jq -c --slurpfile second "$scratch/second.json" \
  '.signatures = [{keyid: .signatures[0].keyid, sig: $second[0].signatures[0].sig}, $second[0].signatures[0]]' \
  "$scratch/first.json" > "$out/two-signatures/manifest.json"
{
  echo "# The first key. The signature under its id is the second key's, so it fails."
  cat "$scratch/first.pub"
  echo
  echo "# The second key, which signed."
  cat "$scratch/second.pub"
} > "$out/keys/two.pub"

# The broken copies must break what they say: only the archives and
# bad-binary still pass check.sh's signature check under their own keys.
for name in bad-payload bad-signature big-s noncanonical-signature; do
  if bash tools/release/check.sh "$out/$name" "$out/keys/real.pub" > /dev/null 2>&1; then
    echo "::error::check.sh accepted $name, which should fail its signature check"
    exit 1
  fi
done
if "$tool" verify -dir "$out/noncanonical-signature" -pub "$out/keys/real.pub" > /dev/null 2>&1; then
  echo "::error::tools/release verify accepted noncanonical-signature"
  exit 1
fi
if bash tools/release/check.sh "$out/bad-archive" "$out/keys/real.pub" > /dev/null 2>&1; then
  echo "::error::check.sh accepted bad-archive"
  exit 1
fi
bash tools/release/check.sh "$out/two-signatures" "$scratch/second.pub" > /dev/null
echo "fixture for $version in $out:"
ls -1 "$out"
