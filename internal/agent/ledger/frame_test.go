// SPDX-License-Identifier: AGPL-3.0-or-later

package ledger

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"testing"
)

func TestFrames(t *testing.T) {
	whole := appendFrame(appendFrame(nil, kindRecord, []byte("first")), kindSeal, []byte(`{"b":1}`))
	firstEnd := frameSize(len("first"))
	flipped := bytes.Clone(whole)
	flipped[firstEnd+frameHeadSize+1] ^= 0x01 // inside the second payload
	huge := appendFrame(nil, kindRecord, nil)
	binary.BigEndian.PutUint32(huge[1:], maxPayload+1)
	cases := []struct {
		name string
		data []byte
		// want is how many whole frames come back, and torn where the
		// reader finds a frame that isn't whole, or -1 for none.
		want int
		torn int64
	}{
		{"two frames read back whole, then the file ends", whole, 2, -1},
		{"an empty payload is a whole frame", appendFrame(nil, kindRecord, nil), 1, -1},
		{"an empty file holds no frames", nil, 0, -1},
		{"a file cut inside a frame's header is torn where the frame starts", whole[:firstEnd+3], 1, firstEnd},
		{"a file cut inside a payload is torn where the frame starts", whole[:len(whole)-6], 1, firstEnd},
		{"a file cut inside the CRC is torn where the frame starts", whole[:len(whole)-1], 1, firstEnd},
		{"a changed payload byte fails the CRC", flipped, 1, firstEnd},
		{"zeros a power cut leaves are no frame", append(bytes.Clone(whole), make([]byte, 32)...), 2, int64(len(whole))},
		{"a byte that is no kind of frame is torn", append(bytes.Clone(whole), 'x'), 2, int64(len(whole))},
		{"a length over the most a frame holds is torn, unread", huge, 0, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fr := newFrameReader(bytes.NewReader(c.data), 0)
			got := 0
			for {
				fm, err := fr.next()
				if errors.Is(err, io.EOF) {
					if c.torn >= 0 {
						t.Fatalf("read %d frames and reached the end, want a torn frame at %d", got, c.torn)
					}
					break
				}
				var torn *tornError
				if errors.As(err, &torn) {
					if torn.off != c.torn {
						t.Fatalf("torn at %d (%v), want %d", torn.off, err, c.torn)
					}
					break
				}
				if err != nil {
					t.Fatal(err)
				}
				if fm.end-fm.off != frameSize(len(fm.payload)) {
					t.Errorf("frame %d spans %d to %d, for a payload of %d bytes", got, fm.off, fm.end, len(fm.payload))
				}
				got++
			}
			if got != c.want {
				t.Errorf("read %d whole frames, want %d", got, c.want)
			}
		})
	}
}
