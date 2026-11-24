// SPDX-License-Identifier: AGPL-3.0-or-later

package submission

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/some-du6e/pairtime/internal/server/store/storetest"
	"github.com/some-du6e/pairtime/internal/server/writer"
)

// storedR reads submission id's r straight from its row, as no read of the
// package does before the draw.
func (h *harness) storedR(id string) []byte {
	h.t.Helper()
	var r []byte
	if err := h.db.Read.QueryRowContext(context.Background(), `SELECT r FROM submissions WHERE id = $1`, id).Scan(&r); err != nil {
		h.t.Fatal(err)
	}
	return r
}

// leaks reports whether body holds r in any of the forms an answer could
// carry it in.
func leaks(body string, r []byte) bool {
	for _, form := range []string{
		string(r), hex.EncodeToString(r), strings.ToUpper(hex.EncodeToString(r)),
		base64.StdEncoding.EncodeToString(r), base64.RawStdEncoding.EncodeToString(r),
		base64.URLEncoding.EncodeToString(r), base64.RawURLEncoding.EncodeToString(r),
	} {
		if strings.Contains(body, form) {
			return true
		}
	}
	return false
}

// drawR runs DrawR in a writer op of its own, as WP-SB5's draw does.
func (h *harness) drawR(id string) ([]byte, error) {
	var r []byte
	err := h.w.Do(context.Background(), 1, func(ctx context.Context, tx writer.Tx) error {
		var err error
		r, err = DrawR(ctx, tx, id)
		return err
	})
	return r, err
}

// TestRCommitment is PLAN WP-SB1's check that r is unreadable through any
// endpoint until the draw is stored, which moves the submission to
// challenged, for a submission withdrawn at metadata_revealed too, and that
// it matches the commitment afterwards (D-sb1-submissions, Q9).
func TestRCommitment(t *testing.T) {
	start, end := t0.Add(-2*day), t0.Add(-day)
	// answers is every answer the package gives about submission id, from
	// each credential that can see it.
	answers := func(t *testing.T, h *harness, id string) map[string]string {
		t.Helper()
		out := make(map[string]string)
		for _, c := range []struct{ as, method, target, body string }{
			{"ptd_laptop", "POST", SubmissionsPath, window("hard-mode", "req-1", start, end)},
			{"ptd_laptop", "GET", SubmissionsPath + "/" + id, ""},
			{"ptd_desktop", "GET", SubmissionsPath + "/" + id, ""},
			{"sess:fresh", "GET", SubmissionsPath + "/" + id, ""},
			{"ptd_laptop", "GET", SubmissionsPath, ""},
			{"sess:fresh", "GET", SubmissionsPath + "?limit=1", ""},
			{"prog:hard-mode", "GET", SubmissionsPath + "/" + id, ""},
			{"prog:hard-mode", "GET", SubmissionsPath, ""},
			{"ptd_laptop", "GET", DevlogWindowsPath + "?project_key=k_app", ""},
			{"", "GET", ProgramsPath, ""},
		} {
			rec := h.call(c.as, c.method, c.target, c.body)
			out[c.as+" "+c.method+" "+c.target] = rec.Body.String() + "\n" + strings.Join(rec.Header().Values("Location"), " ")
		}
		exported, err := Export(context.Background(), h.db.Read, u1)
		if err != nil {
			t.Fatal(err)
		}
		b, err := json.Marshal(exported)
		if err != nil {
			t.Fatal(err)
		}
		out["Export"] = string(b)
		return out
	}
	tests := []struct {
		name string
		run  func(t *testing.T, h *harness)
	}{
		{
			name: "while frozen and once metadata_revealed, no answer of any route to any credential, and no export, holds r, and RevealedR refuses it",
			run: func(t *testing.T, h *harness) {
				v := h.create("ptd_laptop", window("hard-mode", "req-1", start, end), http.StatusCreated)
				r := h.storedR(v.ID)
				for _, state := range []State{Frozen, MetadataRevealed} {
					if state != Frozen {
						h.move(v.ID, state)
					}
					for what, body := range answers(t, h, v.ID) {
						if leaks(body, r) {
							t.Errorf("%s: %s holds r: %s", state, what, body)
						}
					}
					if got, err := RevealedR(context.Background(), h.db.Read, v.ID); !errors.Is(err, ErrNotRevealed) {
						t.Errorf("%s: RevealedR = %x, %v; want ErrNotRevealed", state, got, err)
					}
				}
			},
		},
		{
			name: "DrawR gives r inside the draw's op only while the submission is metadata_revealed",
			run: func(t *testing.T, h *harness) {
				v := h.create("ptd_laptop", window("hard-mode", "req-1", start, end), http.StatusCreated)
				r := h.storedR(v.ID)
				if _, err := h.drawR(v.ID); !errors.Is(err, ErrNotRevealed) {
					t.Errorf("frozen: DrawR = %v, want ErrNotRevealed", err)
				}
				h.move(v.ID, MetadataRevealed)
				if got, err := h.drawR(v.ID); err != nil || !bytes.Equal(got, r) {
					t.Errorf("metadata_revealed: DrawR = %x, %v; want the stored r", got, err)
				}
				h.move(v.ID, Challenged)
				if _, err := h.drawR(v.ID); !errors.Is(err, ErrNotRevealed) {
					t.Errorf("challenged: DrawR = %v, want ErrNotRevealed", err)
				}
				if _, err := h.drawR("s-none"); !errors.Is(err, ErrNotFound) {
					t.Errorf("an unknown id: DrawR = %v, want ErrNotFound", err)
				}
			},
		},
		{
			name: "once the draw is stored the teen view and the export show r, whose SHA-256 is the commitment the freeze answered with, and the program view never does",
			run: func(t *testing.T, h *harness) {
				v := h.create("ptd_laptop", window("hard-mode", "req-1", start, end), http.StatusCreated)
				r := h.storedR(v.ID)
				h.move(v.ID, MetadataRevealed, Challenged)
				got := view(t, h.call("sess:fresh", "GET", SubmissionsPath+"/"+v.ID, ""))
				sum := sha256.Sum256(r)
				if len(r) != 32 || got.R != hex.EncodeToString(r) || hex.EncodeToString(sum[:]) != *v.RSHA256 || got.ChallengedAt == nil {
					t.Errorf("the view's r %q and challenged_at %v, the freeze's r_sha256 %s; want r, whose SHA-256 is it", got.R, got.ChallengedAt, *v.RSHA256)
				}
				for what, body := range answers(t, h, v.ID) {
					switch {
					case strings.HasPrefix(what, "prog:") && leaks(body, r):
						t.Errorf("%s holds r: %s", what, body)
					case what == "Export" && !strings.Contains(body, hex.EncodeToString(r)):
						t.Errorf("the export doesn't hold r: %s", body)
					}
				}
				h.move(v.ID, ContentRevealed)
				want(t, h.call("ptd_laptop", "POST", SubmissionsPath+"/"+v.ID+"/withdraw", ""), http.StatusOK, "")
				if got := view(t, h.call("ptd_laptop", "GET", SubmissionsPath+"/"+v.ID, "")); got.R != hex.EncodeToString(r) {
					t.Errorf("after a withdrawal from content_revealed the view's r is %q, want r: the draw was stored", got.R)
				}
			},
		},
		{
			name: "a submission withdrawn at metadata_revealed never shows r",
			run: func(t *testing.T, h *harness) {
				v := h.create("ptd_laptop", window("hard-mode", "req-1", start, end), http.StatusCreated)
				r := h.storedR(v.ID)
				h.move(v.ID, MetadataRevealed)
				rec := h.call("ptd_laptop", "POST", SubmissionsPath+"/"+v.ID+"/withdraw", "")
				want(t, rec, http.StatusOK, "")
				if leaks(rec.Body.String(), r) {
					t.Errorf("the withdrawal's answer holds r: %s", rec.Body.String())
				}
				for what, body := range answers(t, h, v.ID) {
					if leaks(body, r) {
						t.Errorf("%s holds r: %s", what, body)
					}
				}
				if _, err := RevealedR(context.Background(), h.db.Read, v.ID); !errors.Is(err, ErrNotRevealed) {
					t.Errorf("RevealedR = %v, want ErrNotRevealed", err)
				}
				if _, err := h.drawR(v.ID); !errors.Is(err, ErrNotRevealed) {
					t.Errorf("DrawR = %v, want ErrNotRevealed", err)
				}
			},
		},
		{
			name: "two freezes draw two different 32-byte values",
			run: func(t *testing.T, h *harness) {
				one := h.create("ptd_laptop", window("hard-mode", "req-1", start, end), http.StatusCreated)
				two := h.create("ptd_laptop", window("hard-mode", "req-2", end, t0), http.StatusCreated)
				r1, r2 := h.storedR(one.ID), h.storedR(two.ID)
				if len(r1) != 32 || len(r2) != 32 || bytes.Equal(r1, r2) || *one.RSHA256 == *two.RSHA256 {
					t.Errorf("r %x and %x, commitments %s and %s; want two different 32-byte values", r1, r2, *one.RSHA256, *two.RSHA256)
				}
			},
		},
	}
	for _, engine := range storetest.Engines() {
		for _, tt := range tests {
			t.Run(string(engine)+"/"+tt.name, func(t *testing.T) { tt.run(t, newHarness(t, engine)) })
		}
	}
}
