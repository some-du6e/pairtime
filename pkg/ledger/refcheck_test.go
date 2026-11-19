// SPDX-License-Identifier: AGPL-3.0-or-later

package ledger

import (
	"bytes"
	"encoding/json"
	"errors"
	"maps"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

// TestReferenceCheck writes 1,000 random batches with this package, in the
// vectors format, and has tools/ledger_ref.py, which shares no code with
// it, recompute every value in them (WP-LG2). It needs python3 and fails
// without it.
func TestReferenceCheck(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Fatalf("python3 is missing, and this test runs tools/ledger_ref.py with it: %v", err)
	}
	doc := randomLedgers(t)
	batches := 0
	for _, c := range doc.Chains {
		batches += len(c.Batches)
	}
	if batches != 1000 {
		t.Fatalf("wrote %d batches, want 1,000", batches)
	}
	// A slice of the same file with one byte of one entry changed, to
	// show the check really reads the file this test writes.
	small := vectorsFile{Format: vectorsFormat, Chains: []chainVec{doc.Chains[0]}}
	small.Chains[0].Batches = small.Chains[0].Batches[:3]
	tampered := deepCopy(t, small)
	e := tampered.Chains[0].Batches[1].Records[0].Entry
	e[len(e)-1] ^= 0x01

	cases := []struct {
		name string
		doc  vectorsFile
		want int
	}{
		{"1,000 random batches from four devices, with receipt messages, proofs and day trees, pass tools/ledger_ref.py check", doc, 0},
		{"tools/ledger_ref.py check fails the first batches of the same file with one entry byte changed", tampered, 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "ledger.json")
			data, err := json.Marshal(c.doc)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, data, 0o600); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command(python, "-I", filepath.Join("..", "..", "tools", "ledger_ref.py"), "check", path)
			out, err := cmd.CombinedOutput()
			code := 0
			if err != nil {
				var exit *exec.ExitError
				if !errors.As(err, &exit) {
					t.Fatalf("running tools/ledger_ref.py: %v", err)
				}
				code = exit.ExitCode()
			}
			if code != c.want {
				t.Fatalf("tools/ledger_ref.py check exited %d, want %d:\n%s", code, c.want, out)
			}
			lines := bytes.Split(bytes.TrimSpace(out), []byte("\n"))
			t.Logf("%d bytes checked: %s", len(data), lines[len(lines)-1])
		})
	}
}

func deepCopy(t *testing.T, v vectorsFile) vectorsFile {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var out vectorsFile
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

// randomDevice is one device in randomLedgers' file.
type randomDevice struct {
	id      string
	batches int
	// start is about when the device's first batch reaches the server.
	start string
}

// randomLedgers writes 1,000 random batches over four devices in the
// vectors format: every record type, each batch's receipt message and
// proofs, and the day tree of every day the batches reached the server.
// It checks each chain and the days with this package's own verifier on
// the way. The seed is fixed, so a failure reproduces.
func randomLedgers(t *testing.T) vectorsFile {
	t.Helper()
	rng := rand.New(rand.NewPCG(20261119, 1000))
	server := testServerKey()
	devices := []randomDevice{
		{"d_" + randHex(rng, 8), 400, "2026-11-19T20:00:00Z"},
		{randKey(rng, MaxKeyLen), 300, "2026-11-19T21:30:00Z"},
		{"x", 200, "2026-11-19T22:45:00Z"},
		{"Device-" + randHex(rng, 3), 100, "2026-11-19T23:50:00Z"},
	}
	codes := allRTypes()
	doc := vectorsFile{Format: vectorsFormat, Note: "Written by pkg/ledger's TestReferenceCheck from a fixed seed."}
	var dayRows []DayRow
	for _, dev := range devices {
		kDev := make([]byte, KeySize)
		for i := range kDev {
			kDev[i] = byte(rng.Uint32())
		}
		k, err := DeriveKeys(kDev)
		if err != nil {
			t.Fatal(err)
		}
		prk, err := extract(kDev, make([]byte, HashSize))
		if err != nil {
			t.Fatal(err)
		}
		leafKey, tokKey, featKey, vaultKey := k.LeafKey(), k.TokenKey(), k.FeatKey(), k.VaultKey()
		doc.Keys = append(doc.Keys, keysVec{KDev: kDev, PRK: prk, KLeaf: leafKey[:], KTok: tokKey[:], KFeat: featKey[:], KVault: vaultKey[:]})

		var projects, sessions []string
		for range 3 {
			p := "p_" + randHex(rng, 6)
			d, err := ProjectDigest(p)
			if err != nil {
				t.Fatal(err)
			}
			doc.Digests = append(doc.Digests, digestVec{ProjectKey: &p, Project: d[:]})
			projects = append(projects, p)
		}
		for range 4 {
			s := "s_" + randHex(rng, 6)
			d, err := SessionDigest(s)
			if err != nil {
				t.Fatal(err)
			}
			doc.Digests = append(doc.Digests, digestVec{SessionKey: &s, Session: d[:]})
			sessions = append(sessions, s)
		}
		orNone := append(slices.Clone(projects), "")

		start, err := time.Parse(time.RFC3339, dev.start)
		if err != nil {
			t.Fatal(err)
		}
		received := uint64(start.UnixMilli())
		cv := chainVec{DeviceID: dev.id, KDev: kDev}
		chain := Chain{DeviceID: dev.id}
		seq, prev, flush := uint64(1), zeroHash, 0
		for b := uint64(1); b <= uint64(dev.batches); b++ {
			// Batches mostly reach the server about a minute apart, on
			// whole seconds, so devices tie now and then. Now and again a
			// device that was offline flushes a few batches at once, all in
			// one millisecond.
			if flush > 0 {
				flush--
			} else {
				received += uint64(30+rng.IntN(60)) * 1000
				if rng.IntN(40) == 0 {
					flush = 1 + rng.IntN(4)
				}
			}
			n := randomBatchSize(rng)
			recs := make([]Record, n)
			rvs := make([]recordVec, n)
			for i := range recs {
				r := pick(rng, codes)
				spec := recordSpec{rtype: r, deviceID: dev.id, seq: seq, t: int64(received) - rng.Int64N(55_000),
					projectKey: pick(rng, orNone), sessionKey: pick(rng, sessions)}
				if r == RTypeCheckpoint {
					spec.projectKey = pick(rng, projects)
				}
				rec, projectKey, sessionKey, err := testRecord(rng, k, spec)
				if err != nil {
					t.Fatalf("device %s, record %d: %v", dev.id, seq, err)
				}
				e := rec.Entry()
				recs[i] = rec
				rvs[i] = recordVec{Seq: seq, RType: uint8(r), ProjectKey: projectKey, SessionKey: sessionKey,
					Body: rec.Body, Nonce: hashBytes(k.Nonce(seq)), Entry: e[:]}
				seq++
			}
			entries := entriesOf(recs)
			row, err := Seal(prev, b, entries)
			if err != nil {
				t.Fatal(err)
			}
			rc, err := SignReceipt(server, dev.id, b, row.Head, received)
			if err != nil {
				t.Fatal(err)
			}
			row.Receipt = &rc
			msg, err := ReceiptMessage(dev.id, b, row.Head, received)
			if err != nil {
				t.Fatal(err)
			}
			leaves := EntryLeaves(entries)
			var proofs []proofVec
			for _, m := range proofIndices(rng, n, 9, 8) {
				path, err := InclusionProof(m, leaves)
				if err != nil {
					t.Fatal(err)
				}
				proofs = append(proofs, proofVec{Index: uint64(m), Path: hashesBytes(path)})
			}
			at := received
			cv.Batches = append(cv.Batches, batchVec{B: b, N: row.N, ReceivedAt: &at, Prev: hashBytes(prev),
				Root: hashBytes(row.Root), Head: hashBytes(row.Head), ReceiptMsg: msg, Records: rvs, Proofs: proofs})
			chain.Batches = append(chain.Batches, BatchOf(row, recs))
			dayRows = append(dayRows, DayRow{DeviceID: dev.id, B: b, Head: row.Head, ReceivedAt: received})
			prev = row.Head
		}
		if err := chain.Verify(Options{ServerKey: serverKeyAlways(testServerPub()), Keys: k}); err != nil {
			t.Fatalf("device %s: pkg/ledger refuses the chain it wrote: %v", dev.id, err)
		}
		doc.Chains = append(doc.Chains, cv)
	}

	byDay := map[uint64][]DayRow{}
	for _, r := range dayRows {
		byDay[DayOf(r.ReceivedAt)] = append(byDay[DayOf(r.ReceivedAt)], r)
	}
	days := slices.Sorted(maps.Keys(byDay))
	prevDay := zeroHash
	var published []PublishedDay
	for d := days[0]; d <= days[len(days)-1]; d++ {
		day, err := BuildDay(d, prevDay, byDay[d])
		if err != nil {
			t.Fatal(err)
		}
		dv := dayVec{Day: d, Date: dateOf(d), Prev: hashBytes(prevDay), Count: day.Count(), Root: hashBytes(day.Root),
			Head: hashBytes(day.Head), SignedMsg: day.Message(), Rows: []rowVec{}}
		for _, r := range day.Rows {
			leaf, err := r.Leaf()
			if err != nil {
				t.Fatal(err)
			}
			dv.Rows = append(dv.Rows, rowVec{DeviceID: r.DeviceID, B: r.B, Head: hashBytes(r.Head), ReceivedAt: r.ReceivedAt, Leaf: leaf})
		}
		for _, i := range proofIndices(rng, len(day.Rows), 64, 24) {
			path, err := day.Proof(i)
			if err != nil {
				t.Fatal(err)
			}
			dv.Proofs = append(dv.Proofs, proofVec{Index: uint64(i), Path: hashesBytes(path)})
		}
		p, err := day.Publish(server)
		if err != nil {
			t.Fatal(err)
		}
		if err := p.VerifyRows(day.Rows); err != nil {
			t.Fatalf("day %d: %v", d, err)
		}
		published = append(published, p)
		doc.Days = append(doc.Days, dv)
		prevDay = day.Head
	}
	if len(doc.Days) < 2 {
		t.Fatalf("the batches reached the server on %d days; they should span midnight", len(doc.Days))
	}
	if err := VerifyDays(testServerPub(), published); err != nil {
		t.Fatal(err)
	}
	return doc
}

// randomBatchSize is mostly a few records, sometimes a dozen, and now and
// then a hundred or so, like a batch sealed after a long turn.
func randomBatchSize(rng *rand.Rand) int {
	switch x := rng.IntN(100); {
	case x < 70:
		return 1 + rng.IntN(4)
	case x < 96:
		return 5 + rng.IntN(12)
	default:
		return 17 + rng.IntN(100)
	}
}

// proofIndices picks the leaves to prove in a tree of n leaves: all of
// them when n is at most every, and otherwise the first two, the middle,
// the last two and random others, count in all.
func proofIndices(rng *rand.Rand, n, every, count int) []int {
	if n <= every {
		idx := make([]int, n)
		for i := range idx {
			idx[i] = i
		}
		return idx
	}
	picks := map[int]bool{0: true, 1: true, n / 2: true, n - 2: true, n - 1: true}
	for len(picks) < count {
		picks[rng.IntN(n)] = true
	}
	return slices.Sorted(maps.Keys(picks))
}
