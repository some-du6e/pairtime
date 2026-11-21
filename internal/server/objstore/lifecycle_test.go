// SPDX-License-Identifier: AGPL-3.0-or-later

package objstore

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/internal/server/objstore/objstoretest"
)

// r2AbortRule is the rule R2 gives every new bucket, as R2's documentation
// shows it: it aborts multipart uploads that never finished and deletes no
// object.
const r2AbortRule = `<Rule><ID>Default Multipart Abort Rule</ID><Filter><Prefix></Prefix></Filter><Status>Enabled</Status><AbortIncompleteMultipartUpload><DaysAfterInitiation>7</DaysAfterInitiation></AbortIncompleteMultipartUpload></Rule>`

func TestLifecycle(t *testing.T) {
	date := time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)
	tests := []struct {
		name  string
		rules string
		want  []LifecycleRule
		// wantExpires and wantCovers are each rule's Expires and
		// Covers(SnapshotPrefix).
		wantExpires, wantCovers []bool
		wantIs                  error
		wantStatus              int
	}{
		{name: "a bucket without a lifecycle configuration has no rules", rules: ""},
		{name: "R2's default rule aborts unfinished uploads and deletes nothing", rules: r2AbortRule,
			want:        []LifecycleRule{{ID: "Default Multipart Abort Rule", Enabled: true, AbortUploadDays: 7}},
			wantExpires: []bool{false}, wantCovers: []bool{true}},
		{name: "a rule that deletes snapshots after 30 days covers every snapshot",
			rules:       `<Rule><ID>snapshots</ID><Filter><Prefix>snapshots/</Prefix></Filter><Status>Enabled</Status><Expiration><Days>30</Days></Expiration></Rule>`,
			want:        []LifecycleRule{{ID: "snapshots", Enabled: true, Prefix: "snapshots/", ExpireDays: 30}},
			wantExpires: []bool{true}, wantCovers: []bool{true}},
		{name: "a disabled rule deletes nothing",
			rules:       `<Rule><ID>off</ID><Filter><Prefix>snapshots/</Prefix></Filter><Status>Disabled</Status><Expiration><Days>30</Days></Expiration></Rule>`,
			want:        []LifecycleRule{{ID: "off", Prefix: "snapshots/", ExpireDays: 30}},
			wantExpires: []bool{false}, wantCovers: []bool{true}},
		{name: "a rule narrowed by a tag inside And doesn't cover every key under its prefix",
			rules:       `<Rule><ID>tagged</ID><Filter><And><Prefix>snapshots/</Prefix><Tag><Key>k</Key><Value>v</Value></Tag></And></Filter><Status>Enabled</Status><Expiration><Days>30</Days></Expiration></Rule>`,
			want:        []LifecycleRule{{ID: "tagged", Enabled: true, Prefix: "snapshots/", Narrowed: true, ExpireDays: 30}},
			wantExpires: []bool{true}, wantCovers: []bool{false}},
		{name: "a rule narrowed by object size doesn't cover every key either",
			rules:       `<Rule><ID>big</ID><Filter><ObjectSizeGreaterThan>1048576</ObjectSizeGreaterThan></Filter><Status>Enabled</Status><Expiration><Days>1</Days></Expiration></Rule>`,
			want:        []LifecycleRule{{ID: "big", Enabled: true, Narrowed: true, ExpireDays: 1}},
			wantExpires: []bool{true}, wantCovers: []bool{false}},
		{name: "the deprecated Prefix beside the filter names the rule's prefix",
			rules:       `<Rule><ID>old</ID><Prefix>evidence/</Prefix><Status>Enabled</Status><Expiration><Date>2027-01-01T00:00:00.000Z</Date></Expiration></Rule>`,
			want:        []LifecycleRule{{ID: "old", Enabled: true, Prefix: "evidence/", ExpireDate: date}},
			wantExpires: []bool{true}, wantCovers: []bool{false}},
		{name: "an expiration only of delete markers deletes no object",
			rules:       `<Rule><ID>markers</ID><Filter></Filter><Status>Enabled</Status><Expiration><ExpiredObjectDeleteMarker>true</ExpiredObjectDeleteMarker></Expiration></Rule>`,
			want:        []LifecycleRule{{ID: "markers", Enabled: true}},
			wantExpires: []bool{false}, wantCovers: []bool{true}},
		{name: "an expiration date that doesn't read fails rather than passing as no expiration",
			rules: `<Rule><ID>bad</ID><Filter></Filter><Status>Enabled</Status><Expiration><Date>soon</Date></Expiration></Rule>`, wantStatus: http.StatusOK},
		{name: "a store without GetBucketLifecycleConfiguration fails with ErrNotImplemented", rules: objstoretest.NotImplemented, wantIs: ErrNotImplemented},
		{name: "a key that may not read the configuration fails with the store's 403", rules: objstoretest.AccessDenied, wantStatus: http.StatusForbidden},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := objstoretest.New(t, mainBucket)
			srv.SetLifecycle(mainBucket, tt.rules)
			b := bucket(t, newTestClient(t, srv), mainBucket)
			got, err := b.Lifecycle(context.Background())
			var e *Error
			switch {
			case tt.wantIs != nil:
				if !errors.Is(err, tt.wantIs) {
					t.Fatalf("error = %v, want %v", err, tt.wantIs)
				}
				return
			case tt.wantStatus != 0:
				if !errors.As(err, &e) || e.Status != tt.wantStatus {
					t.Fatalf("error = %v, want one with status %d", err, tt.wantStatus)
				}
				return
			case err != nil:
				t.Fatal(err)
			}
			if len(got) != len(tt.want) {
				t.Fatalf("rules = %+v, want %+v", got, tt.want)
			}
			for i := range got {
				g, w := got[i], tt.want[i]
				if !g.ExpireDate.Equal(w.ExpireDate) {
					t.Errorf("rule %q expires on %s, want %s", g.ID, g.ExpireDate, w.ExpireDate)
				}
				g.ExpireDate, w.ExpireDate = time.Time{}, time.Time{}
				if !reflect.DeepEqual(g, w) {
					t.Errorf("rule %d = %+v, want %+v", i, g, w)
				}
			}
			for i, r := range got {
				if i < len(tt.wantExpires) && r.Expires() != tt.wantExpires[i] {
					t.Errorf("rule %q: Expires = %v, want %v", r.ID, r.Expires(), tt.wantExpires[i])
				}
				if i < len(tt.wantCovers) && r.Covers(SnapshotPrefix) != tt.wantCovers[i] {
					t.Errorf("rule %q: Covers(%s) = %v, want %v", r.ID, SnapshotPrefix, r.Covers(SnapshotPrefix), tt.wantCovers[i])
				}
			}
		})
	}
}
