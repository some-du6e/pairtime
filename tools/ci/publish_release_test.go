// SPDX-License-Identifier: AGPL-3.0-or-later

package ci

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// fakeGH stands in for gh in TestPublishRelease. It answers gh api calls
// as the REST API does, from the files in FAKE_DIR, and writes each call
// to FAKE_DIR/calls as a line: the method, the path, then every other
// argument in the order given, so -f and -F, the headers and --paginate
// all show. The draft it makes is always release 900.
//
//   - releases.json holds the release list, a page a line. Without
//     --paginate only the first page comes back.
//   - assets.json is the draft's asset list.
//   - upload-fails/NAME holds how many more uploads of NAME fail with a
//     502, and upload-state and upload-size, if there, what an upload
//     answers instead of uploaded at the file's size.
const fakeGH = `#!/usr/bin/env bash
set -euo pipefail
if [ "${1:-}" != api ]; then
	echo "fake gh: only gh api is expected, not: gh $*" >&2
	exit 64
fi
shift
method=GET path="" input="" paginate=false logged=()
while [ $# -gt 0 ]; do
	case "$1" in
	-X | --method) method=$2; shift ;;
	-f | -F | -H | --raw-field | --field | --header) logged+=("$1" "$2"); shift ;;
	--input) input=$2; logged+=("$1" "$2"); shift ;;
	--paginate) paginate=true; logged+=("$1") ;;
	--silent) logged+=("$1") ;;
	-*) echo "fake gh: unexpected flag $1" >&2; exit 64 ;;
	*) path=$1 ;;
	esac
	shift
done
echo "$method $path${logged[*]+ ${logged[*]}}" >>"$FAKE_DIR/calls"
repo=repos/$GH_REPO
case "$method $path" in
"GET $repo/releases")
	if [ -e "$FAKE_DIR/list-fails" ]; then
		echo "gh: Server Error (HTTP 502)" >&2
		exit 1
	fi
	if [ "$paginate" = true ]; then
		cat "$FAKE_DIR/releases.json"
	else
		head -n 1 "$FAKE_DIR/releases.json"
	fi
	;;
"DELETE $repo/releases/"*) ;;
"POST $repo/releases")
	echo "{\"id\": 900, \"draft\": true, \"upload_url\": \"https://uploads.github.com/$repo/releases/900/assets{?name,label}\"}"
	;;
"GET $repo/releases/900/assets") cat "$FAKE_DIR/assets.json" ;;
"POST https://uploads.github.com/$repo/releases/900/assets")
	name=$(basename "$input")
	fails="$FAKE_DIR/upload-fails/$name"
	if [ -e "$fails" ] && [ "$(cat "$fails")" -gt 0 ]; then
		echo $(($(cat "$fails") - 1)) >"$fails"
		echo "gh: Bad Gateway (HTTP 502)" >&2
		exit 1
	fi
	state=uploaded size=$(wc -c <"$input")
	if [ -e "$FAKE_DIR/upload-state" ]; then state=$(cat "$FAKE_DIR/upload-state"); fi
	if [ -e "$FAKE_DIR/upload-size" ]; then size=$(cat "$FAKE_DIR/upload-size"); fi
	echo "{\"name\": \"$name\", \"state\": \"$state\", \"size\": $size}"
	;;
"PATCH $repo/releases/900")
	echo "{\"id\": 900, \"draft\": false, \"html_url\": \"https://github.com/$GH_REPO/releases/tag/$(cat "$FAKE_DIR/version")\"}"
	;;
*)
	echo "fake gh: no answer for $method $path" >&2
	exit 64
	;;
esac
`

// fakeGit stands in for git: it writes each call to FAKE_DIR/calls, and
// its Nth git ls-remote prints FAKE_DIR/ls-remote.N if that's there, or
// else FAKE_DIR/ls-remote.
const fakeGit = `#!/usr/bin/env bash
set -euo pipefail
echo "git $*" >>"$FAKE_DIR/calls"
if [ "${1:-}" != ls-remote ]; then
	echo "fake git: only git ls-remote is expected, not: git $*" >&2
	exit 64
fi
n=$(($(cat "$FAKE_DIR/ls-remote-calls" 2>/dev/null || echo 0) + 1))
echo "$n" >"$FAKE_DIR/ls-remote-calls"
if [ -e "$FAKE_DIR/ls-remote.$n" ]; then
	cat "$FAKE_DIR/ls-remote.$n"
else
	cat "$FAKE_DIR/ls-remote"
fi
`

const (
	// publishRepo is GH_REPO in TestPublishRelease.
	publishRepo = "owner/pairtime"
	// builtSHA is GITHUB_SHA, the commit the run built.
	builtSHA = "1111111111111111111111111111111111111111"
	// otherSHA is a commit the run didn't build.
	otherSHA = "2222222222222222222222222222222222222222"
	// tagObject is an annotated tag's own object id.
	tagObject = "3333333333333333333333333333333333333333"
)

// releaseFiles are the files each case publishes, in dist/, in the order
// the script gets them.
var releaseFiles = []struct{ name, body string }{
	{"pairtime_1.2.0_linux_amd64.tar.gz", "an archive\n"},
	{"checksums.txt", "sums\n"},
}

type publishCase struct {
	name     string
	version  string // v1.2.0 if empty
	releases string // the release list's answer, a page a line
	assets   string // the draft's asset list; [] if empty
	// lsRemote is what git ls-remote prints; empty means the version's tag
	// at builtSHA, unless noTag is set. lsRemote2 replaces it for the
	// second call.
	lsRemote, lsRemote2 string
	noTag               bool
	listFails           bool   // the release list answers HTTP 502
	uploadFails         int    // how many uploads of the first file fail
	uploadState         string // what an upload answers as its state
	uploadSize          string // what an upload answers as its size
	noFiles             bool   // the script gets only VERSION and NOTES
	wantCode            int
	wantCalls           []string // FAKE_DIR is TMP in them
	wantOut             string   // a substring of the script's output
}

// TestPublishRelease runs publish_release.sh, which release.yml's publish
// job runs for a v* tag, with a fake gh and a fake git first on PATH. No
// PR runs that job, so this is where its handling of the releases already
// there is checked: a leftover draft, however many, is deleted and never
// published; a published release stops it; a failed lookup never reads as
// no release; the release it publishes is the draft it made, by id; and
// it publishes only whole uploads of a tag that still names the commit.
func TestPublishRelease(t *testing.T) {
	for _, tool := range []string{"bash", "jq", "awk", "stat"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("publish_release.sh needs %s: %v", tool, err)
		}
	}
	// go test reuses a cached pass while the files the test process opened
	// are unchanged, and bash reads the script in a process of its own, so
	// open it here too, or a change to the script alone could pass unrun.
	if _, err := os.ReadFile("publish_release.sh"); err != nil {
		t.Fatal(err)
	}
	const (
		list      = "GET repos/" + publishRepo + "/releases --paginate"
		assets    = "GET repos/" + publishRepo + "/releases/900/assets --paginate"
		publish   = "PATCH repos/" + publishRepo + "/releases/900 -F draft=false"
		latest    = publish + " -f make_latest=true"
		firstFile = "pairtime_1.2.0_linux_amd64.tar.gz"
	)
	lsRemote := func(v string) string {
		return fmt.Sprintf("git ls-remote origin refs/tags/%s refs/tags/%s^{}", v, v)
	}
	create := func(v string, prerelease bool) string {
		return fmt.Sprintf("POST repos/%s/releases -f tag_name=%s -f name=%s -F body=@TMP/notes.md -F draft=true -F prerelease=%v", publishRepo, v, v, prerelease)
	}
	del := func(id int) string {
		return fmt.Sprintf("DELETE repos/%s/releases/%d --silent", publishRepo, id)
	}
	delAsset := func(id int) string {
		return fmt.Sprintf("DELETE repos/%s/releases/assets/%d --silent", publishRepo, id)
	}
	upload := func(name string) string {
		return fmt.Sprintf("POST https://uploads.github.com/repos/%s/releases/900/assets -H Content-Type: application/octet-stream -f name=%s --input TMP/dist/%s", publishRepo, name, name)
	}
	// made is the calls from the first tag check on, for a run that
	// publishes.
	made := func(v string, prerelease bool) []string {
		last := latest
		if prerelease {
			last = publish
		}
		return []string{lsRemote(v), create(v, prerelease), upload(releaseFiles[0].name), upload(releaseFiles[1].name), lsRemote(v), last}
	}
	tests := []publishCase{
		{
			name:      "with no release of the tag yet, it makes a draft, uploads each file into it and publishes that draft by its id as the latest release",
			releases:  `[{"id": 4, "tag_name": "v1.1.0", "draft": false}]`,
			wantCalls: slices.Concat([]string{list}, made("v1.2.0", false)),
			wantOut:   "Published v1.2.0: https://github.com/owner/pairtime/releases/tag/v1.2.0",
		},
		{
			name:      "a draft of the tag that a failed run left is deleted before the new draft is made, and the new draft's id is the one published",
			releases:  `[{"id": 7, "tag_name": "v1.2.0", "draft": true}, {"id": 4, "tag_name": "v1.1.0", "draft": false}]`,
			wantCalls: slices.Concat([]string{list, del(7)}, made("v1.2.0", false)),
			wantOut:   "Deleting draft release 7 of v1.2.0",
		},
		{
			name:      "every leftover draft of the tag is deleted, though a lookup by tag would find one at most",
			releases:  `[{"id": 8, "tag_name": "v1.2.0", "draft": true}, {"id": 7, "tag_name": "v1.2.0", "draft": true}]`,
			wantCalls: slices.Concat([]string{list, del(8), del(7)}, made("v1.2.0", false)),
		},
		{
			name:      "a published release of the tag is refused before any draft is deleted or made",
			releases:  `[{"id": 7, "tag_name": "v1.2.0", "draft": true}, {"id": 5, "tag_name": "v1.2.0", "draft": false}]`,
			wantCode:  1,
			wantCalls: []string{list},
			wantOut:   "release v1.2.0 is already published (release 5)",
		},
		{
			name:      "the whole list is read, so a draft on its second page is deleted too",
			releases:  "[{\"id\": 4, \"tag_name\": \"v1.1.0\", \"draft\": false}]\n[{\"id\": 2, \"tag_name\": \"v1.2.0\", \"draft\": true}]",
			wantCalls: slices.Concat([]string{list, del(2)}, made("v1.2.0", false)),
		},
		{
			name:      "the whole list is read, so a published release on its second page is refused",
			releases:  "[{\"id\": 4, \"tag_name\": \"v1.1.0\", \"draft\": false}]\n[{\"id\": 2, \"tag_name\": \"v1.2.0\", \"draft\": false}]",
			wantCode:  1,
			wantCalls: []string{list},
			wantOut:   "release v1.2.0 is already published (release 2)",
		},
		{
			name:      "drafts and releases of other tags are left alone, a tag that only starts the same among them",
			releases:  `[{"id": 6, "tag_name": "v1.2.0-rc.1", "draft": true}, {"id": 3, "tag_name": "v1.2", "draft": true}, {"id": 4, "tag_name": "v1.1.0", "draft": false}]`,
			wantCalls: slices.Concat([]string{list}, made("v1.2.0", false)),
		},
		{
			name:      "a version with a prerelease part is made a prerelease and published without becoming the latest release",
			version:   "v1.2.0-alpha.1",
			releases:  `[]`,
			wantCalls: slices.Concat([]string{list}, made("v1.2.0-alpha.1", true)),
			wantOut:   "Published v1.2.0-alpha.1: https://github.com/owner/pairtime/releases/tag/v1.2.0-alpha.1",
		},
		{
			name:      "an annotated tag counts by the commit it points at, not by the tag object",
			releases:  `[]`,
			lsRemote:  tagObject + "\trefs/tags/v1.2.0\n" + builtSHA + "\trefs/tags/v1.2.0^{}\n",
			wantCalls: slices.Concat([]string{list}, made("v1.2.0", false)),
		},
		{
			name:      "a tag that origin holds at another commit is refused before the draft is made",
			releases:  `[{"id": 7, "tag_name": "v1.2.0", "draft": true}]`,
			lsRemote:  otherSHA + "\trefs/tags/v1.2.0\n",
			wantCode:  1,
			wantCalls: []string{list, del(7), lsRemote("v1.2.0")},
			wantOut:   "refs/tags/v1.2.0 on origin names " + otherSHA + ", not " + builtSHA,
		},
		{
			name:      "an annotated tag moved to another commit is refused, though the tag object's line carries this run's commit id",
			releases:  `[]`,
			lsRemote:  builtSHA + "\trefs/tags/v1.2.0\n" + otherSHA + "\trefs/tags/v1.2.0^{}\n",
			wantCode:  1,
			wantCalls: []string{list, lsRemote("v1.2.0")},
			wantOut:   "names " + otherSHA,
		},
		{
			name:      "a tag origin doesn't hold is refused before the draft is made",
			releases:  `[]`,
			noTag:     true,
			wantCode:  1,
			wantCalls: []string{list, lsRemote("v1.2.0")},
			wantOut:   "refs/tags/v1.2.0 on origin names no commit",
		},
		{
			name:      "a tag moved while the files upload is refused before the draft is published",
			releases:  `[]`,
			lsRemote2: otherSHA + "\trefs/tags/v1.2.0\n",
			wantCode:  1,
			wantCalls: []string{list, lsRemote("v1.2.0"), create("v1.2.0", false), upload(releaseFiles[0].name), upload(releaseFiles[1].name), lsRemote("v1.2.0")},
			wantOut:   "names " + otherSHA,
		},
		{
			name:      "a release list that fails stops the script, rather than reading as a tag with no releases",
			listFails: true,
			wantCode:  1,
			wantCalls: []string{list},
		},
		{
			name:        "an upload answered in a state other than uploaded leaves the draft unpublished",
			releases:    `[]`,
			uploadState: "starter",
			wantCode:    1,
			wantCalls:   []string{list, lsRemote("v1.2.0"), create("v1.2.0", false), upload(releaseFiles[0].name)},
			wantOut:     `answered "starter 11", not "uploaded 11", so the draft stays unpublished`,
		},
		{
			name:       "an upload answered as uploaded at another size than the file's leaves the draft unpublished",
			releases:   `[]`,
			uploadSize: "3",
			wantCode:   1,
			wantCalls:  []string{list, lsRemote("v1.2.0"), create("v1.2.0", false), upload(releaseFiles[0].name)},
			wantOut:    `answered "uploaded 3", not "uploaded 11"`,
		},
		{
			name:        "a failed upload goes again, after the asset the failed try left under that name is deleted",
			releases:    `[]`,
			assets:      `[{"id": 30, "name": "checksums.txt.old", "state": "uploaded"}, {"id": 31, "name": "` + firstFile + `", "state": "starter"}]`,
			uploadFails: 1,
			wantCalls: slices.Concat([]string{list, lsRemote("v1.2.0"), create("v1.2.0", false), upload(firstFile), assets, delAsset(31)},
				[]string{upload(firstFile), upload(releaseFiles[1].name), lsRemote("v1.2.0"), latest}),
			wantOut: "Uploading " + firstFile + " failed, so it goes again",
		},
		{
			name:        "an upload that fails three times leaves the draft unpublished",
			releases:    `[]`,
			uploadFails: 3,
			wantCode:    1,
			wantCalls:   []string{list, lsRemote("v1.2.0"), create("v1.2.0", false), upload(firstFile), assets, upload(firstFile), assets, upload(firstFile)},
			wantOut:     "failed three times, so the draft stays unpublished",
		},
		{
			name:     "without a file to publish it is a usage error, and it calls nothing",
			noFiles:  true,
			wantCode: 2,
			wantOut:  "usage: publish_release.sh VERSION NOTES FILE...",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			version := tt.version
			if version == "" {
				version = "v1.2.0"
			}
			lsRemoteOut := tt.lsRemote
			if lsRemoteOut == "" && !tt.noTag {
				lsRemoteOut = builtSHA + "\trefs/tags/" + version + "\n"
			}
			assetList := tt.assets
			if assetList == "" {
				assetList = "[]"
			}
			dir := t.TempDir()
			for _, d := range []string{"bin", "dist", "upload-fails"} {
				if err := os.Mkdir(filepath.Join(dir, d), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			files := map[string]string{
				"bin/gh":        fakeGH,
				"bin/git":       fakeGit,
				"releases.json": tt.releases + "\n",
				"assets.json":   assetList + "\n",
				"ls-remote":     lsRemoteOut,
				"version":       version,
				"notes.md":      "manifest.json is signed with release key `0123456789abcdef`.\n",
			}
			if tt.lsRemote2 != "" {
				files["ls-remote.2"] = tt.lsRemote2
			}
			if tt.listFails {
				files["list-fails"] = ""
			}
			if tt.uploadFails > 0 {
				files["upload-fails/"+firstFile] = fmt.Sprintf("%d\n", tt.uploadFails)
			}
			if tt.uploadState != "" {
				files["upload-state"] = tt.uploadState
			}
			if tt.uploadSize != "" {
				files["upload-size"] = tt.uploadSize
			}
			for _, f := range releaseFiles {
				files["dist/"+f.name] = f.body
			}
			for name, body := range files {
				mode := fs.FileMode(0o644)
				if strings.HasPrefix(name, "bin/") {
					mode = 0o755
				}
				if err := os.WriteFile(filepath.Join(dir, filepath.FromSlash(name)), []byte(body), mode); err != nil {
					t.Fatal(err)
				}
			}
			args := []string{"publish_release.sh", version, filepath.Join(dir, "notes.md")}
			if !tt.noFiles {
				for _, f := range releaseFiles {
					args = append(args, filepath.Join(dir, "dist", f.name))
				}
			}
			summary := filepath.Join(dir, "summary.md")
			cmd := exec.Command("bash", args...)
			// The last value of a variable wins, so these override what the
			// CI job itself has, GITHUB_SHA and GITHUB_STEP_SUMMARY among it.
			cmd.Env = append(os.Environ(),
				"PATH="+filepath.Join(dir, "bin")+string(os.PathListSeparator)+os.Getenv("PATH"),
				"FAKE_DIR="+dir,
				"GH_REPO="+publishRepo,
				"GITHUB_SHA="+builtSHA,
				"GITHUB_STEP_SUMMARY="+summary,
				"PUBLISH_RETRY_SECONDS=0",
			)
			var out bytes.Buffer
			cmd.Stdout = &out
			cmd.Stderr = &out
			code := 0
			if err := cmd.Run(); err != nil {
				var exit *exec.ExitError
				if !errors.As(err, &exit) {
					t.Fatal(err)
				}
				code = exit.ExitCode()
			}
			if code != tt.wantCode {
				t.Errorf("exit code = %d, want %d\noutput:\n%s", code, tt.wantCode, out.String())
			}
			if !strings.Contains(out.String(), tt.wantOut) {
				t.Errorf("output lacks %q:\n%s", tt.wantOut, out.String())
			}
			var calls []string
			b, err := os.ReadFile(filepath.Join(dir, "calls"))
			if err != nil && !errors.Is(err, fs.ErrNotExist) {
				t.Fatal(err)
			}
			if s := strings.TrimSuffix(strings.ReplaceAll(string(b), dir, "TMP"), "\n"); s != "" {
				calls = strings.Split(s, "\n")
			}
			if !slices.Equal(calls, tt.wantCalls) {
				t.Errorf("calls:\n  %s\nwant:\n  %s\noutput:\n%s", strings.Join(calls, "\n  "), strings.Join(tt.wantCalls, "\n  "), out.String())
			}
			if tt.wantCode == 0 {
				got, err := os.ReadFile(summary)
				if err != nil {
					t.Fatal(err)
				}
				want := fmt.Sprintf("Published %s: https://github.com/%s/releases/tag/%s\n", version, publishRepo, version)
				if string(got) != want {
					t.Errorf("GITHUB_STEP_SUMMARY = %q, want %q", got, want)
				}
			}
		})
	}
}
