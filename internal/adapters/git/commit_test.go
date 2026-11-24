// SPDX-License-Identifier: AGPL-3.0-or-later

package git

import (
	"encoding/json"
	"testing"

	"github.com/some-du6e/pairtime/pkg/event"
)

// TestCommitRecordBytes checks a commit record's bytes (Commit.MarshalJSON):
// the exported fields in their order, as encoding/json writes the struct,
// so every record whose shortstat was read keeps the bytes it had before
// the record had a MarshalJSON of its own, and no files_changed, insertions
// or deletions when the shortstat couldn't be read (CommitLocal.NoStat), as
// ingest-v1 2.3.3 says. TestCommitJSON pins the fields to DESIGN 4.3's.
func TestCommitRecordBytes(t *testing.T) {
	// plain is Commit without its MarshalJSON, as encoding/json writes the
	// struct's fields, so a field the record leaves out shows.
	type plain Commit
	base := Commit{SHA: "1a2b3c4d5e6f7a8b9c0d1e2f3a4b5c6d7e8f9a0b", Parents: []string{"0f1e2d3c4b5a69788796a5b4c3d2e1f0a9b8c7d6"},
		Project: "d0123456789abcdef0123456789abcdef", ObservedAt: 1795193310281, AuthorTime: 1795193300, CommitterTime: 1795193305,
		FilesChanged: 2, Insertions: 10, Deletions: 3, Rewrites: []Rewrite{{OldSHA: "2b3c4d5e6f7a8b9c0d1e2f3a4b5c6d7e8f9a0b1c", NewSHA: "1a2b3c4d5e6f7a8b9c0d1e2f3a4b5c6d7e8f9a0b"}},
		Actor: event.ActorAgent, AgentTrailer: "claude"}
	cases := []struct {
		name string
		edit func(c *Commit)
		// want is the JSON of the record, "" for plain's.
		want string
	}{
		{"a commit whose shortstat was read writes its fields as the struct does", func(c *Commit) {}, ""},
		{"a root commit's parents stay null", func(c *Commit) { c.Parents = nil }, ""},
		{"a commit that changed nothing keeps its 0 counts", func(c *Commit) { c.FilesChanged, c.Insertions, c.Deletions = 0, 0, 0 }, ""},
		{"a commit whose shortstat couldn't be read leaves its counts out", func(c *Commit) {
			c.FilesChanged, c.Insertions, c.Deletions, c.Local.NoStat = 0, 0, 0, true
		}, `{"sha":"1a2b3c4d5e6f7a8b9c0d1e2f3a4b5c6d7e8f9a0b","parents":["0f1e2d3c4b5a69788796a5b4c3d2e1f0a9b8c7d6"],` +
			`"project":"d0123456789abcdef0123456789abcdef","observed_at":1795193310281,"author_time":1795193300,"committer_time":1795193305,` +
			`"rewrites":[{"old_sha":"2b3c4d5e6f7a8b9c0d1e2f3a4b5c6d7e8f9a0b1c","new_sha":"1a2b3c4d5e6f7a8b9c0d1e2f3a4b5c6d7e8f9a0b"}],` +
			`"actor":"agent","agent_trailer":"claude"}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			commit := base
			commit.Parents = append([]string(nil), base.Parents...)
			c.edit(&commit)
			got, err := json.Marshal(commit)
			if err != nil {
				t.Fatal(err)
			}
			want := c.want
			if want == "" {
				b, err := json.Marshal(plain(commit))
				if err != nil {
					t.Fatal(err)
				}
				want = string(b)
			}
			if string(got) != want {
				t.Errorf("got %s\nwant %s", got, want)
			}
		})
	}
}
