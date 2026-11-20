// SPDX-License-Identifier: AGPL-3.0-or-later

package sigv4

import (
	"crypto/hmac"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"
)

// ErrSignature is the error Verify wraps when a request isn't signed by
// the credentials it names.
var ErrSignature = errors.New("sigv4: signature does not match")

// Verify checks req's Authorization header the way an S3 server does: the
// credential scope must name creds' access key, region and service on the
// day of X-Amz-Date, X-Amz-Date must be within maxSkew of now, host and
// X-Amz-Date must be signed, and the signature recomputed from the request
// as received, over the headers its SignedHeaders lists, must match.
// payloadHash is the hash the server holds the body to, normally the
// X-Amz-Content-Sha256 header once the server has checked it against the
// body. Paths are not normalized, as S3 doesn't.
//
// pairtime's test servers use it; pairtimed itself only signs.
func Verify(req *http.Request, payloadHash string, creds Credentials, region, service string, now time.Time, maxSkew time.Duration) error {
	fields, ok := strings.CutPrefix(req.Header.Get("Authorization"), Algorithm+" ")
	if !ok {
		return fmt.Errorf("%w: no %s Authorization header", ErrSignature, Algorithm)
	}
	var credential, signedList, signature string
	for f := range strings.SplitSeq(fields, ",") {
		k, v, _ := strings.Cut(strings.TrimSpace(f), "=")
		switch k {
		case "Credential":
			credential = v
		case "SignedHeaders":
			signedList = v
		case "Signature":
			signature = v
		}
	}
	scope := strings.Split(credential, "/")
	if len(scope) != 5 || scope[4] != "aws4_request" {
		return fmt.Errorf("%w: malformed credential scope", ErrSignature)
	}
	if !hmac.Equal([]byte(scope[0]), []byte(creds.AccessKeyID)) {
		return fmt.Errorf("%w: unknown access key id", ErrSignature)
	}
	if scope[2] != region || scope[3] != service {
		return fmt.Errorf("%w: the scope names region %q and service %q, not %q and %q", ErrSignature, scope[2], scope[3], region, service)
	}
	t, err := time.Parse(amzDateFormat, req.Header.Get("X-Amz-Date"))
	if err != nil {
		return fmt.Errorf("%w: missing or malformed X-Amz-Date", ErrSignature)
	}
	if scope[1] != t.Format(dateFormat) {
		return fmt.Errorf("%w: the scope's date isn't X-Amz-Date's", ErrSignature)
	}
	if d := now.Sub(t); d > maxSkew || d < -maxSkew {
		return fmt.Errorf("%w: X-Amz-Date is %s from the server's clock, more than %s", ErrSignature, d.Round(time.Second), maxSkew)
	}
	names := strings.Split(signedList, ";")
	if !slices.IsSorted(names) || len(slices.Compact(slices.Clone(names))) != len(names) {
		return fmt.Errorf("%w: SignedHeaders must be sorted and without repeats", ErrSignature)
	}
	for _, n := range names {
		if n == "" || n != strings.ToLower(n) || n == "authorization" {
			return fmt.Errorf("%w: SignedHeaders lists %q", ErrSignature, n)
		}
	}
	if !slices.Contains(names, "host") || !slices.Contains(names, "x-amz-date") {
		return fmt.Errorf("%w: host and x-amz-date must be signed", ErrSignature)
	}
	query, err := parseQuery(req.URL.RawQuery)
	if err != nil {
		return err
	}
	s := Signer{Credentials: creds, Region: region, Service: service}
	want := s.signNamed(req, names, query, payloadHash, t)
	if !hmac.Equal([]byte(signature), []byte(want.signature)) {
		return ErrSignature
	}
	return nil
}
