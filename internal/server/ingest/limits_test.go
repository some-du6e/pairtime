// SPDX-License-Identifier: AGPL-3.0-or-later

package ingest

import (
	"io"
	"runtime"
	"strings"
	"testing"

	"github.com/some-du6e/pairtime/internal/server/store"
)

// TestLimitsWhileReading sends requests past DESIGN 4.5's limits through
// POST /api/v1/ingest/batches, each from a linked device and signed when
// its body is, and checks that the server refuses each before it reads or
// inflates past the limit (ingest-v1 2.4): a Content-Length over 256 KiB
// before a byte of the body is read, an endless body one byte past 256
// KiB, a gzip bomb once 4 MiB are inflated, and a body of two million
// items at the item past the limit. envelope_test.go checks the same for
// readRaw, inflate and parseBody alone; this checks that nothing in front
// of them reads more.
func TestLimitsWhileReading(t *testing.T) {
	engines(t, func(t *testing.T, engine store.Engine) {
		h := newHarness(t, engine)
		dev := h.device("d_test", false)
		millions := []byte(`{"buckets":[` + strings.Repeat("0,", (MaxInflatedBody-32)/2-1) + `0]}`)
		if len(millions) > MaxInflatedBody {
			t.Fatalf("the body of items is %d bytes, over the inflated limit, so it tests nothing", len(millions))
		}
		tests := []struct {
			name string
			u    upload
			// body, when set, replaces the signed body, with length as its
			// Content-Length; maxRead is the most of it the server may read.
			body     *countingReader
			length   int64
			maxRead  int
			code     string
			maxAlloc uint64 // the most serving the request may allocate, or 0
		}{
			{name: "a Content-Length over 256 KiB is refused before a byte of the body is read",
				body: &countingReader{}, length: MaxRawBody + 1, maxRead: 0, code: CodeBodyTooLarge},
			{name: "an endless body without a Content-Length is refused one byte past 256 KiB",
				body: &countingReader{}, length: -1, maxRead: MaxRawBody + 1, code: CodeBodyTooLarge},
			{name: "a signed gzip bomb of 64 MiB stops inflating at the 4 MiB limit",
				u: upload{raw: bomb(t, 64<<20)}, code: CodeDecompressedTooLarge, maxAlloc: 32 << 20},
			{name: "a signed body of two million buckets is refused at the 2,001st, keeping no more",
				u: upload{json: millions}, code: CodeTooManyBuckets, maxAlloc: 48 << 20},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				u := tt.u
				u.dev = dev
				if u.raw != nil && len(u.raw) > MaxRawBody {
					t.Fatalf("the raw body is %d bytes, over the raw limit, so it tests nothing", len(u.raw))
				}
				r := h.request(t, u)
				if tt.body != nil {
					r.Body = io.NopCloser(tt.body)
					r.ContentLength = tt.length
				}
				var before, after runtime.MemStats
				runtime.GC()
				runtime.ReadMemStats(&before)
				rec := h.serve(t, r)
				runtime.ReadMemStats(&after)
				if status, code := refusalOf(t, rec); status != refusals[tt.code] || code != tt.code {
					t.Fatalf("got %d %s, want %d %s: %s", status, code, refusals[tt.code], tt.code, rec.Body)
				}
				if tt.body != nil && tt.body.n > tt.maxRead {
					t.Errorf("the server read %d bytes of the body, more than %d", tt.body.n, tt.maxRead)
				}
				if grew := after.TotalAlloc - before.TotalAlloc; tt.maxAlloc > 0 && grew > tt.maxAlloc {
					t.Errorf("serving the request allocated %d bytes, more than %d: it didn't stop at the limit", grew, tt.maxAlloc)
				}
			})
		}
		t.Run("none of them moved the counter or stored anything", func(t *testing.T) {
			if st := h.state(t, dev); st.Req != 0 || st.Head != nil {
				t.Errorf("state = %+v, want counter 0 and no head", st)
			}
		})
	})
}
