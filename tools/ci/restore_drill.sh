#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-or-later
#
# restore_drill.sh image
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
# base, main or a pull request's base branch, so a PR's drill tests the
# image its code would make. A pin bump, a bump of the other images or a
# runbook change runs the pinned image. A pull request that changes both the
# pin and what goes into the image fails, since one drill can't test both.
# Then image checks what it made: both programs answer, the image runs as
# 65532, and it holds the litestream.yml compose needs (#119).
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

# image_paths are what goes into the server image: pairtimed's code and the
# files it embeds, the modules and the patched module copies it builds with,
# the notices tool, and the Dockerfile's other inputs. server-image.yml builds
# a new image on a push to main that changes one of them, and tools/ci's
# TestDrillImagePaths keeps its list and this one the same.
image_paths=(
	cmd/pairtimed
	internal/server
	internal/version
	docs/embed.go
	docs/retention.json
	'deploy/*.go'
	deploy/pairtime.toml.tmpl
	deploy/litestream.yml
	third_party
	tools/release
	Dockerfile.pairtimed
	go.mod
	go.sum
)

# The pairtimed image's repository, as compose.yml and server-image.yml name
# it.
repository=ghcr.io/some-du6e/pairtimed

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
	local base diff
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
	# The base's tip alone: the diff compares two trees and needs no
	# history. A pull request's checkout is its merge commit with the base.
	base=${GITHUB_BASE_REF:-main}
	git fetch --quiet --no-tags --depth=1 origin "refs/heads/$base"
	if git diff --quiet FETCH_HEAD HEAD -- "${image_paths[@]}"; then
		drill_mode=pinned
		return
	fi
	diff=$(git diff FETCH_HEAD HEAD -- deploy/compose.yml)
	if [ "${GITHUB_EVENT_NAME:-}" = pull_request ] &&
		grep -Eq '^[-+][[:space:]]+image:[[:space:]]+ghcr\.io/some-du6e/pairtimed:' <<< "$diff"; then
		fail "this PR changes compose.yml's pairtimed pin and what goes into the image too, and one drill can test only one of them: pin in a PR of its own (deploy/runbook.md, The image pin)"
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
	# that have none, and compose.yml mounts the checkout's copy for them.
	cid=$(docker create pairtimed:drill)
	if docker cp "$cid:/etc/litestream.yml" - > "$RUNNER_TEMP/image-litestream.tar" 2> /dev/null; then
		has=yes
	fi
	docker rm "$cid" > /dev/null
	if grep -Eq '^[[:space:]]+- \./litestream\.yml:/etc/litestream\.yml:ro[[:space:]]*$' deploy/compose.yml; then
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
		echo "The image holds deploy/litestream.yml at /etc/litestream.yml, mode 0644."
		;;
	source,no,*)
		fail "the image has no /etc/litestream.yml: Dockerfile.pairtimed copies deploy/litestream.yml there"
		;;
	pinned,yes,yes)
		fail "the pinned image carries its own /etc/litestream.yml, so compose.yml's mount of the checkout's copy goes: delete the ./litestream.yml line under the pairtimed service in this PR (deploy/runbook.md, The image pin)"
		;;
	pinned,no,no)
		fail "the pinned image has no /etc/litestream.yml of its own, so compose.yml has to mount the checkout's copy"
		;;
	pinned,yes,no)
		echo "The pinned image runs its own litestream.yml."
		;;
	pinned,no,yes)
		echo "The pinned image is older than litestream.yml in the image, so compose.yml mounts the checkout's copy."
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
}

read_only() {
	local v d copy agent missing=""
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
	cp -r deploy "$d"
	cd "$d"
	sudo install -d -m 0700 -o 65532 -g 65532 data witness-roots
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
	# To a new name and then moved, as runbook step 7 does: a redirect
	# straight to pairtime.toml would make an empty one before init runs,
	# and init refuses to replace a pairtime.toml.
	compose run --rm -T pairtimed pairtimed init -base-url http://localhost:8080 -config-out - > config/pairtime.toml.new
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
	# Reads only: the newest snapshot and key backup exist and are recent.
	# -skip push, because "snapshot" in -only also names push.snapshot,
	# which would fail on the monitor URL this host lacks, or ping the real
	# monitor if it had one.
	docker exec drill-serve pairtimed doctor -only snapshot,keys_backup,database,keys,config -skip push
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
read-only) read_only ;;
*)
	echo "usage: restore_drill.sh image | read-only" >&2
	exit 2
	;;
esac
