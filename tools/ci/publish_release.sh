#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-or-later
#
# publish_release.sh VERSION NOTES FILE...
#
# Publishes the GitHub release of VERSION, a tag that origin must hold at
# GITHUB_SHA, with the file NOTES as its notes and FILE... as its files.
# release.yml's publish job runs it.
#
# It finds and changes releases by id, never by tag name. GitHub's lookup
# by tag (GET /repos/{owner}/{repo}/releases/tags/{tag}) answers for a
# published release only. gh's fallback for drafts asks GraphQL for one
# release of the tag, so with two drafts it sees only one, and when that
# lookup fails it says "release not found", which a script can't tell from
# no draft at all. Then a retry makes a second draft, and publishing by tag
# name can publish a failed run's partial one, which immutable releases
# then keep. So:
#
# 1. It lists the repository's releases, drafts included. A published
#    release of VERSION stops it: what's out is never changed. Every draft
#    of VERSION, which a failed run left, is deleted.
# 2. It makes a draft and keeps its id, uploads each file into that draft
#    and checks that the upload is whole, then publishes that id. A version
#    with a prerelease part, such as v0.1.0-alpha.1, is a prerelease, and
#    any other becomes the latest release.
# 3. Right before it makes the draft, and again right before it publishes
#    it, it checks that refs/tags/VERSION on origin still names GITHUB_SHA,
#    so a tag moved after checkout, or while the files upload, can't carry
#    this build.
# 4. A draft stays editable by anyone who can write to the repository, so
#    before it publishes, it checks that the release is still a draft and
#    that its assets are exactly the files, each at its SHA-256. It
#    publishes under VERSION's tag alone, and checks the publish answer
#    the same way, with its tag, so a file swapped in the last moment,
#    which immutable releases then keep, fails the job.
#
# If it stops after making the draft and before publishing it, the draft
# stays unpublished, and the next run's step 1 deletes it. The exceptions
# are a draft someone published outside this run, and a publish answer
# that isn't the release this run uploaded: that release is out and
# frozen, so the job fails and says to check it.
#
# GH_REPO names the repository (owner/name), and gh reads its token from
# GH_TOKEN. PUBLISH_RETRY_SECONDS, 5 unless set, is the pause before an
# upload is tried again. It needs gh, git, jq, coreutils and diff, prints
# the release's URL and adds that to GITHUB_STEP_SUMMARY when it's set.
# tools/ci's TestPublishRelease runs it against a fake gh.
set -euo pipefail

if [ $# -lt 3 ]; then
	echo "usage: publish_release.sh VERSION NOTES FILE..." >&2
	exit 2
fi
version=$1
notes=$2
shift 2
: "${GH_REPO:?names the repository, owner/name}"
: "${GITHUB_SHA:?names the commit this run built}"
api=repos/$GH_REPO

# tools/release check refused build metadata, so a hyphen means a
# prerelease part.
prerelease=false
case "$version" in
*-*) prerelease=true ;;
esac

for file in "$@"; do
	if [ ! -f "$file" ]; then
		echo "::error::$file isn't a file to publish"
		exit 2
	fi
done
# want_assets is what the release must hold: each file's name and its
# SHA-256 as GitHub gives an asset's digest, a line each, sorted.
want_assets=$(for file in "$@"; do printf '%s\tsha256:%s\n' "$(basename "$file")" "$(sha256sum "$file" | cut -d ' ' -f 1)"; done | sort)

# check_assets stops the script with the message WHAT unless GOT, a
# release's assets listed as want_assets lists them, is exactly
# want_assets. A null digest, which the API allows, matches no file.
check_assets() {
	local what=$1 got=$2
	if [ "$got" != "$want_assets" ]; then
		echo "::error::$what"
		{ diff <(printf '%s\n' "$want_assets") <(printf '%s\n' "$got") || true; } | sed -n 's/^< /  uploaded: /p; s/^> /  found:    /p'
		exit 1
	fi
}

# check_tag stops the script unless refs/tags/VERSION on origin names
# GITHUB_SHA. An annotated tag's own line names the tag object, and its ^{}
# line the commit the tag points at.
check_tag() {
	local refs tagged
	refs=$(git ls-remote origin "refs/tags/$version" "refs/tags/$version^{}")
	tagged=$(awk -v ref="refs/tags/$version" '$2 == ref { plain = $1 } $2 == ref "^{}" { peeled = $1 } END { print (peeled != "" ? peeled : plain) }' <<<"$refs")
	if [ "$tagged" != "$GITHUB_SHA" ]; then
		echo "::error::refs/tags/$version on origin names ${tagged:-no commit}, not $GITHUB_SHA, which this run built, so it won't be published"
		exit 1
	fi
}

# upload puts FILE into draft release id at upload_url and checks that the
# answer is the whole file, uploaded. It tries three times, as gh release
# upload does, since an upload can fail on a 5xx or a dropped connection.
# A failed try can leave an asset of the name behind, such as an empty one
# in state starter, and the API refuses a second asset of a name, so before
# trying again it deletes those. gh sends the -f fields of a request with
# --input as its query.
upload() {
	local file=$1 name want got try assets asset
	name=$(basename "$file")
	want="uploaded $(stat -c %s "$file")"
	for try in 1 2 3; do
		if got=$(gh api --method POST "$upload_url" -H "Content-Type: application/octet-stream" -f name="$name" --input "$file" | jq -r '"\(.state) \(.size)"'); then
			if [ "$got" != "$want" ]; then
				echo "::error::uploading $name into draft release $id answered \"$got\", not \"$want\", so the draft stays unpublished"
				exit 1
			fi
			echo "Uploaded $name"
			return
		fi
		[ "$try" -lt 3 ] || break
		echo "Uploading $name failed, so it goes again"
		assets=$(gh api --paginate "$api/releases/$id/assets" | jq -r --arg n "$name" '.[] | select(.name == $n) | .id')
		for asset in $assets; do
			gh api --method DELETE "$api/releases/assets/$asset" --silent
		done
		sleep "${PUBLISH_RETRY_SECONDS:-5}"
	done
	echo "::error::uploading $name into draft release $id failed three times, so the draft stays unpublished"
	exit 1
}

# 1. A token that can push lists drafts too. A failed list stops the script
# here, so it never reads as a tag without releases. --paginate prints the
# pages as one array, or as one array a page in older gh; .[] reads both.
releases=$(gh api --paginate "$api/releases" | jq -r --arg v "$version" '.[] | select(.tag_name == $v) | "\(.id) \(.draft)"')
drafts=()
while read -r id draft; do
	[ -n "$id" ] || continue
	if [ "$draft" != true ]; then
		echo "::error::release $version is already published (release $id), and this run won't replace its files"
		exit 1
	fi
	drafts+=("$id")
done <<<"$releases"
for id in "${drafts[@]}"; do
	echo "Deleting draft release $id of $version, which a failed run left"
	gh api --method DELETE "$api/releases/$id" --silent
done

# 2 and 3. The draft's upload_url is a URI template, {?name,label} at the
# end.
check_tag
created=$(gh api --method POST "$api/releases" -f tag_name="$version" -f name="$version" -F body=@"$notes" -F draft=true -F prerelease="$prerelease")
id=$(jq -r .id <<<"$created")
upload_url=$(jq -r .upload_url <<<"$created")
upload_url=${upload_url%%\{*}
case "$id" in
"" | *[!0-9]*)
	echo "::error::creating the draft of $version answered without a release id: $created"
	exit 1
	;;
esac
echo "Made draft release $id of $version"
for file in "$@"; do
	upload "$file"
done

# 4. Still a draft, then the draft's assets, every one of them, against
# the files. A writer could have published the draft after its last upload,
# swapped or not, and then nothing this run does can change it.
still=$(gh api "$api/releases/$id" | jq -r .draft)
if [ "$still" != true ]; then
	echo "::error::release $id of $version was published outside this run, and immutable releases keep its files as they are. Check the release now."
	exit 1
fi
got=$(gh api --paginate "$api/releases/$id/assets" | jq -r '.[] | "\(.name)\t\(.digest)"' | sort)
check_assets "draft release $id doesn't hold exactly the files this run uploaded, each at its SHA-256, so it won't be published:" "$got"

check_tag
publish=(-f tag_name="$version" -F draft=false)
if [ "$prerelease" = false ]; then
	publish+=(-f make_latest=true)
fi
published=$(gh api --method PATCH "$api/releases/$id" "${publish[@]}")
draft=$(jq -r .draft <<<"$published")
tag=$(jq -r .tag_name <<<"$published")
if [ "$draft" != false ]; then
	echo "::error::publishing draft release $id answered draft $draft, so $version isn't out"
	exit 1
fi
# Out and frozen from here, so report a wrong tag and wrong files both.
if [ "$tag" != "$version" ]; then
	echo "::error::release $id is published under tag $tag, not $version, and immutable releases keep it as it is. Check the release now."
fi
got=$(jq -r '.assets[] | "\(.name)\t\(.digest)"' <<<"$published" | sort)
check_assets "release $id of $version is published, but its files aren't exactly the ones this run uploaded, and immutable releases keep them as they are. Check the release now:" "$got"
if [ "$tag" != "$version" ]; then
	exit 1
fi
url=$(jq -r .html_url <<<"$published")
echo "Published $version: $url"
if [ -n "${GITHUB_STEP_SUMMARY:-}" ]; then
	echo "Published $version: $url" >>"$GITHUB_STEP_SUMMARY"
fi
