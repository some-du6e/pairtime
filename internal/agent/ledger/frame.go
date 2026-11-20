// SPDX-License-Identifier: AGPL-3.0-or-later

package ledger

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
)

// Every file the writer keeps is a run of frames:
//
//	frame = u8(kind) || u32(len(payload)) || payload || u32(CRC-32C of kind, len and payload)
//
// with integers big-endian. The CRC is what tells a whole frame from one a
// crash or a power cut left half written. Nothing in a frame is hashed into
// the ledger apart from a record's header and body, which a record frame
// carries as they are (ledger-v1 4).
const (
	frameHeadSize = 1 + 4
	frameTailSize = 4
	// maxPayload bounds a frame's payload. A length above it can only be
	// damage, so a reader stops there rather than read that much.
	maxPayload = 16 << 20
)

// The kinds of frame. A segment holds record and seal frames; batches.seg
// a chain frame, then batch and receipt frames.
const (
	// kindRecord is a record: its 33-byte header, then its body.
	kindRecord byte = 'r'
	// kindSeal closes a batch: it follows the batch's last record.
	kindSeal byte = 's'
	// kindChain names the chain batches.seg belongs to. It is that file's
	// first frame.
	kindChain byte = 'h'
	// kindBatch is a sealed batch's row.
	kindBatch byte = 'b'
	// kindReceipt is the server's receipt for a batch.
	kindReceipt byte = 'c'
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
var errTorn = errors.New("ledger: not a whole frame")

// tornError says where a file stops holding whole frames, and why.
type tornError struct {
	off int64
	why string
}

func (e *tornError) Error() string {
	return fmt.Sprintf("ledger: no whole frame at offset %d: %s", e.off, e.why)
}

func (e *tornError) Unwrap() error { return errTorn }

// frameReader reads frames from a file, counting offsets.
type frameReader struct {
	r   *bufio.Reader
	off int64
}

func newFrameReader(r io.Reader, off int64) *frameReader {
	return &frameReader{r: bufio.NewReaderSize(r, 64<<10), off: off}
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
	switch kind {
	case kindRecord, kindSeal, kindChain, kindBatch, kindReceipt:
	default:
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
