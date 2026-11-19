#!/bin/sh
# SPDX-License-Identifier: AGPL-3.0-or-later
#
# Rebuilds corpus.jsonl, pkg/redact's planted-secret corpus, from the true
# and false positives in gitleaks' own rule sources at the pinned tag.
#
# Usage: testdata/redact/gitleaks/extract.sh [output file]
#
# Needs git, Go and the network: it clones gitleaks, adds dump.go.txt to its
# config generator, and runs that generator, which checks every rule against
# its examples and now also writes them out. The run must regenerate
# gitleaks' config/gitleaks.toml byte for byte and match the copy in
# pkg/redact/rules, so the corpus and the rules come from the same source.
# The secrets in the corpus are partly random (gitleaks generates them from
# each rule's regex), so two runs never write the same file.
set -eu

tag=v8.30.1
commit=83d9cd684c87d95d656c1458ef04895a7f1cbd8e

here=$(cd "$(dirname "$0")" && pwd)
repo=$(cd "$here/../../.." && pwd)
out=${1:-$here/corpus.jsonl}

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

git -c advice.detachedHead=false clone --quiet --depth 1 --branch "$tag" \
	https://github.com/gitleaks/gitleaks "$work/gitleaks"
got=$(git -C "$work/gitleaks" rev-parse HEAD)
if [ "$got" != "$commit" ]; then
	echo "extract.sh: gitleaks $tag is $got, want $commit" >&2
	exit 1
fi

utils=$work/gitleaks/cmd/generate/config/utils
cp "$here/dump.go.txt" "$utils/zz_pairtime_dump.go"
awk '
/^func Validate\(rule config\.Rule, truePositives \[\]string, falsePositives \[\]string\) \*config\.Rule \{$/ {
	print; print "\tpairtimeDump(rule, truePositives, falsePositives, nil, nil)"; n++; next
}
/^func ValidateWithPaths\(rule config\.Rule, truePositives map\[string\]string, falsePositives map\[string\]string\) \*config\.Rule \{$/ {
	print; print "\tpairtimeDump(rule, nil, nil, truePositives, falsePositives)"; n++; next
}
{ print }
END {
	if (n != 2) {
		print "extract.sh: found " n " of the 2 Validate functions in validate.go" > "/dev/stderr"
		exit 1
	}
}
' "$utils/validate.go" >"$utils/validate.go.new"
mv "$utils/validate.go.new" "$utils/validate.go"

# The generator reads rules/config.tmpl relative to its own directory.
(
	cd "$work/gitleaks/cmd/generate/config"
	PAIRTIME_GITLEAKS_DUMP="$work/dump.jsonl" CGO_ENABLED=0 go run . "$work/gitleaks.toml"
)

if ! cmp -s "$work/gitleaks.toml" "$work/gitleaks/config/gitleaks.toml"; then
	echo "extract.sh: the generator didn't reproduce gitleaks' config/gitleaks.toml" >&2
	exit 1
fi
if ! gzip -dc "$repo/pkg/redact/rules/gitleaks.toml.gz" | cmp -s - "$work/gitleaks.toml"; then
	echo "extract.sh: pkg/redact/rules/gitleaks.toml.gz isn't gitleaks $tag config/gitleaks.toml" >&2
	exit 1
fi

LC_ALL=C sort "$work/dump.jsonl" >"$out"
echo "extract.sh: wrote $(wc -l <"$out") rule cases to $out"
