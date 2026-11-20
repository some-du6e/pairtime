// SPDX-License-Identifier: AGPL-3.0-or-later

package ledger

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// The vectors format of spec section 11.3. vectors.json is read with it,
// and TestReferenceCheck writes it for tools/ledger_ref.py check.

const vectorsFormat = "pairtime-ledger-vectors-v1"

type vectorsFile struct {
	Format    string      `json:"format"`
	Spec      string      `json:"spec,omitempty"`
	Generator string      `json:"generator,omitempty"`
	Note      string      `json:"note,omitempty"`
	Keys      []keysVec   `json:"keys,omitempty"`
	Digests   []digestVec `json:"digests,omitempty"`
	Chains    []chainVec  `json:"chains,omitempty"`
	Days      []dayVec    `json:"days,omitempty"`
}

type keysVec struct {
	KDev   hexBytes `json:"k_dev"`
	PRK    hexBytes `json:"prk,omitempty"`
	KLeaf  hexBytes `json:"k_leaf"`
	KTok   hexBytes `json:"k_tok"`
	KFeat  hexBytes `json:"k_feat"`
	KVault hexBytes `json:"k_vault"`
}

type digestVec struct {
	ProjectKey *string  `json:"project_key,omitempty"`
	Project    hexBytes `json:"project,omitempty"`
	SessionKey *string  `json:"session_key,omitempty"`
	Session    hexBytes `json:"session,omitempty"`
}

type chainVec struct {
	DeviceID string     `json:"device_id"`
	KDev     hexBytes   `json:"k_dev"`
	Batches  []batchVec `json:"batches"`
}

type batchVec struct {
	B          uint64      `json:"b"`
	N          uint64      `json:"n"`
	ReceivedAt *uint64     `json:"received_at,omitempty"`
	Prev       hexBytes    `json:"prev"`
	Root       hexBytes    `json:"root"`
	Head       hexBytes    `json:"head"`
	ReceiptMsg hexBytes    `json:"receipt_msg,omitempty"`
	Records    []recordVec `json:"records"`
	Proofs     []proofVec  `json:"proofs,omitempty"`
}

type recordVec struct {
	Seq        uint64   `json:"seq"`
	RType      uint8    `json:"rtype"`
	ProjectKey string   `json:"project_key"`
	SessionKey string   `json:"session_key"`
	Body       hexBytes `json:"body"`
	Nonce      hexBytes `json:"nonce"`
	Entry      hexBytes `json:"entry"`
}

type proofVec struct {
	Index uint64     `json:"index"`
	Path  []hexBytes `json:"path"`
}

type dayVec struct {
	Day       uint64     `json:"day"`
	Date      string     `json:"date,omitempty"`
	Prev      hexBytes   `json:"prev"`
	Count     uint64     `json:"count"`
	Root      hexBytes   `json:"root"`
	Head      hexBytes   `json:"head"`
	SignedMsg hexBytes   `json:"signed_msg"`
	Rows      []rowVec   `json:"rows"`
	Proofs    []proofVec `json:"proofs,omitempty"`
}

type rowVec struct {
	DeviceID   string   `json:"device_id"`
	B          uint64   `json:"b"`
	Head       hexBytes `json:"head"`
	ReceivedAt uint64   `json:"received_at"`
	Leaf       hexBytes `json:"leaf"`
}

// hexBytes is a byte string in the vectors format: lowercase hex.
type hexBytes []byte

func (h hexBytes) MarshalText() ([]byte, error) { return hex.AppendEncode(nil, h), nil }

func (h *hexBytes) UnmarshalText(text []byte) error {
	if len(text)%2 != 0 || !isLowerHex(text) {
		return fmt.Errorf("want lowercase hex, got %d characters that aren't", len(text))
	}
	b, err := hex.DecodeString(string(text))
	if err != nil {
		return err
	}
	*h = b
	return nil
}

func hashBytes(h Hash) hexBytes { return hexBytes(h[:]) }

func hashesBytes(hs []Hash) []hexBytes {
	out := make([]hexBytes, len(hs))
	for i, h := range hs {
		out[i] = hashBytes(h)
	}
	return out
}

// dateOf returns day d as YYYY-MM-DD.
func dateOf(d uint64) string {
	return time.UnixMilli(int64(d * DayMS)).UTC().Format(time.DateOnly)
}

// readVectors reads testdata/ledger/vectors.json, refusing any member the
// format doesn't define.
func readVectors(t *testing.T) vectorsFile {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "testdata", "ledger", "vectors.json"))
	if err != nil {
		t.Fatal(err)
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var f vectorsFile
	if err := dec.Decode(&f); err != nil {
		t.Fatalf("vectors.json: %v", err)
	}
	if f.Format != vectorsFormat {
		t.Fatalf("vectors.json: format is %q, want %q", f.Format, vectorsFormat)
	}
	return f
}

func sameBytes(t *testing.T, what string, got, want []byte) {
	t.Helper()
	if !bytes.Equal(got, want) {
		t.Errorf("%s: pkg/ledger computes %x, the vectors have %x", what, got, want)
	}
}

func TestVectorsHaveWhatSpec11Lists(t *testing.T) {
	f := readVectors(t)
	records := 0
	batches := 0
	for _, c := range f.Chains {
		batches += len(c.Batches)
		for _, b := range c.Batches {
			records += len(b.Records)
		}
	}
	// These counts come from testdata/ledger/README.md. If they stop
	// matching, a vectors file with less in it would pass the tests below
	// without notice.
	cases := []struct {
		name      string
		got, want int
	}{
		{"the vectors hold four key sets", len(f.Keys), 4},
		{"the vectors hold nine digests", len(f.Digests), 9},
		{"the vectors hold two chains", len(f.Chains), 2},
		{"the chains hold 54 batches", batches, 54},
		{"the batches hold 1,529 records", records, 1529},
		{"the vectors hold three days", len(f.Days), 3},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if c.got != c.want {
				t.Errorf("got %d, want %d", c.got, c.want)
			}
		})
	}
}

func TestVectorKeys(t *testing.T) {
	for i, kv := range readVectors(t).Keys {
		t.Run(fmt.Sprintf("keys[%d]: HKDF-SHA256 with a zero salt derives each key from K_dev", i), func(t *testing.T) {
			k, err := DeriveKeys(kv.KDev)
			if err != nil {
				t.Fatal(err)
			}
			if kv.PRK != nil {
				prk, err := extract(kv.KDev, make([]byte, sha256.Size))
				if err != nil {
					t.Fatal(err)
				}
				sameBytes(t, "prk", prk, kv.PRK)
			}
			leaf, tok, feat, vault := k.LeafKey(), k.TokenKey(), k.FeatKey(), k.VaultKey()
			sameBytes(t, "k_leaf", leaf[:], kv.KLeaf)
			sameBytes(t, "k_tok", tok[:], kv.KTok)
			sameBytes(t, "k_feat", feat[:], kv.KFeat)
			sameBytes(t, "k_vault", vault[:], kv.KVault)
		})
	}
}

func TestVectorDigests(t *testing.T) {
	for i, dv := range readVectors(t).Digests {
		switch {
		case dv.ProjectKey != nil:
			t.Run(fmt.Sprintf("digests[%d]: project is the first 16 bytes of SHA-256(project_key), zero for none", i), func(t *testing.T) {
				d, err := ProjectDigest(*dv.ProjectKey)
				if err != nil {
					t.Fatal(err)
				}
				sameBytes(t, "project", d[:], dv.Project)
			})
		case dv.SessionKey != nil:
			t.Run(fmt.Sprintf("digests[%d]: session is the first 8 bytes of SHA-256(session_key), zero for none", i), func(t *testing.T) {
				d, err := SessionDigest(*dv.SessionKey)
				if err != nil {
					t.Fatal(err)
				}
				sameBytes(t, "session", d[:], dv.Session)
			})
		default:
			t.Errorf("digests[%d] has neither a project_key nor a session_key", i)
		}
	}
}

func TestVectorChains(t *testing.T) {
	server := testServerKey()
	for i, cv := range readVectors(t).Chains {
		t.Run(fmt.Sprintf("chains[%d]: every nonce, entry, root, head, receipt message and proof reproduces, and the chain verifies", i), func(t *testing.T) {
			k, err := DeriveKeys(cv.KDev)
			if err != nil {
				t.Fatal(err)
			}
			chain := Chain{DeviceID: cv.DeviceID}
			prev := zeroHash
			for j, bv := range cv.Batches {
				where := fmt.Sprintf("batches[%d]", j)
				row, entries := checkBatchVector(t, where, k, cv.DeviceID, prev, bv)
				if bv.ReceivedAt != nil {
					rc, err := SignReceipt(server, cv.DeviceID, row.B, row.Head, *bv.ReceivedAt)
					if err != nil {
						t.Fatal(err)
					}
					// The reference builds M_b; signing it here and verifying
					// shows the signer signs exactly those bytes.
					if !verify(testServerPub(), bv.ReceiptMsg, rc.Signature) {
						t.Errorf("%s: the receipt signature doesn't verify over the vectors' receipt_msg", where)
					}
					row.Receipt = &rc
				}
				chain.Batches = append(chain.Batches, Batch{Row: row, Entries: entries})
				prev = row.Head
			}
			// The bodies are stand-ins, not events, so the chain verifies
			// with every body hidden, as a server holds a revealed batch.
			if err := chain.Verify(Options{ServerKey: serverKeyAlways(testServerPub())}); err != nil {
				t.Errorf("the vectors' chain doesn't verify: %v", err)
			}
		})
	}
}

// checkBatchVector recomputes every value of one batch of the vectors from
// its inputs, and returns the batch's row and entries as pkg/ledger
// computes them.
func checkBatchVector(t *testing.T, where string, k *Keys, deviceID string, prev Hash, bv batchVec) (BatchRow, []Entry) {
	t.Helper()
	sameBytes(t, where+".prev", bv.Prev, prev[:])
	entries := make([]Entry, len(bv.Records))
	for i, rv := range bv.Records {
		rp := fmt.Sprintf("%s.records[%d]", where, i)
		n := k.Nonce(rv.Seq)
		sameBytes(t, rp+".nonce", n[:], rv.Nonce)
		if err := k.CheckNonce(rv.Seq, rv.Body); err != nil {
			t.Errorf("%s.body: %v", rp, err)
		}
		// Go's body writer, given the stand-in record, writes the bytes
		// the reference wrote: the envelope is the same, member for member.
		standIn := fmt.Sprintf(`{"stand_in":%q,"seq":%d}`, RType(rv.RType).String(), rv.Seq)
		body, err := Body{N: n, E: json.RawMessage(standIn)}.Encode()
		if err != nil {
			t.Fatal(err)
		}
		sameBytes(t, rp+".body", body, rv.Body)
		h, err := NewHeader(rv.Seq, RType(rv.RType), rv.ProjectKey, rv.SessionKey)
		if err != nil {
			t.Errorf("%s: %v", rp, err)
			continue
		}
		entries[i] = NewEntry(h, rv.Body)
		sameBytes(t, rp+".entry", entries[i][:], rv.Entry)
	}
	row, err := Seal(prev, bv.B, entries)
	if err != nil {
		t.Fatalf("%s: %v", where, err)
	}
	if row.N != bv.N {
		t.Errorf("%s.n: Seal counts %d, the vectors have %d", where, row.N, bv.N)
	}
	sameBytes(t, where+".root", row.Root[:], bv.Root)
	sameBytes(t, where+".head", row.Head[:], bv.Head)
	if bv.ReceivedAt != nil {
		m, err := ReceiptMessage(deviceID, bv.B, row.Head, *bv.ReceivedAt)
		if err != nil {
			t.Fatal(err)
		}
		sameBytes(t, where+".receipt_msg", m, bv.ReceiptMsg)
	}
	leaves := EntryLeaves(entries)
	for _, pv := range bv.Proofs {
		checkProofVector(t, fmt.Sprintf("%s.proofs[%d]", where, pv.Index), leaves, row.Root, pv)
	}
	return row, entries
}

// checkProofVector regenerates a proof of the vectors and verifies it.
func checkProofVector(t *testing.T, where string, leaves []Hash, root Hash, pv proofVec) {
	t.Helper()
	if pv.Index >= uint64(len(leaves)) {
		t.Errorf("%s: index %d isn't in a tree of %d leaves", where, pv.Index, len(leaves))
		return
	}
	path, err := InclusionProof(int(pv.Index), leaves)
	if err != nil {
		t.Fatal(err)
	}
	if len(path) != len(pv.Path) {
		t.Errorf("%s: pkg/ledger's proof has %d hashes, the vectors' %d", where, len(path), len(pv.Path))
		return
	}
	given := make([]Hash, len(pv.Path))
	for i, p := range pv.Path {
		sameBytes(t, fmt.Sprintf("%s.path[%d]", where, i), path[i][:], p)
		if given[i], err = HashOf(p); err != nil {
			t.Fatal(err)
		}
	}
	if err := VerifyInclusion(pv.Index, uint64(len(leaves)), leaves[pv.Index], given, root); err != nil {
		t.Errorf("%s: %v", where, err)
	}
}

func TestVectorDays(t *testing.T) {
	server := testServerKey()
	var published []PublishedDay
	days := readVectors(t).Days
	for i, dv := range days {
		t.Run(fmt.Sprintf("days[%d]: the leaves, their order, T_d, count_d, G_d, D_d and every proof reproduce", i), func(t *testing.T) {
			if dv.Date != "" && dateOf(dv.Day) != dv.Date {
				t.Errorf("day %d is %s, the vectors say %s", dv.Day, dateOf(dv.Day), dv.Date)
			}
			prev, err := HashOf(dv.Prev)
			if err != nil {
				t.Fatal(err)
			}
			rows := make([]DayRow, len(dv.Rows))
			for j, rv := range dv.Rows {
				head, err := HashOf(rv.Head)
				if err != nil {
					t.Fatal(err)
				}
				rows[j] = DayRow{DeviceID: rv.DeviceID, B: rv.B, Head: head, ReceivedAt: rv.ReceivedAt}
			}
			// BuildDay sorts what it is given, so hand it the rows reversed:
			// it has to find the vectors' order itself.
			reversed := make([]DayRow, len(rows))
			for j, r := range rows {
				reversed[len(rows)-1-j] = r
			}
			day, err := BuildDay(dv.Day, prev, reversed)
			if err != nil {
				t.Fatal(err)
			}
			for j, r := range day.Rows {
				if r != rows[j] {
					t.Errorf("rows[%d]: pkg/ledger sorts batch %d of %s here, the vectors batch %d of %s", j, r.B, r.DeviceID, rows[j].B, rows[j].DeviceID)
				}
				leaf, err := r.Leaf()
				if err != nil {
					t.Fatal(err)
				}
				sameBytes(t, fmt.Sprintf("rows[%d].leaf", j), leaf, dv.Rows[j].Leaf)
			}
			if day.Count() != dv.Count {
				t.Errorf("count: pkg/ledger counts %d, the vectors %d", day.Count(), dv.Count)
			}
			sameBytes(t, "root", day.Root[:], dv.Root)
			sameBytes(t, "head", day.Head[:], dv.Head)
			sameBytes(t, "signed_msg", day.Message(), dv.SignedMsg)
			for _, pv := range dv.Proofs {
				checkProofVector(t, fmt.Sprintf("proofs[%d]", pv.Index), day.leaves, day.Root, pv)
			}
			p, err := day.Publish(server)
			if err != nil {
				t.Fatal(err)
			}
			if err := p.VerifyRows(rows); err != nil {
				t.Errorf("the vectors' rows don't verify: %v", err)
			}
			published = append(published, p)
		})
	}
	t.Run("the vectors' days chain with no gaps, and their signed heads verify", func(t *testing.T) {
		if len(published) != len(days) {
			t.Fatalf("%d of %d days built", len(published), len(days))
		}
		if err := VerifyDays(serverKeyAlways(testServerPub()), published); err != nil {
			t.Error(err)
		}
	})
}
