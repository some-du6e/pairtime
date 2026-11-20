// SPDX-License-Identifier: AGPL-3.0-or-later

package ingest

import "net/http"

// The codes of a request refused as a whole, ingest-v1 2.6, each with its
// status in refusals.
const (
	CodeIngestOff               = "ingest_off"
	CodeTokenMissing            = "token_missing"
	CodeTokenInvalid            = "token_invalid"
	CodeDeviceRevoked           = "device_revoked"
	CodeDeviceMismatch          = "device_mismatch"
	CodeDeviceKeyMissing        = "device_key_missing"
	CodeIdempotencyKeyMalformed = "idempotency_key_malformed"
	CodeCounterMalformed        = "counter_malformed"
	CodeTimeMalformed           = "time_malformed"
	CodeBodyHashMalformed       = "body_hash_malformed"
	CodeSignatureMalformed      = "signature_malformed"
	CodeContentTypeUnsupported  = "content_type_unsupported"
	CodeEncodingUnsupported     = "encoding_unsupported"
	CodeClockSkew               = "clock_skew"
	CodeBodyTooLarge            = "body_too_large"
	CodeBodyUnreadable          = "body_unreadable"
	CodeBodyHashMismatch        = "body_hash_mismatch"
	CodeSignatureInvalid        = "signature_invalid"
	CodeGzipInvalid             = "gzip_invalid"
	CodeDecompressedTooLarge    = "decompressed_too_large"
	CodeJSONInvalid             = "json_invalid"
	CodeBodyShape               = "body_shape"
	CodeTooManyBatches          = "too_many_batches"
	CodeTooManyBuckets          = "too_many_buckets"
	CodeTooManyCommits          = "too_many_commits"
	CodeIdempotencyKeyReused    = "idempotency_key_reused"
	CodeCounterStale            = "counter_stale"
	CodeUnavailable             = "unavailable"
)

// refusals gives each request code its status.
var refusals = map[string]int{
	CodeIngestOff:               http.StatusServiceUnavailable,
	CodeTokenMissing:            http.StatusUnauthorized,
	CodeTokenInvalid:            http.StatusUnauthorized,
	CodeDeviceRevoked:           http.StatusForbidden,
	CodeDeviceMismatch:          http.StatusForbidden,
	CodeDeviceKeyMissing:        http.StatusForbidden,
	CodeIdempotencyKeyMalformed: http.StatusBadRequest,
	CodeCounterMalformed:        http.StatusBadRequest,
	CodeTimeMalformed:           http.StatusBadRequest,
	CodeBodyHashMalformed:       http.StatusBadRequest,
	CodeSignatureMalformed:      http.StatusBadRequest,
	CodeContentTypeUnsupported:  http.StatusUnsupportedMediaType,
	CodeEncodingUnsupported:     http.StatusUnsupportedMediaType,
	CodeClockSkew:               http.StatusUnauthorized,
	CodeBodyTooLarge:            http.StatusRequestEntityTooLarge,
	CodeBodyUnreadable:          http.StatusBadRequest,
	CodeBodyHashMismatch:        http.StatusBadRequest,
	CodeSignatureInvalid:        http.StatusUnauthorized,
	CodeGzipInvalid:             http.StatusBadRequest,
	CodeDecompressedTooLarge:    http.StatusRequestEntityTooLarge,
	CodeJSONInvalid:             http.StatusBadRequest,
	CodeBodyShape:               http.StatusUnprocessableEntity,
	CodeTooManyBatches:          http.StatusRequestEntityTooLarge,
	CodeTooManyBuckets:          http.StatusRequestEntityTooLarge,
	CodeTooManyCommits:          http.StatusRequestEntityTooLarge,
	CodeIdempotencyKeyReused:    http.StatusUnprocessableEntity,
	CodeCounterStale:            http.StatusConflict,
	CodeUnavailable:             http.StatusServiceUnavailable,
}

// The codes of one item's result, ingest-v1 2.9. The shape codes come
// first; every one of them has status 422 and names a field.
const (
	CodeNotObject    = "not_object"
	CodeMissingField = "missing_field"
	CodeUnknownField = "unknown_field"
	CodeBadType      = "bad_type"
	CodeBadString    = "bad_string"
	CodeBadNumber    = "bad_number"
	CodeTooMany      = "too_many"

	CodeAccepted  = "accepted"
	CodeDuplicate = "duplicate"

	CodeBatchEmpty    = "batch_empty"
	CodeHeadMismatch  = "head_mismatch"
	CodeBatchConflict = "batch_conflict"
	CodePrevMismatch  = "prev_mismatch"
	CodeChainGap      = "chain_gap"
	CodeBeforeChain   = "before_chain"

	CodeSuperseded    = "superseded"
	CodeRevConflict   = "rev_conflict"
	CodeBadRule       = "bad_rule"
	CodeWrongDevice   = "wrong_device"
	CodeFutureMinute  = "future_minute"
	CodeExpiredMinute = "expired_minute"

	CodeUpdated = "updated"
)

// Limits per request, ingest-v1 2.4 (DESIGN 4.5).
const (
	MaxRawBody      = 256 << 10
	MaxInflatedBody = 4 << 20
	MaxBatches      = 2000
	MaxBuckets      = 2000
	MaxCommits      = 500
)

// The clock rules: a request's time and a bucket's minute may be at most
// MaxSkew from the server's clock, and a minute at most BucketHorizon old
// (ingest-v1 2.5 step 7, 2.7; DESIGN 4.4, 4.5).
const (
	MaxSkewMS       = 300_000
	BucketHorizonMS = 180 * 86_400_000
)
