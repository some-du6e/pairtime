// SPDX-License-Identifier: AGPL-3.0-or-later

package objstore

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// LifecycleRule is one rule of a bucket's lifecycle configuration, as
// GetBucketLifecycleConfiguration reports it (S3 API reference). pairtimed
// never sets one. The operator does, when the buckets are made (DESIGN 10.5
// item 4): none that deletes objects in the main bucket, and one in the
// snapshot bucket that deletes old snapshots. pairtimed doctor reads them.
type LifecycleRule struct {
	// ID names the rule.
	ID string
	// Enabled says the rule's Status is Enabled. Only an enabled rule acts.
	Enabled bool
	// Prefix is the key prefix the rule's filter names, or the deprecated
	// Prefix element beside it; "" covers the whole bucket.
	Prefix string
	// Narrowed says the filter also names tags or object sizes, so the rule
	// may pass over keys under Prefix.
	Narrowed bool
	// ExpireDays and ExpireDate are its Expiration's Days and Date when it
	// deletes objects, and zero otherwise.
	ExpireDays int
	ExpireDate time.Time
	// AbortUploadDays is its AbortIncompleteMultipartUpload's
	// DaysAfterInitiation, or 0. That action deletes no object, only the
	// parts of uploads that never finished.
	AbortUploadDays int
}

// Expires reports whether the rule deletes objects: it is enabled, and its
// Expiration has a number of days or a date.
func (r LifecycleRule) Expires() bool {
	return r.Enabled && (r.ExpireDays > 0 || !r.ExpireDate.IsZero())
}

// Covers reports whether the rule applies to every key that starts with
// prefix.
func (r LifecycleRule) Covers(prefix string) bool {
	return !r.Narrowed && strings.HasPrefix(prefix, r.Prefix)
}

// lifecycleFilter is a rule's Filter, or the And inside one: S3 allows one
// of Prefix, Tag and the object sizes directly, and several inside And.
type lifecycleFilter struct {
	Prefix                *string    `xml:"Prefix"`
	Tags                  []struct{} `xml:"Tag"`
	ObjectSizeGreaterThan *string    `xml:"ObjectSizeGreaterThan"`
	ObjectSizeLessThan    *string    `xml:"ObjectSizeLessThan"`
	And                   *lifecycleFilter
}

// narrowed reports whether the filter names more than a prefix.
func (f *lifecycleFilter) narrowed() bool {
	if f == nil {
		return false
	}
	return len(f.Tags) > 0 || f.ObjectSizeGreaterThan != nil || f.ObjectSizeLessThan != nil || f.And.narrowed()
}

// prefix is the prefix the filter names, in it or in its And.
func (f *lifecycleFilter) prefix() *string {
	switch {
	case f == nil:
		return nil
	case f.Prefix != nil:
		return f.Prefix
	default:
		return f.And.prefix()
	}
}

// Lifecycle returns the bucket's lifecycle rules: none when it has no
// lifecycle configuration, which S3 answers with 404
// NoSuchLifecycleConfiguration. A store without the call fails with
// ErrNotImplemented.
func (b *Bucket) Lifecycle(ctx context.Context) ([]LifecycleRule, error) {
	const op = "GetBucketLifecycleConfiguration"
	resp, err := b.c.do(ctx, request{op: op, method: http.MethodGet, bucket: b.name, rawQuery: "lifecycle"})
	var e *Error
	if errors.As(err, &e) && e.Status == http.StatusNotFound && e.Code == "NoSuchLifecycleConfiguration" {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var doc struct {
		Rules []struct {
			ID         string           `xml:"ID"`
			Status     string           `xml:"Status"`
			Prefix     *string          `xml:"Prefix"`
			Filter     *lifecycleFilter `xml:"Filter"`
			Expiration *struct {
				Days int    `xml:"Days"`
				Date string `xml:"Date"`
			} `xml:"Expiration"`
			Abort *struct {
				Days int `xml:"DaysAfterInitiation"`
			} `xml:"AbortIncompleteMultipartUpload"`
		} `xml:"Rule"`
	}
	fail := func(err error) error {
		return &Error{Op: op, Bucket: b.name, Status: resp.StatusCode, Err: err}
	}
	if err := xml.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&doc); err != nil {
		return nil, fail(fmt.Errorf("reading the answer: %w", err))
	}
	rules := make([]LifecycleRule, 0, len(doc.Rules))
	for _, d := range doc.Rules {
		r := LifecycleRule{ID: printable(d.ID, 100), Enabled: d.Status == "Enabled", Narrowed: d.Filter.narrowed()}
		if p := d.Filter.prefix(); p != nil {
			r.Prefix = *p
		} else if d.Prefix != nil {
			r.Prefix = *d.Prefix
		}
		if x := d.Expiration; x != nil {
			r.ExpireDays = x.Days
			if x.Date != "" {
				t, err := time.Parse(time.RFC3339, x.Date)
				if err != nil {
					return nil, fail(fmt.Errorf("rule %q expires objects on a date that doesn't read: %q", r.ID, printable(x.Date, 40)))
				}
				r.ExpireDate = t
			}
		}
		if d.Abort != nil {
			r.AbortUploadDays = d.Abort.Days
		}
		rules = append(rules, r)
	}
	return rules, nil
}
