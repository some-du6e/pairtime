// SPDX-License-Identifier: AGPL-3.0-or-later

package ledger

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/some-du6e/pairtime/pkg/event"
)

// TestSpecWorkedExample computes the worked example of spec section 12 and
// compares each value with the one the spec prints.
func TestSpecWorkedExample(t *testing.T) {
	k := testKeys(t)
	prk, err := extract(exampleKDev(), make([]byte, sha256.Size))
	if err != nil {
		t.Fatal(err)
	}
	leafKey, tokKey, featKey, vaultKey := k.LeafKey(), k.TokenKey(), k.FeatKey(), k.VaultKey()
	project, err := ProjectDigest("p_example")
	if err != nil {
		t.Fatal(err)
	}
	session, err := SessionDigest("s_example")
	if err != nil {
		t.Fatal(err)
	}

	type value struct {
		name string
		got  []byte
		want string
	}
	values := []value{
		{"12.1 PRK is HMAC-SHA256(zero(32), K_dev)", prk, "46bd320605c5a6b6163ab70bc6345b92a5f908e79fe58979c23ebb47d1a5e307"},
		{"12.1 K_leaf is HKDF-SHA256 with info pairtime/v1/leaf", leafKey[:], "03ece076e53f8e25304839d8440440c015f90b22c6e1fdb8a6df0b250523f6d8"},
		{"12.1 K_tok is HKDF-SHA256 with info pairtime/v1/token", tokKey[:], "6f55cd167c1531f33afaa9e9b486ae9c441cd03d69ccafc29189111aa6bc76f4"},
		{"12.1 K_feat is HKDF-SHA256 with info pairtime/v1/feat", featKey[:], "c9ad26aafffd194d9a0ea333a5fc444203aec6be575eb7bb253f942a67e53e70"},
		{"12.1 K_vault is HKDF-SHA256 with info pairtime/v1/vault", vaultKey[:], "e04e0f65a92a86bef6551c50829f02191e37092290e74232e1a5acd722f7dd12"},
		{"12.2 project is SHA-256(p_example)[0:16]", project[:], "44f24edfdf081d9d54bf2363932f2238"},
		{"12.2 session is SHA-256(s_example)[0:8]", session[:], "8cf787fc8e65ef99"},
	}

	records := []struct {
		s                uint64
		rtype            RType
		project, session string
		nonce, bodyHash  string
		hdr, entry       string
	}{
		{1, 0x0e, "", event.NoSession,
			"606cd8ffa6f47d889c246f68a533884bcb7f06f50856b7a5b7b652661d5b421f",
			"420440de3ab02c01e44bc1f996b96e804743e85e755f8fe28e423258023bf0cf",
			"00000000000000010e000000000000000000000000000000000000000000000000",
			"00000000000000010e000000000000000000000000000000000000000000000000420440de3ab02c01e44bc1f996b96e804743e85e755f8fe28e423258023bf0cf"},
		{2, 0x03, "p_example", "s_example",
			"307601f3934aaac02e04d46a7fc34f067003619812e6928f60858d439075f569",
			"34a9fe8fca3ad97eb963b156da7fbf5f1631dc700b27f31450c489ab5826917d",
			"00000000000000020344f24edfdf081d9d54bf2363932f22388cf787fc8e65ef99",
			"00000000000000020344f24edfdf081d9d54bf2363932f22388cf787fc8e65ef9934a9fe8fca3ad97eb963b156da7fbf5f1631dc700b27f31450c489ab5826917d"},
		{3, RTypeCheckpoint, "p_example", event.NoSession,
			"f118141d8b500a8cb7e37bb3fc530542b9427f84196a3f10b76cfa12360142b4",
			"9e85465282ae9812cd845c6b1511668fed3ebf04440a1f9f638aa7cc9fe305d2",
			"00000000000000034044f24edfdf081d9d54bf2363932f22380000000000000000",
			"00000000000000034044f24edfdf081d9d54bf2363932f223800000000000000009e85465282ae9812cd845c6b1511668fed3ebf04440a1f9f638aa7cc9fe305d2"},
	}
	var entries []Entry
	for _, r := range records {
		n := k.Nonce(r.s)
		body, err := Body{N: n, E: json.RawMessage(fmt.Sprintf(`{"stand_in":%q,"seq":%d}`, r.rtype.String(), r.s))}.Encode()
		if err != nil {
			t.Fatal(err)
		}
		wantBody := `{"n":"` + r.nonce + `","e":{"stand_in":"` + r.rtype.String() + `","seq":` + fmt.Sprint(r.s) + `}}`
		h, err := NewHeader(r.s, r.rtype, r.project, r.session)
		if err != nil {
			t.Fatal(err)
		}
		bodyHash := sha256.Sum256(body)
		e := NewEntry(h, body)
		entries = append(entries, e)
		values = append(values,
			value{fmt.Sprintf("12.2 record %d's nonce is HMAC-SHA256(K_leaf, 0x01 || u64(%d))", r.s, r.s), n[:], r.nonce},
			value{fmt.Sprintf("12.2 record %d's body is the nonce then the stand-in record", r.s), body, hex.EncodeToString([]byte(wantBody))},
			value{fmt.Sprintf("12.2 record %d's body hash is SHA-256 of the body", r.s), bodyHash[:], r.bodyHash},
			value{fmt.Sprintf("12.2 record %d's header is u64(s) || rtype || project || session", r.s), h[:], r.hdr},
			value{fmt.Sprintf("12.2 record %d's entry is its header then its body hash", r.s), e[:], r.entry},
		)
	}

	leaves := EntryLeaves(entries)
	row, err := Seal(zeroHash, 1, entries)
	if err != nil {
		t.Fatal(err)
	}
	node := NodeHash(leaves[0], leaves[1])
	proof, err := InclusionProof(1, leaves)
	if err != nil {
		t.Fatal(err)
	}
	if len(proof) != 2 {
		t.Fatalf("the proof of record 2 has %d hashes, the spec 2", len(proof))
	}
	const receivedAt = 1795089660250 // 2026-11-19T12:01:00.250Z
	msg, err := ReceiptMessage("d_example", 1, row.Head, receivedAt)
	if err != nil {
		t.Fatal(err)
	}
	values = append(values,
		value{"12.3 L1 is LEAF(entry 1)", leaves[0][:], "7032e9622881fd3312c71e8d40655522fa52f3f8c0081e6ce011e08d95e4bb38"},
		value{"12.3 L2 is LEAF(entry 2)", leaves[1][:], "df456675d8772a06b52b8b9e3ef2a349f504162e1b8682deb0e4d5cd769044a1"},
		value{"12.3 L3 is LEAF(entry 3)", leaves[2][:], "6ae6ba7550b1a30f4e1ed0f9af3404c263f3f1c4fd078e70352f7390a193fc44"},
		value{"12.3 NODE(L1, L2)", node[:], "7196b4294b0d7ff5dcade1e5d0b908874cc7ca864d6939cc0cc6c70d0e033f65"},
		value{"12.3 R_1 is NODE(NODE(L1, L2), L3)", row.Root[:], "cee3ad5e6675750f997684c411a4ff6cd1fdbeabfd296b3a68679795850a8e0c"},
		value{"12.3 H_1 is SHA-256(0x03 || zero(32) || R_1 || u64(3) || u64(1))", row.Head[:], "d0277390f5841d01711ecb7493662d98d18fd1a318f14ce25fefc94edc1b00a7"},
		value{"12.3 the proof of record 2 starts with L1", proof[0][:], "7032e9622881fd3312c71e8d40655522fa52f3f8c0081e6ce011e08d95e4bb38"},
		value{"12.3 the proof of record 2 ends with L3", proof[1][:], "6ae6ba7550b1a30f4e1ed0f9af3404c263f3f1c4fd078e70352f7390a193fc44"},
		value{"12.4 M_1 is the context, the device id with its length, b, H_1 and received_at", msg,
			"7061697274696d652d726563656970742d763109645f6578616d706c650000000000000001d0277390f5841d01711ecb7493662d98d18fd1a318f14ce25fefc94edc1b00a7000001a1f3ae795a"},
	)

	otherHead := sum([]byte("stand-in head of batch 7 of d_other"))
	d := DayOf(receivedAt)
	day, err := BuildDay(d, zeroHash, []DayRow{
		{DeviceID: "d_example", B: 1, Head: row.Head, ReceivedAt: receivedAt},
		{DeviceID: "d_other", B: 7, Head: otherHead, ReceivedAt: receivedAt - 30_000},
	})
	if err != nil {
		t.Fatal(err)
	}
	leaf1, err := day.Rows[0].Leaf()
	if err != nil {
		t.Fatal(err)
	}
	leaf2, err := day.Rows[1].Leaf()
	if err != nil {
		t.Fatal(err)
	}
	dayProof, err := day.Proof(1)
	if err != nil {
		t.Fatal(err)
	}
	if len(dayProof) != 1 {
		t.Fatalf("the day proof of d_example has %d hashes, the spec 1", len(dayProof))
	}
	values = append(values,
		value{"12.5 the stand-in head is SHA-256 of its text", otherHead[:], "0f8e07775a3efbc8ca16d900af808e95567b6092c72e22a0a3d18872e06cc242"},
		value{"12.5 the earlier received_at sorts first: leaf 1 is d_other's", leaf1,
			"07645f6f7468657200000000000000070f8e07775a3efbc8ca16d900af808e95567b6092c72e22a0a3d18872e06cc242000001a1f3ae042a"},
		value{"12.5 leaf 2 is d_example's batch 1", leaf2,
			"09645f6578616d706c650000000000000001d0277390f5841d01711ecb7493662d98d18fd1a318f14ce25fefc94edc1b00a7000001a1f3ae795a"},
		value{"12.5 LEAF(leaf 1)", day.leaves[0][:], "dbb9530abac5f5e52ac7188e663999f5510b184196d22795ae6df086de8ae3de"},
		value{"12.5 LEAF(leaf 2)", day.leaves[1][:], "bf08bde260d74c54b160bf8345364601ae6afe57cfac850ecc3be3f054c6c850"},
		value{"12.5 T_d is NODE(LEAF(leaf 1), LEAF(leaf 2))", day.Root[:], "10d0de031bcf121f8db0423641f73dc5105696cece9d56f7df58feeb02b372a2"},
		value{"12.5 G_d is SHA-256(0x04 || zero(32) || T_d || u64(2) || u64(20776))", day.Head[:], "b222eaa5b42b58700786a473f68ca59f03f77e4971d49b77e049bf5f9e6650c5"},
		value{"12.5 D_d is the context, u64(d) and G_d", day.Message(),
			"7061697274696d652d6461792d76310000000000005128b222eaa5b42b58700786a473f68ca59f03f77e4971d49b77e049bf5f9e6650c5"},
		value{"12.5 the day proof of d_example is LEAF(leaf 1)", dayProof[0][:], "dbb9530abac5f5e52ac7188e663999f5510b184196d22795ae6df086de8ae3de"},
	)

	t.Run("12.4 received_at 2026-11-19T12:01:00.250Z falls on day 20776", func(t *testing.T) {
		if d != 20776 || dateOf(d) != "2026-11-19" {
			t.Errorf("day %d, %s", d, dateOf(d))
		}
	})
	for _, v := range values {
		t.Run(v.name, func(t *testing.T) {
			if got := hex.EncodeToString(v.got); got != v.want {
				t.Errorf("pkg/ledger computes %s, the spec %s", got, v.want)
			}
		})
	}
}
