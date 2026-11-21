// SPDX-License-Identifier: AGPL-3.0-or-later

package doctor

// A reader of a Mach-O binary's code signature, enough to tell no
// signature, an ad-hoc one and one made with a certificate apart. It reads
// the bytes by hand rather than through debug/macho, which would bring
// debug/dwarf into the client binary for one load command. macOS's own
// check (Gatekeeper, codesign) is what verifies a signature; this only
// says which kind is there, so it never claims one is valid.

import (
	"encoding/binary"
	"errors"
	"io"
)

// The parts of the Mach-O and code signing formats it reads (Apple's
// mach-o/loader.h and the Security framework's cs_blobs.h).
const (
	machMagic64     = 0xfeedfacf // MH_MAGIC_64, little-endian on amd64 and arm64
	machHeader64    = 32         // sizeof(struct mach_header_64)
	lcCodeSignature = 0x1d       // LC_CODE_SIGNATURE

	csMagicEmbedded = 0xfade0cc0 // CSMAGIC_EMBEDDED_SIGNATURE, a SuperBlob
	csMagicCodeDir  = 0xfade0c02 // CSMAGIC_CODEDIRECTORY
	csMagicWrapper  = 0xfade0b01 // CSMAGIC_BLOBWRAPPER, which holds the CMS signature
	csSlotCodeDir   = 0          // CSSLOT_CODEDIRECTORY
	csSlotCMS       = 0x10000    // CSSLOT_SIGNATURESLOT
	csAdhoc         = 0x2        // CS_ADHOC, in the code directory's flags

	// maxLoadCmds and maxSignature bound what is read, so a damaged file
	// can't make doctor allocate much.
	maxLoadCmds  = 1 << 20
	maxSignature = 16 << 20
)

// errNotMachO means the file isn't a thin 64-bit Mach-O binary.
var errNotMachO = errors.New("it isn't a 64-bit Mach-O binary")

// machOSignature reports which signature the Mach-O binary r holds:
// Unsigned, AdHoc or Signed, with a certificate's CMS signature that this
// doesn't verify.
func machOSignature(r io.ReaderAt) (Verdict, error) {
	var hdr [machHeader64]byte
	if _, err := r.ReadAt(hdr[:], 0); err != nil {
		return Unchecked, errNotMachO
	}
	le := binary.LittleEndian
	if le.Uint32(hdr[0:]) != machMagic64 {
		return Unchecked, errNotMachO
	}
	ncmds, size := le.Uint32(hdr[16:]), le.Uint32(hdr[20:])
	if size > maxLoadCmds {
		return Unchecked, errors.New("its load commands are too long")
	}
	cmds := make([]byte, size)
	if _, err := r.ReadAt(cmds, machHeader64); err != nil {
		return Unchecked, errors.New("its load commands are cut short")
	}
	var off, n uint32
	found := false
	for i, at := uint32(0), uint32(0); i < ncmds; i++ {
		if at+8 > size {
			return Unchecked, errors.New("its load commands are cut short")
		}
		cmd, cmdSize := le.Uint32(cmds[at:]), le.Uint32(cmds[at+4:])
		if cmdSize < 8 || cmdSize > size-at {
			return Unchecked, errors.New("a load command has a bad size")
		}
		if cmd == lcCodeSignature {
			if cmdSize < 16 {
				return Unchecked, errors.New("its code signature command is too short")
			}
			off, n, found = le.Uint32(cmds[at+8:]), le.Uint32(cmds[at+12:]), true
			break
		}
		at += cmdSize
	}
	if !found {
		return Unsigned, nil
	}
	if n > maxSignature || n < 12 {
		return Unchecked, errors.New("its code signature has a bad size")
	}
	sig := make([]byte, n)
	if _, err := r.ReadAt(sig, int64(off)); err != nil {
		return Unchecked, errors.New("its code signature is cut short")
	}
	return superBlob(sig)
}

// superBlob reads an embedded signature: a SuperBlob of big-endian blobs,
// its code directory and, for a signature made with a certificate, a CMS
// blob with something in it.
func superBlob(sig []byte) (Verdict, error) {
	be := binary.BigEndian
	if be.Uint32(sig) != csMagicEmbedded {
		return Unchecked, errors.New("its code signature isn't an embedded signature")
	}
	count := be.Uint32(sig[8:])
	if uint64(count)*8+12 > uint64(len(sig)) {
		return Unchecked, errors.New("its code signature's index is cut short")
	}
	adhoc, cms, codeDir := false, false, false
	for i := uint32(0); i < count; i++ {
		typ, at := be.Uint32(sig[12+8*i:]), be.Uint32(sig[16+8*i:])
		if uint64(at)+8 > uint64(len(sig)) {
			return Unchecked, errors.New("a blob of its code signature is out of bounds")
		}
		magic, length := be.Uint32(sig[at:]), be.Uint32(sig[at+4:])
		switch {
		case typ == csSlotCodeDir && magic == csMagicCodeDir:
			if uint64(at)+16 > uint64(len(sig)) {
				return Unchecked, errors.New("its code directory is cut short")
			}
			codeDir = true
			adhoc = be.Uint32(sig[at+12:])&csAdhoc != 0
		case typ == csSlotCMS && magic == csMagicWrapper:
			// An ad-hoc signature has no CMS blob, or an empty one.
			cms = length > 8
		}
	}
	switch {
	case !codeDir:
		return Unchecked, errors.New("its code signature has no code directory")
	case adhoc || !cms:
		return AdHoc, nil
	}
	return Signed, nil
}

// machOWords says a Mach-O verdict in doctor's words.
func machOWords(v Verdict) string {
	switch v {
	case Unsigned:
		return "not code-signed"
	case AdHoc:
		return "signed ad hoc, with no developer's identity, as the Go linker signs every Apple silicon binary, which Gatekeeper doesn't take as a signature"
	case Signed:
		return "signed with a developer's certificate, which macOS checks when it runs"
	}
	return "its signature couldn't be checked"
}
