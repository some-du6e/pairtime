// SPDX-License-Identifier: AGPL-3.0-or-later

package submission

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// TestRefusalStore checks what the store of signed writes' refusals keeps,
// for how long and how many (docs/spec/submissions-v1.md 2.2).
func TestRefusalStore(t *testing.T) {
	now := t0.UnixMilli()
	tests := []struct {
		name string
		run  func(t *testing.T)
	}{
		{
			name: "a kept refusal holds its status and error code alone, so its size doesn't grow with the overlap list it named",
			run: func(t *testing.T) {
				s := &Service{o: Options{Now: func() time.Time { return t0 }}}
				for _, n := range []int{1, 1000} {
					key := fmt.Sprintf("d1 sig-%d", n)
					refs := make([]Ref, n)
					for i := range refs {
						refs[i] = Ref{ID: fmt.Sprintf("s_%04d", i), Program: "hard-mode"}
					}
					s.keepRefusals(func(w http.ResponseWriter, r *http.Request) {
						rec := w.(*recorder)
						rec.key, rec.untilMS = key, now+1000
						ref := refuse(http.StatusConflict, CodeAlreadySubmitted, "The windows overlap.")
						ref.submissions = refs
						s.refuse(w, ref)
					})(httptest.NewRecorder(), httptest.NewRequest("POST", SubmissionsPath, nil))
					if got, wantKept := s.refused.kept[key], (keptRefusal{status: http.StatusConflict, code: CodeAlreadySubmitted, untilMS: now + 1000}); got != wantKept {
						t.Errorf("with %d overlaps the store keeps %+v, want %+v", n, got, wantKept)
					}
				}
			},
		},
		{
			name: "the store holds at most maxKeptRefusals, dropping the oldest first",
			run: func(t *testing.T) {
				var rs refusalStore
				for i := range maxKeptRefusals + 1 {
					rs.put(fmt.Sprintf("d1 sig-%d", i), keptRefusal{status: http.StatusBadRequest, code: CodeProgramUnknown, untilMS: now + 1000}, now)
				}
				if len(rs.kept) != maxKeptRefusals {
					t.Errorf("the store holds %d refusals, want %d", len(rs.kept), maxKeptRefusals)
				}
				if _, ok := rs.get("d1 sig-0", now); ok {
					t.Error("the oldest refusal is still kept past the bound")
				}
				if _, ok := rs.get(fmt.Sprintf("d1 sig-%d", maxKeptRefusals), now); !ok {
					t.Error("the newest refusal isn't kept")
				}
			},
		},
		{
			name: "another request's later store call leaves an entry that a copy which passed its clock check before the entry's time still asks for",
			run: func(t *testing.T) {
				var rs refusalStore
				rs.put("d1 sig", keptRefusal{status: http.StatusConflict, code: CodeAlreadySubmitted, untilMS: now + 1000}, now)
				rs.get("d2 sig", now+6000)
				if _, ok := rs.get("d1 sig", now+999); !ok {
					t.Error("a copy whose clock check passed at 999 ms lost the entry to a later call's expiry")
				}
				rs.get("d2 sig", now+1000+keepGraceMS)
				if _, ok := rs.kept["d1 sig"]; ok {
					t.Error("the entry outlived its time and keepGraceMS")
				}
			},
		},
		{
			name: "a refusal is kept until the millisecond before its untilMS, the first in which a copy fails the clock check",
			run: func(t *testing.T) {
				var rs refusalStore
				rs.put("d1 sig", keptRefusal{status: http.StatusBadRequest, code: CodeProgramUnknown, untilMS: now + 1000}, now)
				if _, ok := rs.get("d1 sig", now+999); !ok {
					t.Error("the refusal is gone a millisecond before its time")
				}
				if _, ok := rs.get("d1 sig", now+1000); ok {
					t.Error("the refusal is still kept at its time")
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, tt.run)
	}
}
