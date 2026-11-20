// SPDX-License-Identifier: AGPL-3.0-or-later

package ipc

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"testing"
)

func TestFrames(t *testing.T) {
	header := func(n uint32) []byte {
		var b [4]byte
		binary.BigEndian.PutUint32(b[:], n)
		return b[:]
	}
	tests := []struct {
		name    string
		in      []byte // what the reader reads
		limit   int
		want    []byte
		wantErr error
	}{
		{name: "a frame is its 4-byte big-endian length, then its body", in: append(header(2), "{}"...), limit: 10, want: []byte("{}")},
		{name: "a stream that ends before a frame is io.EOF", in: nil, limit: 10, wantErr: io.EOF},
		{name: "a stream that ends inside the header is io.ErrUnexpectedEOF", in: []byte{0, 0}, limit: 10, wantErr: io.ErrUnexpectedEOF},
		{name: "a stream that ends inside the body is io.ErrUnexpectedEOF", in: append(header(5), "{}"...), limit: 10, wantErr: io.ErrUnexpectedEOF},
		{name: "a frame over the limit is refused before its body is read", in: header(11), limit: 10, wantErr: ErrFrameSize},
		{name: "an empty frame is refused", in: header(0), limit: 10, wantErr: ErrFrameSize},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ReadFrame(bytes.NewReader(tt.in), tt.limit)
			if !errors.Is(err, tt.wantErr) || !bytes.Equal(got, tt.want) {
				t.Errorf("ReadFrame = %q, %v; want %q, %v", got, err, tt.want, tt.wantErr)
			}
		})
	}

	writes := []struct {
		name    string
		body    []byte
		limit   int
		wantErr error
	}{
		{name: "WriteFrame writes what ReadFrame reads back", body: []byte(`{"id":"x"}`), limit: 100},
		{name: "WriteFrame refuses an empty body", body: nil, limit: 100, wantErr: ErrFrameSize},
		{name: "WriteFrame refuses a body over the limit", body: []byte(`{"id":"x"}`), limit: 5, wantErr: ErrFrameSize},
	}
	for _, tt := range writes {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			err := WriteFrame(&buf, tt.body, tt.limit)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("WriteFrame = %v, want %v", err, tt.wantErr)
			}
			if err != nil {
				if buf.Len() != 0 {
					t.Errorf("a refused frame still wrote %d bytes", buf.Len())
				}
				return
			}
			got, err := ReadFrame(&buf, tt.limit)
			if err != nil || !bytes.Equal(got, tt.body) {
				t.Errorf("read back %q, %v; want %q", got, err, tt.body)
			}
		})
	}
}
