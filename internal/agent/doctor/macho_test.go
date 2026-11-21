// SPDX-License-Identifier: AGPL-3.0-or-later

package doctor

import (
	"bytes"
	"encoding/binary"
	"testing"
)

// machOFile builds a thin 64-bit Mach-O binary with the given load
// commands, and sig, when set, as its code signature after them.
func machOFile(sig []byte, extra ...[]byte) []byte {
	le := binary.LittleEndian
	var cmds bytes.Buffer
	for _, c := range extra {
		cmds.Write(c)
	}
	n := len(extra)
	sigAt := 0
	if sig != nil {
		n++
		sigAt = machHeader64 + cmds.Len() + 16 + 64 // after the commands, with a gap
		cmd := make([]byte, 16)
		le.PutUint32(cmd[0:], lcCodeSignature)
		le.PutUint32(cmd[4:], 16)
		le.PutUint32(cmd[8:], uint32(sigAt))
		le.PutUint32(cmd[12:], uint32(len(sig)))
		cmds.Write(cmd)
	}
	hdr := make([]byte, machHeader64)
	le.PutUint32(hdr[0:], machMagic64)
	le.PutUint32(hdr[4:], 0x0100000c) // CPU_TYPE_ARM64
	le.PutUint32(hdr[12:], 2)         // MH_EXECUTE
	le.PutUint32(hdr[16:], uint32(n))
	le.PutUint32(hdr[20:], uint32(cmds.Len()))
	out := append(hdr, cmds.Bytes()...)
	if sig != nil {
		out = append(out, make([]byte, sigAt-len(out))...)
		out = append(out, sig...)
	}
	return out
}

// uuidCmd is an LC_UUID load command, which the reader passes over.
func uuidCmd() []byte {
	c := make([]byte, 24)
	binary.LittleEndian.PutUint32(c[0:], 0x1b)
	binary.LittleEndian.PutUint32(c[4:], 24)
	return c
}

// signature builds an embedded signature: a code directory with flags,
// and a CMS blob holding cms bytes when cms isn't nil.
func signature(flags uint32, cms []byte) []byte {
	be := binary.BigEndian
	type blob struct {
		slot uint32
		data []byte
	}
	cd := make([]byte, 16)
	be.PutUint32(cd[0:], csMagicCodeDir)
	be.PutUint32(cd[4:], 16)
	be.PutUint32(cd[8:], 0x20400)
	be.PutUint32(cd[12:], flags)
	blobs := []blob{{csSlotCodeDir, cd}}
	if cms != nil {
		w := make([]byte, 8+len(cms))
		be.PutUint32(w[0:], csMagicWrapper)
		be.PutUint32(w[4:], uint32(len(w)))
		copy(w[8:], cms)
		blobs = append(blobs, blob{csSlotCMS, w})
	}
	head := 12 + 8*len(blobs)
	out := make([]byte, head)
	be.PutUint32(out[0:], csMagicEmbedded)
	be.PutUint32(out[8:], uint32(len(blobs)))
	for i, b := range blobs {
		be.PutUint32(out[12+8*i:], b.slot)
		be.PutUint32(out[16+8*i:], uint32(len(out)))
		out = append(out, b.data...)
	}
	be.PutUint32(out[4:], uint32(len(out)))
	return out
}

func TestMachOSignature(t *testing.T) {
	tests := []struct {
		name    string
		file    []byte
		want    Verdict
		wantErr bool
	}{
		{"a binary with no code signature command is unsigned", machOFile(nil, uuidCmd()), Unsigned, false},
		{"a code directory flagged ad hoc, as the Go linker signs, is ad hoc", machOFile(signature(csAdhoc|0x20000, nil), uuidCmd()), AdHoc, false},
		{"an ad-hoc signature with an empty CMS blob is still ad hoc", machOFile(signature(csAdhoc, []byte{})), AdHoc, false},
		{"a code directory with a CMS signature and no ad-hoc flag is signed with a certificate", machOFile(signature(0, []byte("a cms signature")), uuidCmd()), Signed, false},
		{"a signature without a CMS blob or the ad-hoc flag is taken as ad hoc, never as signed", machOFile(signature(0, nil)), AdHoc, false},
		{"a file that isn't Mach-O can't be checked", []byte("\x7fELF\x02\x01\x01\x00 not a mach-o file at all, but long enough"), Unchecked, true},
		{"load commands cut short can't be checked", machOFile(nil, uuidCmd())[:machHeader64+10], Unchecked, true},
		{"a signature cut short can't be checked", func() []byte { f := machOFile(signature(csAdhoc, nil)); return f[:len(f)-4] }(), Unchecked, true},
		{"a signature that isn't an embedded one can't be checked", machOFile(make([]byte, 32)), Unchecked, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := machOSignature(bytes.NewReader(tt.file))
			if got != tt.want || (err != nil) != tt.wantErr {
				t.Errorf("machOSignature = %v, %v; want %v with error %v", got, err, tt.want, tt.wantErr)
			}
		})
	}
}
