// SPDX-License-Identifier: AGPL-3.0-or-later

package params

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"slices"
	"strconv"
	"strings"
)

// The canonical form of a JSON document, whose SHA-256 is a table's hash
// (SM 7.6): object keys sorted bytewise at every level, no whitespace, and
// every number exactly as written. jq -cS writes the same bytes, plus a
// final newline. decode refuses what would make the canonical form
// ambiguous or what jq might write differently:
//
//   - a key that appears twice in one object;
//   - a number that isn't a plain decimal: an exponent, a zero after the
//     last nonzero digit of the fraction, or -0, so that a number's text
//     follows from its value;
//   - a string with a character outside printable ASCII, so that '"' and
//     '\' are the only characters that need escaping.

// decode parses one JSON document into maps (map[string]any), slices
// ([]any), strings, json.Number values, bools and nil.
func decode(data []byte) (any, error) {
	if len(bytes.TrimSpace(data)) == 0 {
		return nil, errors.New("no JSON document")
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	v, err := decodeValue(dec)
	if err != nil {
		return nil, err
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		if err == nil {
			return nil, errors.New("more than one JSON document")
		}
		return nil, err
	}
	return v, nil
}

// nextToken reads the next token. Input that ends inside a document is an
// error, not the end of the stream.
func nextToken(dec *json.Decoder) (json.Token, error) {
	t, err := dec.Token()
	if errors.Is(err, io.EOF) {
		err = io.ErrUnexpectedEOF
	}
	return t, err
}

func decodeValue(dec *json.Decoder) (any, error) {
	tok, err := nextToken(dec)
	if err != nil {
		return nil, err
	}
	switch tok := tok.(type) {
	case json.Delim:
		if tok == '[' {
			list := []any{}
			for dec.More() {
				v, err := decodeValue(dec)
				if err != nil {
					return nil, err
				}
				list = append(list, v)
			}
			_, err := nextToken(dec) // ]
			return list, err
		}
		obj := make(map[string]any)
		for dec.More() {
			kt, err := nextToken(dec)
			if err != nil {
				return nil, err
			}
			k, ok := kt.(string)
			if !ok {
				return nil, fmt.Errorf("object key %v isn't a string", kt)
			}
			if err := checkString(k); err != nil {
				return nil, fmt.Errorf("key %q: %w", k, err)
			}
			if _, dup := obj[k]; dup {
				return nil, fmt.Errorf("key %q appears twice in one object", k)
			}
			v, err := decodeValue(dec)
			if err != nil {
				return nil, err
			}
			obj[k] = v
		}
		_, err := nextToken(dec) // }
		return obj, err
	case string:
		if err := checkString(tok); err != nil {
			return nil, fmt.Errorf("string %q: %w", tok, err)
		}
		return tok, nil
	case json.Number:
		if !plainDecimal(string(tok)) {
			return nil, fmt.Errorf("number %s must be a plain decimal: no exponent, no trailing zeros after the point, no -0", tok)
		}
		return tok, nil
	default: // bool or nil
		return tok, nil
	}
}

// checkString accepts printable ASCII only.
func checkString(s string) error {
	for i := 0; i < len(s); i++ {
		if c := s[i]; c < 0x20 || c > 0x7e {
			return fmt.Errorf("has byte %#04x at offset %d; only printable ASCII is allowed", c, i)
		}
	}
	return nil
}

// plainDecimal reports whether s, a valid JSON number, is written the one
// way its value allows: an optional minus sign, an integer part without
// leading zeros, and an optional fraction that doesn't end in 0. It rejects
// exponents and -0.
func plainDecimal(s string) bool {
	if rest, neg := strings.CutPrefix(s, "-"); neg {
		if rest == "0" {
			return false
		}
		s = rest
	}
	whole, frac, dot := strings.Cut(s, ".")
	if !isDigits(whole) || (len(whole) > 1 && whole[0] == '0') {
		return false
	}
	return !dot || (isDigits(frac) && frac[len(frac)-1] != '0')
}

// isDigits reports whether s is one or more ASCII digits.
func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// appendCanonical appends the canonical form of v, a value decode
// returned, to b.
func appendCanonical(b []byte, v any) []byte {
	switch v := v.(type) {
	case map[string]any:
		b = append(b, '{')
		for i, k := range slices.Sorted(maps.Keys(v)) {
			if i > 0 {
				b = append(b, ',')
			}
			b = appendString(b, k)
			b = append(b, ':')
			b = appendCanonical(b, v[k])
		}
		return append(b, '}')
	case []any:
		b = append(b, '[')
		for i, e := range v {
			if i > 0 {
				b = append(b, ',')
			}
			b = appendCanonical(b, e)
		}
		return append(b, ']')
	case string:
		return appendString(b, v)
	case json.Number:
		return append(b, string(v)...)
	case bool:
		return strconv.AppendBool(b, v)
	case nil:
		return append(b, "null"...)
	}
	panic(fmt.Sprintf("params: appendCanonical of %T", v))
}

// appendString appends s, which checkString accepted, as a JSON string.
func appendString(b []byte, s string) []byte {
	b = append(b, '"')
	for i := 0; i < len(s); i++ {
		if s[i] == '"' || s[i] == '\\' {
			b = append(b, '\\')
		}
		b = append(b, s[i])
	}
	return append(b, '"')
}
