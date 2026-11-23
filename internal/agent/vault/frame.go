// SPDX-License-Identifier: AGPL-3.0-or-later

package vault

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
)

// A vault file is a run of frames, as a ledger file is:
//
//	frame = u8(kind) || u32(len(payload)) || payload || u32(CRC-32C of kind, len and payload)
//
// with integers big-endian. The CRC tells a whole frame from one a crash or
// a power cut left half written.
const (
	frameHeadSize = 1 + 4
	frameTailSize = 4
	// maxPayload bounds a frame's payload, as the ledger's frames are
	// bounded. A length above it can only be damage.
	maxPayload = 16 << 20
)

// The kinds of frame: a file's first frame is its head, and every frame
// after it an entry.
const (
	kindHead  byte = 'h'
	kindEntry byte = 'e'
)

var castagnoli = crc32.MakeTable(crc32.Castagnoli)

// appendFrame appends a frame of kind with payload to dst.
func appendFrame(dst []byte, kind byte, payload []byte) []byte {
	start := len(dst)
	dst = append(dst, kind)
	dst = binary.BigEndian.AppendUint32(dst, uint32(len(payload)))
	dst = append(dst, payload...)
	return binary.BigEndian.AppendUint32(dst, crc32.Checksum(dst[start:], castagnoli))
}

// frameSize is the size of a frame with a payload of n bytes.
func frameSize(n int) int64 { return int64(frameHeadSize + n + frameTailSize) }

// frame is one frame read back, with where it is in its file.
type frame struct {
	kind    byte
	payload []byte
	off     int64 // where the frame starts
	end     int64 // just after it
}

// errTorn is the bytes at an offset that aren't a whole frame: cut short,
// failing their CRC, or not a frame at all, such as the zeros a power cut
// can leave at the end of a file.
var errTorn = errors.New("vault: not a whole frame")

// tornError says where a file stops holding whole frames, and why.
type tornError struct {
	off int64
	why string
}

func (e *tornError) Error() string {
	return fmt.Sprintf("vault: no whole frame at offset %d: %s", e.off, e.why)
}

func (e *tornError) Unwrap() error { return errTorn }

// frameReader reads frames from a file, counting offsets.
type frameReader struct {
	r   *bufio.Reader
	off int64
}

func newFrameReader(r io.Reader) *frameReader {
	return &frameReader{r: bufio.NewReaderSize(r, 64<<10)}
}

// next returns the next frame. It returns io.EOF at the end of the file
// and a *tornError where the bytes left aren't a whole frame of a known
// kind. After a tornError the reader is done.
func (fr *frameReader) next() (frame, error) {
	off := fr.off
	var head [frameHeadSize]byte
	n, err := io.ReadFull(fr.r, head[:])
	switch {
	case n == 0 && errors.Is(err, io.EOF):
		return frame{}, io.EOF
	case errors.Is(err, io.ErrUnexpectedEOF):
		return frame{}, &tornError{off, "the file ends inside a frame's header"}
	case err != nil:
		return frame{}, err
	}
	kind := head[0]
	if kind != kindHead && kind != kindEntry {
		return frame{}, &tornError{off, fmt.Sprintf("0x%02x isn't a kind of frame", kind)}
	}
	size := binary.BigEndian.Uint32(head[1:])
	if size > maxPayload {
		return frame{}, &tornError{off, fmt.Sprintf("a length of %d bytes is more than a frame holds", size)}
	}
	rest := make([]byte, int(size)+frameTailSize)
	if _, err := io.ReadFull(fr.r, rest); err != nil {
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			return frame{}, &tornError{off, "the file ends inside the frame"}
		}
		return frame{}, err
	}
	payload, tail := rest[:size], rest[size:]
	crc := crc32.Update(crc32.Checksum(head[:], castagnoli), castagnoli, payload)
	if crc != binary.BigEndian.Uint32(tail) {
		return frame{}, &tornError{off, "its CRC doesn't match"}
	}
	fr.off += frameSize(int(size))
	return frame{kind: kind, payload: payload, off: off, end: fr.off}, nil
}
