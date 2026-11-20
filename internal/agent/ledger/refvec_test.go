// SPDX-License-Identifier: AGPL-3.0-or-later

package ledger

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/some-du6e/pairtime/pkg/event"
	"github.com/some-du6e/pairtime/pkg/ledger"
)

// The vectors format of ledger-v1 11.3, as far as the writer's checks use
// it: chains of batches as the writer stored them, and the commitments
// and feature maps of their records.
type vecFile struct {
	Format      string       `json:"format"`
	Note        string       `json:"note,omitempty"`
	Chains      []vecChain   `json:"chains,omitempty"`
	Commitments []vecCommit  `json:"commitments,omitempty"`
	Features    []vecFeature `json:"features,omitempty"`
}

type hexBytes []byte

func (h hexBytes) MarshalText() ([]byte, error) { return hex.AppendEncode(nil, h), nil }

type vecChain struct {
	DeviceID string     `json:"device_id"`
	KDev     hexBytes   `json:"k_dev"`
	Batches  []vecBatch `json:"batches"`
}

type vecBatch struct {
	B       uint64      `json:"b"`
	N       uint64      `json:"n"`
	Prev    ledger.Hash `json:"prev"`
	Root    ledger.Hash `json:"root"`
	Head    ledger.Hash `json:"head"`
	Records []vecRecord `json:"records"`
}

type vecRecord struct {
	Seq        uint64      `json:"seq"`
	RType      uint8       `json:"rtype"`
	ProjectKey string      `json:"project_key"`
	SessionKey string      `json:"session_key"`
	Body       hexBytes    `json:"body"`
	Nonce      ledger.Hash `json:"nonce"`
	Entry      hexBytes    `json:"entry"`
}

type vecCommit struct {
	KDev  hexBytes    `json:"k_dev"`
	Seq   uint64      `json:"seq"`
	RType uint8       `json:"rtype"`
	Text  hexBytes    `json:"text"`
	N     uint64      `json:"n"`
	Root  ledger.Hash `json:"root"`
	Raw   hexBytes    `json:"raw,omitempty"`
}

type vecFeature struct {
	KDev        hexBytes        `json:"k_dev"`
	RType       uint8           `json:"rtype"`
	F           json.RawMessage `json:"f"`
	PromptClass string          `json:"prompt_class,omitempty"`
	Hunks       *[]vecHunk      `json:"hunks,omitempty"`
	Command     hexBytes        `json:"command,omitempty"`
}

type vecHunk struct {
	Path     string `json:"path"`
	OldStart int    `json:"old_start"`
	OldLines int    `json:"old_lines"`
	NewStart int    `json:"new_start"`
	NewLines int    `json:"new_lines"`
}

const vectorsFormat = "pairtime-ledger-vectors-v1"

// recordKeys returns the project_key and session_key whose digests a
// record's header carries, from its body (ledger-v1 4.3).
func recordKeys(t testing.TB, r ledger.Record) (project, session string) {
	t.Helper()
	var body struct {
		E struct {
			ProjectKey string `json:"project_key"`
			SessionKey string `json:"session_key"`
		} `json:"e"`
	}
	if err := json.Unmarshal(r.Body, &body); err != nil {
		t.Fatalf("record %d: %v", r.Header.Seq(), err)
	}
	if rt := r.Header.RType(); rt == ledger.RTypeCheckpoint || rt == ledger.RTypeUploadFailure {
		body.E.SessionKey = event.NoSession
	}
	return body.E.ProjectKey, body.E.SessionKey
}

// chainVec writes batches, a run of one chain from batch batches[0].Row.B
// on, whose first batch follows prev, in the vectors format.
func chainVec(t testing.TB, deviceID string, kDev []byte, prev ledger.Hash, batches []Batch) vecChain {
	t.Helper()
	keys := keysOf(t, kDev)
	vc := vecChain{DeviceID: deviceID, KDev: kDev}
	for _, b := range batches {
		vb := vecBatch{B: b.Row.B, N: b.Row.N, Prev: prev, Root: b.Row.Root, Head: b.Row.Head}
		for _, r := range b.Records {
			project, session := recordKeys(t, r)
			e := r.Entry()
			vb.Records = append(vb.Records, vecRecord{Seq: r.Header.Seq(), RType: uint8(r.Header.RType()),
				ProjectKey: project, SessionKey: session, Body: r.Body, Nonce: keys.Nonce(r.Header.Seq()), Entry: e[:]})
		}
		vc.Batches = append(vc.Batches, vb)
		prev = b.Row.Head
	}
	return vc
}

// pythonPath finds python 3: python3 where Unix installs it, and python
// on Windows, whose runners don't have python3.
func pythonPath(t testing.TB) string {
	t.Helper()
	name := "python3"
	if runtime.GOOS == "windows" {
		name = "python"
	}
	p, err := exec.LookPath(name)
	if err != nil {
		t.Fatalf("%s is missing, and this test runs tools/ledger_ref.py with it: %v", name, err)
	}
	return p
}

// refCheck has tools/ledger_ref.py check the file doc, and returns its
// exit code and output.
func refCheck(t testing.TB, python string, doc vecFile) (int, string) {
	t.Helper()
	data, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	name := filepath.Join(t.TempDir(), "ledger.json")
	if err := os.WriteFile(name, data, 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(python, "-I", filepath.Join("..", "..", "..", "tools", "ledger_ref.py"), "check", name)
	out, err := cmd.CombinedOutput()
	if err != nil {
		var exit *exec.ExitError
		if !errors.As(err, &exit) {
			t.Fatalf("running tools/ledger_ref.py: %v", err)
		}
		return exit.ExitCode(), string(out)
	}
	return 0, string(out)
}

// contentVecs returns the commitments and feature maps of the records in
// batches, from the inputs they were written from, by sequence number. A
// prompt or track label that commits its text gets its commitment, and not
// its feature map, whose check would need the per-shingle values (D-cc8);
// pkg/ledger's own reference test checks those.
func contentVecs(t testing.TB, kDev []byte, batches []Batch, inputs map[uint64]Input) ([]vecCommit, []vecFeature) {
	t.Helper()
	var commits []vecCommit
	var feats []vecFeature
	for _, r := range records(batches) {
		in, ok := inputs[r.Header.Seq()]
		if !ok {
			continue
		}
		b, err := ledger.ParseBody(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		rtype := uint8(r.Header.RType())
		if b.C != nil {
			vc := vecCommit{KDev: kDev, Seq: r.Header.Seq(), RType: rtype, Text: in.Text, N: b.C.N, Root: b.C.R}
			// An agent message commits its first 16 KiB; prompts and
			// labels commit in full (ledger-v1 10.1).
			if _, final := in.Event.Data.(*event.AgentOutput); final && len(in.Text) > ledger.MessageMax {
				vc.Text, vc.Raw = ledger.CommittedMessage(in.Text), in.Text
			}
			commits = append(commits, vc)
		}
		if b.F == nil {
			continue
		}
		vf := vecFeature{KDev: kDev, RType: rtype, F: b.F}
		switch in.Event.Data.(type) {
		case *event.PromptSubmit:
			if b.C != nil {
				continue
			}
			f, _, err := b.Features(r.Header.RType())
			if err != nil {
				t.Fatal(err)
			}
			vf.PromptClass = string(f.PC)
		case *event.CLITrack:
			continue
		case *event.AgentTool:
			hunks := []vecHunk{}
			for _, e := range in.Edits {
				hunks = append(hunks, vecHunk{Path: e.Path, OldStart: e.OldStart, OldLines: e.OldLines, NewStart: e.NewStart, NewLines: e.NewLines})
			}
			vf.Hunks = &hunks
			vf.Command = []byte(in.Command)
		case *event.ShellCommand:
			vf.Command = []byte(in.Command)
		}
		feats = append(feats, vf)
	}
	return commits, feats
}
