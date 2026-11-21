// SPDX-License-Identifier: AGPL-3.0-or-later

package ingest

import (
	"bytes"
	"compress/gzip"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"io"
	"net/http"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/some-du6e/pairtime/pkg/ledger"
)

// The worked example of ingest-v1 2.2, which Python's gzip and RFC 8032's
// reference Ed25519 wrote.
const (
	exampleSeedText = "pairtime ingest-v1 example device key"
	exampleSeed     = "7bf821b60e33d7e00abe88a5e7e8e31715789eacda1a3ac3eea47eca45c210c1"
	examplePublic   = "fadacf1c88bce8152ec27995a05aa8d9c53ac5678801cdd2e05b19060398a677"
	exampleHead     = "709a14e467695dbaddf729b36d14c2629ba332116c1942c8a21a8e1d21309123"
	exampleJSON     = `{"batches":[{"b":1,"n":3,"R_b":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","H_b":"709a14e467695dbaddf729b36d14c2629ba332116c1942c8a21a8e1d21309123","prev":"0000000000000000000000000000000000000000000000000000000000000000"}]}`
	exampleRaw      = "1f8b0800000000000203a5cc310e02310c44d1bb4c9d22b683b3c909a86911424e1c448510209ad5de9d882becaf669ab7a2d9a7dfc71bf5bca2a152c00355024ed7f9603b43c0f10fe5588cd2489ab51cbc99fb2d7369a24ea9b3ce69224ca49d4ae2be18932d839c49622196093d5fe33ba5b8336c97ed07d5b9d682f7000000"
	exampleBodySHA  = "c8e78a7463f88a78d206683e69e3f847fe3d83715ad7ec2845e997167ddf4e15"
	exampleDevice   = "d_example"
	exampleReq      = "42"
	exampleTime     = "1795089660250"
	exampleM        = "7061697274696d652d696e676573742d76310a645f6578616d706c650a34320a313739353038393636303235300a63386537386137343633663838613738643230363638336536396533663834376665336438333731356164376563323834356539393731363764646634653135"
	exampleSig      = "1ffce087cf6259d5b28584fd9bc097e4333fb8cdf9818cc7b060dddbd66f2362f508f8768f53c61ebe72e750e6a0fef48870b8cfef6def01f78a3640bcf35500"
	exampleHeader   = "ed25519=H/zgh89iWdWyhYT9m8CX5DM/uM35gYzHsGDd29ZvI2L1CPh2j1PGHr5y51DmoP70iHC4z+9t7wH3ijZAvPNVAA=="
)

// exampleKey is the worked example's device key.
func exampleKey() ed25519.PrivateKey {
	seed := sha256.Sum256([]byte(exampleSeedText))
	return ed25519.NewKeyFromSeed(seed[:])
}

// ledgerHead is the head that follows from br's prev, R_b, n and b.
func ledgerHead(br batchRoot) string {
	return ledger.BatchHead(br.prev, br.root, br.n, br.b).String()
}

func mustHex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestWorkedExample(t *testing.T) {
	seed := sha256.Sum256([]byte(exampleSeedText))
	key := exampleKey()
	raw := mustHex(t, exampleRaw)
	inflated, ref := inflate(raw)
	if ref != nil {
		t.Fatalf("the example's raw body doesn't inflate: %s", ref.code)
	}
	sum := sha256.Sum256(raw)
	m := Message(exampleDevice, exampleReq, exampleTime, exampleBodySHA)
	sig := ed25519.Sign(key, m)
	br, e := parseBatchRoot([]byte(exampleJSON[len(`{"batches":[`) : len(exampleJSON)-2]))
	if e != nil {
		t.Fatalf("the example's batch root fails its shape: %s", e.code)
	}
	head := ledgerHead(br)
	tests := []struct{ name, got, want string }{
		{"the seed is the SHA-256 of the spec's string", hex.EncodeToString(seed[:]), exampleSeed},
		{"the public key follows from the seed", hex.EncodeToString(key.Public().(ed25519.PublicKey)), examplePublic},
		{"the raw body inflates to the spec's JSON", string(inflated), exampleJSON},
		{"the batch root's head follows from prev, R_b, n and b", head, exampleHead},
		{"the body hash is the SHA-256 of the raw body", hex.EncodeToString(sum[:]), exampleBodySHA},
		{"M is DESIGN 4.5's message over the example's values", hex.EncodeToString(m), exampleM},
		{"M is 110 bytes", strconv.Itoa(len(m)), "110"},
		{"Ed25519 over M is the spec's signature", hex.EncodeToString(sig), exampleSig},
		{"the header is ed25519= and the signature in padded base64", signaturePrefix + base64.StdEncoding.EncodeToString(sig), exampleHeader},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got != tt.want {
				t.Errorf("got  %s\nwant %s", tt.got, tt.want)
			}
		})
	}
}

// exampleHeaders are the worked example's signed headers.
func exampleHeaders() http.Header {
	h := http.Header{}
	h.Set(HeaderIdempotencyKey, "0190f4b2-5a1c-7d3e-8f00-123456789abc")
	h.Set(HeaderReq, exampleReq)
	h.Set(HeaderTime, exampleTime)
	h.Set(HeaderBodySHA256, exampleBodySHA)
	h.Set(HeaderSignature, exampleHeader)
	return h
}

func TestParseEnvelope(t *testing.T) {
	set := func(name, v string) func(http.Header) { return func(h http.Header) { h.Set(name, v) } }
	add := func(name, v string) func(http.Header) { return func(h http.Header) { h.Add(name, v) } }
	del := func(name string) func(http.Header) { return func(h http.Header) { h.Del(name) } }
	b64 := strings.TrimPrefix(exampleHeader, signaturePrefix)
	tests := []struct {
		name string
		edit func(http.Header)
		want string // the refusal's code, or "" when the headers pass
	}{
		{"the worked example's headers pass", func(http.Header) {}, ""},
		{"the largest counter passes", set(HeaderReq, "9223372036854775807"), ""},
		{"a missing Idempotency-Key is malformed", del(HeaderIdempotencyKey), CodeIdempotencyKeyMalformed},
		{"a repeated Idempotency-Key is malformed", add(HeaderIdempotencyKey, "0190f4b2-5a1c-7d3e-8f00-123456789abd"), CodeIdempotencyKeyMalformed},
		{"an uppercase UUIDv7 is malformed", set(HeaderIdempotencyKey, "0190F4B2-5A1C-7D3E-8F00-123456789ABC"), CodeIdempotencyKeyMalformed},
		{"a UUIDv4 is malformed", set(HeaderIdempotencyKey, "0190f4b2-5a1c-4d3e-8f00-123456789abc"), CodeIdempotencyKeyMalformed},
		{"a UUIDv7 of another variant is malformed", set(HeaderIdempotencyKey, "0190f4b2-5a1c-7d3e-cf00-123456789abc"), CodeIdempotencyKeyMalformed},
		{"a UUID without hyphens is malformed", set(HeaderIdempotencyKey, "0190f4b25a1c7d3e8f00123456789abc"), CodeIdempotencyKeyMalformed},
		{"a missing counter is malformed", del(HeaderReq), CodeCounterMalformed},
		{"a counter of 0 is malformed", set(HeaderReq, "0"), CodeCounterMalformed},
		{"a counter with a leading zero is malformed", set(HeaderReq, "042"), CodeCounterMalformed},
		{"a counter with a sign is malformed", set(HeaderReq, "+42"), CodeCounterMalformed},
		{"a negative counter is malformed", set(HeaderReq, "-1"), CodeCounterMalformed},
		{"a counter past 2^63 - 1 is malformed", set(HeaderReq, "9223372036854775808"), CodeCounterMalformed},
		{"a repeated counter is malformed", add(HeaderReq, "43"), CodeCounterMalformed},
		{"a missing time is malformed", del(HeaderTime), CodeTimeMalformed},
		{"a time with a fraction is malformed", set(HeaderTime, "1795089660250.5"), CodeTimeMalformed},
		{"a time in hex is malformed", set(HeaderTime, "0x1a1f3ae795a"), CodeTimeMalformed},
		{"a missing body hash is malformed", del(HeaderBodySHA256), CodeBodyHashMalformed},
		{"an uppercase body hash is malformed", set(HeaderBodySHA256, strings.ToUpper(exampleBodySHA)), CodeBodyHashMalformed},
		{"a body hash one digit short is malformed", set(HeaderBodySHA256, exampleBodySHA[:63]), CodeBodyHashMalformed},
		{"a missing signature is malformed", del(HeaderSignature), CodeSignatureMalformed},
		{"a signature without ed25519= is malformed", set(HeaderSignature, b64), CodeSignatureMalformed},
		{"a signature in unpadded base64 is malformed", set(HeaderSignature, signaturePrefix+strings.TrimRight(b64, "=")), CodeSignatureMalformed},
		{"a signature in URL-safe base64 is malformed", set(HeaderSignature, signaturePrefix+strings.NewReplacer("+", "-", "/", "_").Replace(b64)), CodeSignatureMalformed},
		{"a 63-byte signature is malformed", set(HeaderSignature, signaturePrefix+base64.StdEncoding.EncodeToString(make([]byte, 63))), CodeSignatureMalformed},
		{"a repeated signature is malformed", add(HeaderSignature, exampleHeader), CodeSignatureMalformed},
		{"with every header malformed, the Idempotency-Key is reported first", func(h http.Header) {
			for _, name := range []string{HeaderIdempotencyKey, HeaderReq, HeaderTime, HeaderBodySHA256, HeaderSignature} {
				h.Set(name, "x")
			}
		}, CodeIdempotencyKeyMalformed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := exampleHeaders()
			tt.edit(h)
			_, ref := parseEnvelope(h)
			got := ""
			if ref != nil {
				got = ref.code
			}
			if got != tt.want {
				t.Errorf("refusal = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestCheckContent(t *testing.T) {
	tests := []struct {
		name        string
		ctype, cenc string
		want        string
	}{
		{"application/json in gzip passes", "application/json", "gzip", ""},
		{"a utf-8 charset passes", "application/json; charset=UTF-8", "gzip", ""},
		{"text/plain is unsupported", "text/plain", "gzip", CodeContentTypeUnsupported},
		{"another charset is unsupported", "application/json; charset=latin1", "gzip", CodeContentTypeUnsupported},
		{"no Content-Type is unsupported", "", "gzip", CodeContentTypeUnsupported},
		{"an identity body is unsupported", "application/json", "", CodeEncodingUnsupported},
		{"deflate is unsupported", "application/json", "deflate", CodeEncodingUnsupported},
		{"two encodings are unsupported", "application/json", "gzip, gzip", CodeEncodingUnsupported},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := http.Header{}
			if tt.ctype != "" {
				h.Set("Content-Type", tt.ctype)
			}
			if tt.cenc != "" {
				h.Set("Content-Encoding", tt.cenc)
			}
			got := ""
			if ref := checkContent(h); ref != nil {
				got = ref.code
			}
			if got != tt.want {
				t.Errorf("refusal = %q, want %q", got, tt.want)
			}
		})
	}
}

// gzipped returns data as one gzip member.
func gzipped(t testing.TB, data []byte) []byte {
	t.Helper()
	var b bytes.Buffer
	zw, err := gzip.NewWriterLevel(&b, gzip.BestCompression)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := zw.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// bomb returns a gzip member of n zero bytes, written a megabyte at a time
// so the test never holds them.
func bomb(t testing.TB, n int) []byte {
	t.Helper()
	var b bytes.Buffer
	zw, err := gzip.NewWriterLevel(&b, gzip.BestCompression)
	if err != nil {
		t.Fatal(err)
	}
	chunk := make([]byte, 1<<20)
	for n > 0 {
		k := min(n, len(chunk))
		if _, err := zw.Write(chunk[:k]); err != nil {
			t.Fatal(err)
		}
		n -= k
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func TestInflate(t *testing.T) {
	one := gzipped(t, []byte(`{}`))
	tests := []struct {
		name string
		raw  []byte
		want string // the refusal's code, or "" when it inflates
		size int    // the inflated size, when it inflates
	}{
		{"one gzip member inflates", one, "", 2},
		{"a member of exactly 4 MiB inflates", bomb(t, MaxInflatedBody), "", MaxInflatedBody},
		{"a member one byte past 4 MiB is too large", bomb(t, MaxInflatedBody+1), CodeDecompressedTooLarge, 0},
		{"a byte after the member is refused", append(bytes.Clone(one), 0), CodeGzipInvalid, 0},
		{"a second member is refused", append(bytes.Clone(one), one...), CodeGzipInvalid, 0},
		{"a body that isn't gzip is refused", []byte(`{}`), CodeGzipInvalid, 0},
		{"a member cut short is refused", one[:len(one)-4], CodeGzipInvalid, 0},
		{"a member whose checksum is wrong is refused", func() []byte {
			b := bytes.Clone(one)
			b[len(b)-8] ^= 1
			return b
		}(), CodeGzipInvalid, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, ref := inflate(tt.raw)
			got := ""
			if ref != nil {
				got = ref.code
			}
			if got != tt.want {
				t.Fatalf("refusal = %q, want %q", got, tt.want)
			}
			if ref == nil && len(out) != tt.size {
				t.Errorf("inflated %d bytes, want %d", len(out), tt.size)
			}
		})
	}
}

// TestBombStopsEarly checks that a gzip bomb costs the server about the
// limit, not what the bomb would inflate to (ingest-v1 2.4).
func TestBombStopsEarly(t *testing.T) {
	tests := []struct {
		name     string
		inflates int
	}{
		{"a 64 MiB bomb stops at the 4 MiB limit", 64 << 20},
		{"a 128 MiB bomb stops at the 4 MiB limit", 128 << 20},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw := bomb(t, tt.inflates)
			if len(raw) > MaxRawBody {
				t.Fatalf("the bomb is %d bytes, over the raw limit, so it tests nothing", len(raw))
			}
			var before, after runtime.MemStats
			runtime.GC()
			runtime.ReadMemStats(&before)
			_, ref := inflate(raw)
			runtime.ReadMemStats(&after)
			if ref == nil || ref.code != CodeDecompressedTooLarge {
				t.Fatalf("refusal = %v, want %s", ref, CodeDecompressedTooLarge)
			}
			if grew := after.TotalAlloc - before.TotalAlloc; grew > 32<<20 {
				t.Errorf("inflating allocated %d bytes, more than 32 MiB for a 4 MiB limit: it didn't stop early", grew)
			}
		})
	}
}

// countingReader yields zeros for ever and counts how many it gave.
type countingReader struct{ n int }

func (c *countingReader) Read(p []byte) (int, error) {
	clear(p)
	c.n += len(p)
	return len(p), nil
}

func TestReadRaw(t *testing.T) {
	tests := []struct {
		name          string
		contentLength int64
		body          io.Reader
		want          string
		maxRead       int
	}{
		{"a Content-Length over 256 KiB is refused before anything is read", MaxRawBody + 1, &countingReader{}, CodeBodyTooLarge, 0},
		{"an endless body without a Content-Length is read one byte past the limit, then refused", -1, &countingReader{}, CodeBodyTooLarge, MaxRawBody + 1},
		{"a body of exactly 256 KiB is read", MaxRawBody, bytes.NewReader(make([]byte, MaxRawBody)), "", MaxRawBody},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, err := http.NewRequest(http.MethodPost, BatchesPath, tt.body)
			if err != nil {
				t.Fatal(err)
			}
			r.ContentLength = tt.contentLength
			raw, ref := readRaw(r)
			got := ""
			if ref != nil {
				got = ref.code
			}
			if got != tt.want {
				t.Fatalf("refusal = %q, want %q", got, tt.want)
			}
			if c, ok := tt.body.(*countingReader); ok && c.n > tt.maxRead {
				t.Errorf("read %d bytes, more than %d", c.n, tt.maxRead)
			}
			if ref == nil && len(raw) != tt.maxRead {
				t.Errorf("read a body of %d bytes, want %d", len(raw), tt.maxRead)
			}
		})
	}
}

// TestBodyCountStopsKeeping checks that a body of millions of tiny items,
// which a few KiB of gzip inflate to, is refused for its count at the item
// past the limit, having kept no more items than the limit (ingest-v1 2.4).
func TestBodyCountStopsKeeping(t *testing.T) {
	tests := []struct {
		name, member, want string
	}{
		{"two million buckets are too_many_buckets", "buckets", CodeTooManyBuckets},
		{"two million batch roots are too_many_batches", "batches", CodeTooManyBatches},
		{"two million commit records are too_many_commits", "commits", CodeTooManyCommits},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			n := (MaxInflatedBody - 32) / 2
			body := []byte(`{"` + tt.member + `":[` + strings.Repeat("0,", n-1) + `0]}`)
			if len(body) > MaxInflatedBody {
				t.Fatalf("the body is %d bytes, over the inflated limit, so it tests nothing", len(body))
			}
			var before, after runtime.MemStats
			runtime.GC()
			runtime.ReadMemStats(&before)
			_, ref := parseBody(body)
			runtime.ReadMemStats(&after)
			if ref == nil || ref.code != tt.want {
				t.Fatalf("refusal = %v, want %s", ref, tt.want)
			}
			if grew := after.TotalAlloc - before.TotalAlloc; grew > 16<<20 {
				t.Errorf("parsing allocated %d bytes for %d items, more than 16 MiB: it kept items past the limit", grew, n)
			}
		})
	}
}

func TestParseBody(t *testing.T) {
	items := func(name string, n int) string {
		return `{"` + name + `":[` + strings.TrimSuffix(strings.Repeat("0,", n), ",") + `]}`
	}
	tests := []struct {
		name string
		body string
		want string
	}{
		{"an empty object is a body", `{}`, ""},
		{"all four members are a body", `{"batches":[],"buckets":[],"commits":[],"labels":{}}`, ""},
		{"null members mean none", `{"batches":null,"buckets":null,"commits":null,"labels":null}`, ""},
		{"text that isn't JSON is json_invalid", `{"batches":[`, CodeJSONInvalid},
		{"invalid UTF-8 is json_invalid", "{\"labels\":{\"client_build\":\"\xff\"}}", CodeJSONInvalid},
		{"a second value after the object is json_invalid", `{} {}`, CodeJSONInvalid},
		{"an array is body_shape", `[]`, CodeBodyShape},
		{"a member outside ingest-v1 2.3 is body_shape", `{"events":[]}`, CodeBodyShape},
		{"batches as an object is body_shape", `{"batches":{}}`, CodeBodyShape},
		{"labels as an array is body_shape", `{"labels":[]}`, CodeBodyShape},
		{"buckets as a string is body_shape", `{"buckets":"[]"}`, CodeBodyShape},
		{"a member that appears twice is body_shape", `{"batches":[],"batches":[]}`, CodeBodyShape},
		{"a member spelled twice, once with an escape, is body_shape", `{"batches":[],"\u0062atches":[]}`, CodeBodyShape},
		{"labels twice are body_shape, even when both are empty", `{"labels":{},"labels":null}`, CodeBodyShape},
		{"members are read in order: one outside 2.3 before too many buckets is body_shape", `{"events":[],` + items("buckets", MaxBuckets+1)[1:], CodeBodyShape},
		{"members are read in order: too many buckets before one outside 2.3 are too_many_buckets", items("buckets", MaxBuckets+1)[:len(items("buckets", MaxBuckets+1))-1] + `,"events":[]}`, CodeTooManyBuckets},
		{"2,000 batch roots fit", items("batches", MaxBatches), ""},
		{"2,001 batch roots are too many", items("batches", MaxBatches+1), CodeTooManyBatches},
		{"2,000 buckets fit", items("buckets", MaxBuckets), ""},
		{"2,001 buckets are too many", items("buckets", MaxBuckets+1), CodeTooManyBuckets},
		{"500 commit records fit", items("commits", MaxCommits), ""},
		{"501 commit records are too many", items("commits", MaxCommits+1), CodeTooManyCommits},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, ref := parseBody([]byte(tt.body))
			got := ""
			if ref != nil {
				got = ref.code
			}
			if got != tt.want {
				t.Errorf("refusal = %q, want %q", got, tt.want)
			}
		})
	}
}
