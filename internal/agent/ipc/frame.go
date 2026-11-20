// SPDX-License-Identifier: AGPL-3.0-or-later

package ipc

import (
	"encoding/binary"
	"errors"
	"io"
)

// A frame is a 4-byte big-endian length and then that many bytes of JSON.
// A sender writes one frame per message, and the agent answers each with a
// reply frame (reply). A connection may carry many messages in turn, which
// `pairtime emit` will use (WP-AD19); a hook sends one and hangs up.
const frameHeader = 4

// maxReply bounds a reply frame, which is a few dozen bytes.
const maxReply = 4 << 10

// ErrFrameSize means a frame's length is 0 or over the limit. The stream
// can't be read past it, so the connection has to close.
var ErrFrameSize = errors.New("ipc: frame length is 0 or over the limit")

// WriteFrame writes body as one frame, in a single Write call, so a frame
// never goes out in pieces from this side.
func WriteFrame(w io.Writer, body []byte, limit int) error {
	if len(body) == 0 || len(body) > limit {
		return ErrFrameSize
	}
	buf := make([]byte, frameHeader+len(body))
	binary.BigEndian.PutUint32(buf, uint32(len(body)))
	copy(buf[frameHeader:], body)
	_, err := w.Write(buf)
	return err
}

// ReadFrame reads one frame whose body is at most limit bytes. It returns
// io.EOF when the stream ends cleanly before a frame starts, and
// io.ErrUnexpectedEOF when it ends inside one.
func ReadFrame(r io.Reader, limit int) ([]byte, error) {
	var hdr [frameHeader]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return nil, err
	}
	n := binary.BigEndian.Uint32(hdr[:])
	if n == 0 || uint64(n) > uint64(limit) {
		return nil, ErrFrameSize
	}
	body := make([]byte, n)
	if _, err := io.ReadFull(r, body); err != nil {
		if errors.Is(err, io.EOF) {
			err = io.ErrUnexpectedEOF
		}
		return nil, err
	}
	return body, nil
}
