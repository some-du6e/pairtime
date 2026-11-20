// SPDX-License-Identifier: AGPL-3.0-or-later

package hookpath

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
	"testing/iotest"
	"time"
)

// openPipe returns a reader that yields what is written to it and never
// reaches EOF until the test ends, like a harness that leaves stdin open.
func openPipe(t *testing.T, write string) io.Reader {
	t.Helper()
	r, w := io.Pipe()
	go w.Write([]byte(write))
	t.Cleanup(func() { w.Close() })
	return r
}

func TestReadStdin(t *testing.T) {
	const limit = 1 << 10
	event := `{"session_id":"abc","hook_event_name":"Stop"}`
	tests := []struct {
		name      string
		r         func(t *testing.T) io.Reader
		wait      time.Duration
		want      string
		whole     bool
		cut       bool
		atMostFor time.Duration // 0: no bound on how long it may take
	}{
		{
			name:  "an event the harness closes stdin after is read whole",
			r:     func(*testing.T) io.Reader { return strings.NewReader(event) },
			wait:  time.Minute,
			want:  event,
			whole: true,
		},
		{
			name:  "an event that comes a byte at a time is read whole",
			r:     func(*testing.T) io.Reader { return iotest.OneByteReader(strings.NewReader(event)) },
			wait:  time.Minute,
			want:  event,
			whole: true,
		},
		{
			name: "stdin past the limit is cut at the limit",
			r:    func(*testing.T) io.Reader { return strings.NewReader(strings.Repeat("x", 3*limit)) },
			wait: time.Minute,
			want: strings.Repeat("x", limit),
			cut:  true,
		},
		{
			name: "a JSON object cut by the limit isn't whole",
			r: func(*testing.T) io.Reader {
				return strings.NewReader(`{"prompt":"` + strings.Repeat("y", 2*limit) + `"}`)
			},
			wait: time.Minute,
			want: (`{"prompt":"` + strings.Repeat("y", 2*limit))[:limit],
			cut:  true,
		},
		{
			name: "stdin that isn't JSON is read to its end, and isn't whole",
			r:    func(*testing.T) io.Reader { return strings.NewReader(`{"session_id": tru`) },
			wait: time.Minute,
			want: `{"session_id": tru`,
		},
		{
			name: "empty stdin reads nothing",
			r:    func(*testing.T) io.Reader { return strings.NewReader("") },
			wait: time.Minute,
		},
		{
			name: "no stdin reads nothing",
			r:    func(*testing.T) io.Reader { return nil },
			wait: time.Minute,
		},
		{
			name: "a read error ends stdin as EOF does",
			r: func(*testing.T) io.Reader {
				return io.MultiReader(strings.NewReader(event), iotest.ErrReader(errors.New("broken pipe")))
			},
			wait:  time.Minute,
			want:  event,
			whole: true,
		},
		{
			name:      "a whole JSON object on stdin that stays open is taken without waiting",
			r:         func(t *testing.T) io.Reader { return openPipe(t, event+"\n") },
			wait:      time.Minute,
			want:      event + "\n",
			whole:     true,
			atMostFor: 10 * time.Second,
		},
		{
			name: "stdin that stays open without a whole JSON object is cut when the wait runs out",
			r:    func(t *testing.T) io.Reader { return openPipe(t, `{"session_id":`) },
			wait: 50 * time.Millisecond,
			want: `{"session_id":`,
			cut:  true,
		},
		{
			name: "stdin that stays open with nothing on it is cut when the wait runs out",
			r: func(t *testing.T) io.Reader {
				r, w := io.Pipe()
				t.Cleanup(func() { w.Close() })
				return r
			},
			wait: 50 * time.Millisecond,
			cut:  true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			start := time.Now()
			data, whole, cut := readStdin(tt.r(t), limit, tt.wait)
			took := time.Since(start)
			if string(data) != tt.want {
				t.Errorf("read %q, want %q", shorten(data), shorten([]byte(tt.want)))
			}
			if whole != tt.whole || cut != tt.cut {
				t.Errorf("whole, cut = %v, %v, want %v, %v", whole, cut, tt.whole, tt.cut)
			}
			if tt.atMostFor > 0 && took > tt.atMostFor {
				t.Errorf("took %v, want at most %v", took, tt.atMostFor)
			}
		})
	}
}

func shorten(b []byte) string {
	if len(b) > 80 {
		return string(b[:40]) + "..." + string(b[len(b)-40:])
	}
	return string(bytes.ToValidUTF8(b, []byte("?")))
}
