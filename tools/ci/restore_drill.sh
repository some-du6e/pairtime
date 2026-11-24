#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-or-later
#
# restore_drill.sh image
# restore_drill.sh bundle DIR
# restore_drill.sh read-only
#
# The steps restore-drill.yml's two jobs share (DESIGN 10.3; deploy/runbook.md,
# step 14). Run from the repository's root on a GitHub runner: it uses docker,
# sudo, sqlite3, gh and RUNNER_TEMP.
#
# image makes pairtimed:drill, the image the drill deploys, in one of two ways:
#
#   pinned  pulls the image deploy/compose.yml pins, by digest. Its tag must
#           name the commit in its org.opencontainers.image.revision label,
#           and that commit must be on main, so a pin can't name an image
#           built from a branch.
#   source  builds the image from the checkout, as server-image.yml does.
#
# PAIRTIME_DRILL_IMAGE picks one. Unset or auto, the weekly drill uses the
# pinned image, the one hosts run, and any other run builds from source when
# the checkout changes what goes into the image (image_paths) against its
# base, so a PR's drill tests the image its code would make. A pull
# request's base is its merge commit's first parent, main as it stood when
# GitHub made the merge, and anything else's is where it forked from main,
# so a change main made since counts for neither. A pin bump, a bump of the
# other images or a runbook change runs the pinned image. A pull request
# that changes both the pin and what goes into the image fails, since one
# drill can't test both. Then image checks what it made: both programs
# answer, the image runs as 65532, and it holds the litestream.yml compose
# needs (#119). It records which one it made for bundle and read-only.
#
# bundle copies deploy/ to DIR, a drill host's bundle. A pinned image from
# before #123 runs litestream.frozen.yml, which compose.yml mounts for it; a
# source build runs the litestream.yml it carries, as hosts will once the
# next pin drops that mount, so its bundle loses the mount.
#
# read-only restores the replica in the buckets that S3_ENDPOINT, S3_REGION,
# S3_BUCKET, S3_SNAPSHOT_BUCKET, S3_ACCESS_KEY_ID and S3_SECRET_ACCESS_KEY
# name into a throwaway host under RUNNER_TEMP, runs PRAGMA integrity_check,
# serves the copy without Litestream, and checks that the newest snapshot and
# key backup exist. It writes nothing to the buckets. The production job runs
# it with the restore-drill environment's read-only keys, and the minio job
# runs it against MinIO, checking that both buckets list the same after it.
#
# Never run it with set -x: read-only writes the keys into a file.
set -euo pipefail

# image_paths are what goes into the server image: pairtimed's code, the
# packages it imports and the files they embed, the patched module copy it
# builds with, the notices tool, and the Dockerfile's other inputs.
# server-image.yml builds a new image on a push to main that changes one of
# them, and tools/ci's TestDrillImagePaths keeps its list and this one the
# same.
image_paths=(
	cmd/pairtimed
	internal/server
	deploy/embed.go
	deploy/pairtime.toml.tmpl
	deploy/litestream.yml
	docs/embed.go
	docs/retention.json
	internal/coach/text
	internal/devtoken
	internal/version
	pkg/event
	pkg/ledger
	pkg/params
	pkg/projectname
	pkg/seal
	third_party/burntsushi-toml
	tools/release
	internal/release
	LICENSE
	Dockerfile.pairtimed
	go.mod
	go.sum
)

# The pairtimed image's repository, as compose.yml and server-image.yml name
# it.
repository=ghcr.io/some-du6e/pairtimed

# frozen_mount matches compose.yml's mount of litestream.frozen.yml, the
# litestream.yml of a pinned image from before #123, which carries none.
frozen_mount='^[[:space:]]+- \./litestream\.frozen\.yml:/etc/litestream\.yml:ro[[:space:]]*$'

fail() {
	echo "::error::$*"
	exit 1
}

# pinned_image prints compose.yml's pairtimed image, tag and digest.
pinned_image() {
	awk -v repo="$repository:" '$1 == "image:" && index($2, repo) == 1 { print $2 }' deploy/compose.yml
}

# decide sets drill_mode to how image makes pairtimed:drill, pinned or
# source.
decide() {
	local base head diff
	case "${PAIRTIME_DRILL_IMAGE:-auto}" in
	pinned | source)
		drill_mode=$PAIRTIME_DRILL_IMAGE
		return
		;;
	auto | "") ;;
	*) fail "PAIRTIME_DRILL_IMAGE is '$PAIRTIME_DRILL_IMAGE', not auto, pinned or source" ;;
	esac
	# The weekly drill tests what hosts run.
	if [ "${GITHUB_EVENT_NAME:-}" = schedule ]; then
		drill_mode=pinned
		return
	fi
	# What the checkout changes, against the base it was made from. A pull
	# request is checked out as the merge commit GitHub made when the event
	# fired, whose first parent is the base branch as it stood then:
	# restore-drill.yml checks out two commits for it. A dispatch compares
	# with where its commit forked from main, from GitHub's compare API, so
	# a branch behind main isn't drilled from source for main's changes.
	head=$(git rev-parse HEAD)
	if [ "${GITHUB_EVENT_NAME:-}" = pull_request ]; then
		base=$(git rev-parse --quiet --verify 'HEAD^1^{commit}') ||
			fail "the checkout has no first parent to compare with: restore-drill.yml checks a pull request out with fetch-depth 2"
	else
		base=$(gh api "repos/$GITHUB_REPOSITORY/compare/main...$head" --jq .merge_base_commit.sha)
		git fetch --quiet --no-tags --depth=1 origin "$base"
	fi
	if git diff --quiet "$base" "$head" -- "${image_paths[@]}"; then
		drill_mode=pinned
		return
	fi
	diff=$(git diff "$base" "$head" -- deploy/compose.yml)
	if [ "${GITHUB_EVENT_NAME:-}" = pull_request ] &&
		grep -Eq '^[-+][[:space:]]+image:[[:space:]]+ghcr\.io/some-du6e/pairtimed:' <<< "$diff"; then
		fail "this PR changes compose.yml's pairtimed pin and what goes into the image too, and one drill can test only one of them: pin in a PR of its own (deploy/runbook.md, The image pin)"
	fi
	# Only the pinned image runs litestream.frozen.yml, and a source build's
	# drill runs without it, so an edit to it goes in a PR of its own, whose
	# drill runs the pinned image with it. Adding it, as #123 did, or
	# deleting it with its mount, as the next pin does, is no edit.
	if [ "${GITHUB_EVENT_NAME:-}" = pull_request ] &&
		! git diff --quiet --diff-filter=M "$base" "$head" -- deploy/litestream.frozen.yml; then
		fail "this PR edits deploy/litestream.frozen.yml, which only the pinned image runs, and changes what goes into the image too, whose drill runs without it: edit the frozen copy in a PR of its own (deploy/runbook.md, The image pin)"
	fi
	drill_mode=source
}

# build_source builds pairtimed:drill from the checkout, as server-image.yml
# builds its images, for linux/amd64.
build_source() {
	local pkg commit date
	pkg=github.com/some-du6e/pairtime/internal/version
	commit=$(git rev-parse HEAD)
	date=$(TZ=UTC git log -1 --format=%cd --date=format-local:%Y-%m-%dT%H:%M:%SZ)
	mkdir -p dist/linux_amd64
	GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -buildvcs=false \
		-ldflags="-s -w -X $pkg.Version=v0.0.0-drill -X $pkg.Commit=$commit -X $pkg.Date=$date" \
		-o dist/linux_amd64/pairtimed ./cmd/pairtimed
	go run ./tools/release notices -bin dist/linux_amd64/pairtimed -out dist/THIRD_PARTY_NOTICES.txt
	docker buildx build -f Dockerfile.pairtimed --platform linux/amd64 --load \
		--build-arg VERSION=v0.0.0-drill --build-arg COMMIT="$commit" -t pairtimed:drill .
	echo "The drill runs pairtimed:drill, built from the checkout at $commit."
}

# pull_pinned pulls compose.yml's pairtimed image and tags it pairtimed:drill,
# once its tag and label show it was built from a commit on main.
pull_pinned() {
	local pin tag rev status
	pin=$(pinned_image)
	case "$pin" in
	"$repository":*@sha256:*) ;;
	*) fail "compose.yml pins no pairtimed image by digest ('$pin'), so there is none to drill (deploy/runbook.md, The image pin)" ;;
	esac
	docker pull --quiet --platform linux/amd64 "$pin"
	rev=$(docker image inspect "$pin" --format '{{index .Config.Labels "org.opencontainers.image.revision"}}')
	tag=${pin#"$repository":}
	tag=${tag%%@*}
	if ! [[ $rev =~ ^[0-9a-f]{40}$ ]] || [ "$tag" != "sha-${rev:0:12}" ]; then
		fail "the pinned image's tag is $tag and its revision label '$rev'; a server-image.yml push is tagged sha-<12 hex digits of the commit it was built from>"
	fi
	# How main stands to rev: ahead or identical when rev is on main.
	status=$(gh api "repos/$GITHUB_REPOSITORY/compare/$rev...main" --jq .status)
	case "$status" in
	ahead | identical) ;;
	*) fail "the pinned image was built from $rev, which isn't on main (main is $status of it): pin an image server-image.yml pushed from main" ;;
	esac
	docker tag "$pin" pairtimed:drill
	echo "The drill runs the pinned image, $pin, built from $rev on main."
}

# check_image checks pairtimed:drill as mode made it.
check_image() {
	local mode=$1 user cid has=no mounts=no perm
	docker run --rm pairtimed:drill pairtimed version
	docker run --rm pairtimed:drill litestream version
	user=$(docker image inspect pairtimed:drill --format '{{.Config.User}}')
	if [ "$user" != 65532:65532 ]; then
		fail "the image runs as '$user', not 65532:65532"
	fi

	# Each image carries the litestream.yml it was built and drilled with,
	# so a rollback to it needs nothing from git (#119). Images from before
	# that have none, and compose.yml mounts litestream.frozen.yml for them.
	cid=$(docker create pairtimed:drill)
	if docker cp "$cid:/etc/litestream.yml" - > "$RUNNER_TEMP/image-litestream.tar" 2> /dev/null; then
		has=yes
	fi
	docker rm "$cid" > /dev/null
	if grep -Eq "$frozen_mount" deploy/compose.yml; then
		mounts=yes
	fi
	if [ "$has" = yes ]; then
		perm=$(tar -tvf "$RUNNER_TEMP/image-litestream.tar" | awk 'NR == 1 { print $1 }')
		if [ "$perm" != -rw-r--r-- ]; then
			fail "the image's /etc/litestream.yml has mode $perm, not -rw-r--r--, so 65532 may not read it"
		fi
		tar -xOf "$RUNNER_TEMP/image-litestream.tar" > "$RUNNER_TEMP/image-litestream.yml"
	fi
	case "$mode,$has,$mounts" in
	source,yes,*)
		if ! cmp -s "$RUNNER_TEMP/image-litestream.yml" deploy/litestream.yml; then
			fail "the image's /etc/litestream.yml isn't deploy/litestream.yml"
		fi
		echo "The image holds deploy/litestream.yml at /etc/litestream.yml, mode 0644, and the drill runs it without compose.yml's mount of litestream.frozen.yml, if any."
		;;
	source,no,*)
		fail "the image has no /etc/litestream.yml: Dockerfile.pairtimed copies deploy/litestream.yml there"
		;;
	pinned,yes,yes)
		fail "the pinned image carries its own /etc/litestream.yml, so compose.yml's mount of litestream.frozen.yml goes: delete the ./litestream.frozen.yml line under the pairtimed service, its comment and deploy/litestream.frozen.yml in this PR (deploy/runbook.md, The image pin)"
		;;
	pinned,no,no)
		fail "the pinned image has no /etc/litestream.yml of its own, so compose.yml has to mount litestream.frozen.yml over it"
		;;
	pinned,yes,no)
		echo "The pinned image runs its own litestream.yml."
		;;
	pinned,no,yes)
		echo "The pinned image is older than litestream.yml in the image, so compose.yml mounts litestream.frozen.yml for it."
		;;
	esac
}

image() {
	local drill_mode
	decide
	case "$drill_mode" in
	pinned) pull_pinned ;;
	source) build_source ;;
	esac
	check_image "$drill_mode"
	echo "$drill_mode" > "$RUNNER_TEMP/drill-mode"
}

# bundle copies deploy/ to $1 for the image image made. A source build runs
# its own litestream.yml, as hosts will once the next pin drops the frozen
# copy's mount, so its bundle loses that mount; a pinned image keeps it, as
# hosts run it now.
bundle() {
	local dir=$1 mode
	mode=$(cat "$RUNNER_TEMP/drill-mode" 2> /dev/null) ||
		fail "restore_drill.sh image runs first: bundle lays the host out for the image it made"
	cp -r deploy "$dir"
	if [ "$mode" = source ]; then
		sed -E -i "\#$frozen_mount#d" "$dir/compose.yml"
		if grep -q ':/etc/litestream\.yml' "$dir/compose.yml"; then
			fail "the drill's compose.yml still mounts a litestream.yml over the one the source build carries"
		fi
	fi
}

read_only() {
	local v d copy agent help keyshelp newkeys recorded newest only missing=""
	for v in S3_ENDPOINT S3_BUCKET S3_SNAPSHOT_BUCKET S3_ACCESS_KEY_ID S3_SECRET_ACCESS_KEY; do
		if [ -z "${!v:-}" ]; then
			missing="$missing $v"
		fi
	done
	if [ -n "$missing" ]; then
		fail "read-only needs$missing"
	fi
	d=$RUNNER_TEMP/read-only
	copy=$RUNNER_TEMP/read-only-copy
	bundle "$d"
	cd "$d"
	sudo install -d -m 0700 -o 65532 -g 65532 data data/tmp witness-roots
	mkdir config
	# Only the two env files are private: compose reads them on the host.
	# pairtime.toml has to stay readable by 65532, the image's user.
	(
		umask 077
		{
			echo "PAIRTIME_S3_ENDPOINT=$S3_ENDPOINT"
			echo "PAIRTIME_S3_REGION=${S3_REGION:-auto}"
			echo "PAIRTIME_S3_BUCKET=$S3_BUCKET"
			echo "PAIRTIME_S3_SNAPSHOT_BUCKET=$S3_SNAPSHOT_BUCKET"
			echo "PAIRTIME_S3_ACCESS_KEY_ID='$S3_ACCESS_KEY_ID'"
			echo "PAIRTIME_S3_SECRET_ACCESS_KEY='$S3_SECRET_ACCESS_KEY'"
		} > config/secrets.env
		: > config/tunnel.env
	)
	printf 'services:\n  pairtimed:\n    image: pairtimed:drill\n    pull_policy: never\n    network_mode: host\n' > drill.yml
	compose() { docker compose -f compose.yml -f drill.yml "$@"; }
	# This host's keys are throwaway ones, on purpose: the real ones stay
	# with the server. init refuses to make keys where the bucket shows a
	# server ran before, unless -new-keys (#133); an image from before that
	# has neither the check nor the flag.
	help=$(compose run --rm -T pairtimed pairtimed init -h 2>&1 || true)
	newkeys=()
	if grep -q -- '-new-keys' <<< "$help"; then
		newkeys=(-new-keys)
	fi
	# To a new name and then moved, as runbook step 7 does: a redirect
	# straight to pairtime.toml would make an empty one before init runs,
	# and init refuses to replace a pairtime.toml.
	compose run --rm -T pairtimed pairtimed init -base-url http://localhost:8080 "${newkeys[@]}" -config-out - > config/pairtime.toml.new
	chmod 0644 config/pairtime.toml.new
	mv config/pairtime.toml.new config/pairtime.toml

	# litestream restore reads the replica and writes only here.
	compose run --rm -T pairtimed litestream restore -config /etc/litestream.yml -o /data/pairtime.db /data/pairtime.db
	mkdir "$copy"
	sudo sh -c "cp '$d'/data/pairtime.db* '$copy/'"
	sudo chown -R "$(id -u):$(id -g)" "$copy"
	if [ "$(sqlite3 "$copy/pairtime.db" 'PRAGMA integrity_check')" != ok ]; then
		fail "the restored database fails PRAGMA integrity_check"
	fi
	echo "The replica restores to a database that passes integrity_check."

	# The server key the copy was written with, which its key backup has to
	# hold (#133); a database from before pairtimed recorded it has none.
	# The copy is served with this host's throwaway keys, so the record
	# goes from it, or serve would refuse it as written with other keys.
	recorded=
	if [ -n "$(sudo sqlite3 data/pairtime.db "SELECT name FROM sqlite_master WHERE type = 'table' AND name = 'server_keys'")" ]; then
		recorded=$(sudo sqlite3 data/pairtime.db 'SELECT server_key_id FROM server_keys WHERE id = 1')
		sudo sqlite3 data/pairtime.db 'DELETE FROM server_keys'
		sudo chown -R 65532:65532 data
	fi
	# The evidence key's history goes from the copy too: serve refuses an
	# evidence.p256 other than the one the database records as current
	# (WP-SB2; docs/spec/seal-v1.md 7), and this host's is a throwaway
	# one. A database from before key_history has none.
	if [ -n "$(sudo sqlite3 data/pairtime.db "SELECT name FROM sqlite_master WHERE type = 'table' AND name = 'key_history'")" ]; then
		sudo sqlite3 data/pairtime.db "DELETE FROM key_history WHERE kind = 'evidence'"
		sudo chown -R 65532:65532 data
	fi

	# pairtimed serve alone, without Litestream, so nothing writes to the
	# buckets; with no age recipient it takes no snapshot.
	compose run -d --name drill-serve pairtimed pairtimed serve > /dev/null
	if ! timeout 180 bash -c 'until curl -fsS -o /dev/null http://127.0.0.1:9091/readyz; do sleep 2; done'; then
		curl -sS http://127.0.0.1:9091/readyz || true
		docker logs drill-serve
		fail "the restored copy wasn't ready within 3 minutes"
	fi
	agent="pairtime-drill/${GITHUB_SHA::12} (+https://github.com/some-du6e/pairtime)"
	if [ "$(curl -fsS -A "$agent" http://127.0.0.1:8080/healthz)" != ok ]; then
		fail "the restored copy's /healthz didn't answer ok"
	fi
	curl -fsS -A "$agent" -o /dev/null http://127.0.0.1:8080/privacy
	# The newest key backup holds the server key the database was written
	# with. doctor's own key backup check compares it with this host's
	# keys, which are throwaway ones, so it is left out where the image
	# can list the backups; an image from before keys list has the older
	# check, which only asks that a backup exists. Which one is the
	# image's usage, as for init's -new-keys above, so a keys list that
	# fails, such as on a store error, fails the drill with its own error.
	keyshelp=$(docker exec drill-serve pairtimed keys -h 2>&1 || true)
	if grep -q 'pairtimed keys list' <<< "$keyshelp"; then
		if ! docker exec drill-serve pairtimed keys list > "$RUNNER_TEMP/key-backups.txt"; then
			fail "pairtimed keys list couldn't list the key backups in the main bucket; its error is above"
		fi
		newest=$(tail -n 1 "$RUNNER_TEMP/key-backups.txt")
		if [ -z "$newest" ]; then
			fail "there is no key backup in the main bucket"
		fi
		if [ -z "$recorded" ]; then
			echo "The newest key backup is $(cut -f 1 <<< "$newest"). The database records no server key to compare it with, since it was written before pairtimed recorded one (#133)."
		elif [ "$(cut -f 3 <<< "$newest")" != "$recorded" ]; then
			fail "the newest key backup, $(cut -f 1 <<< "$newest"), doesn't hold server key $recorded, which the database was written with: back the keys up again (deploy/runbook.md, step 8)"
		else
			echo "The newest key backup, $(cut -f 1 <<< "$newest"), holds server key $recorded, which the database was written with."
		fi
		only=snapshot,database,keys,config
	else
		only=snapshot,keys_backup,database,keys,config
	fi
	# Reads only: the newest snapshot exists and is recent. -skip push,
	# because "snapshot" in -only also names push.snapshot, which would
	# fail on the monitor URL this host lacks, or ping the real monitor if
	# it had one.
	docker exec drill-serve pairtimed doctor -only "$only" -skip push
	# Karim's key stays offline, so the snapshot is checked for age's
	# header alone.
	docker exec drill-serve pairtimed snapshot fetch -o /data/latest.db.age
	if ! sudo head -c 21 data/latest.db.age | grep -q 'age-encryption.org/v1'; then
		fail "the newest snapshot doesn't start with age's header"
	fi
	docker rm -f drill-serve > /dev/null
	echo "The copy serves, and the newest snapshot and key backup are there."
}

case "${1:-}" in
image) image ;;
bundle)
	if [ -z "${2:-}" ]; then
		echo "usage: restore_drill.sh bundle DIR" >&2
		exit 2
	fi
	bundle "$2"
	;;
read-only) read_only ;;
*)
	echo "usage: restore_drill.sh image | bundle DIR | read-only" >&2
	exit 2
	;;
esac
