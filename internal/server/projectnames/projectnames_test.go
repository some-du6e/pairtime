// SPDX-License-Identifier: AGPL-3.0-or-later

package projectnames_test

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/internal/server/migrations"
	"github.com/some-du6e/pairtime/internal/server/projectnames"
	"github.com/some-du6e/pairtime/internal/server/store"
	"github.com/some-du6e/pairtime/internal/server/store/storetest"
	"github.com/some-du6e/pairtime/internal/server/writer"
)

// fixture is a migrated database with accounts u1 and u2, the project
// P1 of u1 on devices d1 and d2 (keys k1 and k2), and P2 of u1 on d1 (k3).
type fixture struct {
	db *store.DB
	w  *writer.Writer
}

func newFixture(t *testing.T, engine store.Engine) *fixture {
	t.Helper()
	ctx := context.Background()
	db := storetest.Open(t, engine)
	ms, err := migrations.Embedded()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := migrations.Apply(ctx, db, ms, time.Now); err != nil {
		t.Fatal(err)
	}
	w := writer.Start(db.Write, writer.Options{Window: time.Millisecond})
	t.Cleanup(w.Close)
	f := &fixture{db: db, w: w}
	for _, u := range []string{"u1", "u2"} {
		f.exec(t, `INSERT INTO users (id, hca_sub, verification_status, created_at_ms, updated_at_ms, signed_in_at_ms) VALUES ($1, $2, 'verified', 0, 0, 0)`, u, "sub-"+u)
	}
	for _, r := range [][3]string{{"d1", "k1", "P1"}, {"d2", "k2", "P1"}, {"d1", "k3", "P2"}} {
		f.exec(t, `INSERT INTO rollup_projects (user_id, device_id, project_key, project_id) VALUES ('u1', $1, $2, $3)`, r[0], r[1], r[2])
	}
	return f
}

func (f *fixture) exec(t *testing.T, q string, args ...any) {
	t.Helper()
	if err := f.w.Do(context.Background(), 1, func(ctx context.Context, tx writer.Tx) error {
		_, err := tx.ExecContext(ctx, q, args...)
		return err
	}); err != nil {
		t.Fatal(err)
	}
}

func (f *fixture) do(t *testing.T, op writer.Op) {
	t.Helper()
	if err := f.w.Do(context.Background(), 1, op); err != nil {
		t.Fatal(err)
	}
}

func (f *fixture) apply(t *testing.T, account, device string, set []projectnames.Name, del []string) int {
	t.Helper()
	var n int
	f.do(t, func(ctx context.Context, tx writer.Tx) error {
		var err error
		n, err = projectnames.Apply(ctx, tx, account, device, set, del, 1000)
		return err
	})
	return n
}

func (f *fixture) ofDevice(t *testing.T, device string) []projectnames.Name {
	t.Helper()
	names, err := projectnames.OfDevice(context.Background(), f.db.Read, device)
	if err != nil {
		t.Fatal(err)
	}
	return names
}

func (f *fixture) ofAccount(t *testing.T, account string) map[string]string {
	t.Helper()
	labels, err := projectnames.OfAccount(context.Background(), f.db.Read, account)
	if err != nil {
		t.Fatal(err)
	}
	return labels
}

func TestNames(t *testing.T) {
	for _, engine := range storetest.Engines() {
		t.Run(string(engine), func(t *testing.T) {
			tests := []struct {
				name string
				run  func(t *testing.T, f *fixture)
			}{
				{name: "a device's names read back in key order, with the count after the change", run: func(t *testing.T, f *fixture) {
					if n := f.apply(t, "u1", "d1", []projectnames.Name{{Project: "k3", Label: "portfolio"}, {Project: "k1", Label: "chip8"}}, nil); n != 2 {
						t.Errorf("Apply counted %d names, want 2", n)
					}
					if got, want := f.ofDevice(t, "d1"), []projectnames.Name{{Project: "k1", Label: "chip8"}, {Project: "k3", Label: "portfolio"}}; !reflect.DeepEqual(got, want) {
						t.Errorf("OfDevice = %v, want %v", got, want)
					}
				}},
				{name: "a device with no names reads back an empty list, not nil", run: func(t *testing.T, f *fixture) {
					if got := f.ofDevice(t, "d9"); got == nil || len(got) != 0 {
						t.Errorf("OfDevice = %#v, want an empty list", got)
					}
				}},
				{name: "setting a named key again changes its label and adds no row", run: func(t *testing.T, f *fixture) {
					f.apply(t, "u1", "d1", []projectnames.Name{{Project: "k1", Label: "chip8"}}, nil)
					if n := f.apply(t, "u1", "d1", []projectnames.Name{{Project: "k1", Label: "chip8-emulator"}}, nil); n != 1 {
						t.Errorf("Apply counted %d names, want 1", n)
					}
					if got := f.ofDevice(t, "d1"); !reflect.DeepEqual(got, []projectnames.Name{{Project: "k1", Label: "chip8-emulator"}}) {
						t.Errorf("OfDevice = %v", got)
					}
				}},
				{name: "a delete drops the name at once, and a delete of a key with no name is no error", run: func(t *testing.T, f *fixture) {
					f.apply(t, "u1", "d1", []projectnames.Name{{Project: "k1", Label: "chip8"}}, nil)
					if n := f.apply(t, "u1", "d1", nil, []string{"k1", "k_none"}); n != 0 {
						t.Errorf("Apply counted %d names, want 0", n)
					}
					if got := f.ofDevice(t, "d1"); len(got) != 0 {
						t.Errorf("OfDevice = %v, want none", got)
					}
				}},
				{name: "an account's names are read by project key, and no other account's", run: func(t *testing.T, f *fixture) {
					f.apply(t, "u1", "d1", []projectnames.Name{{Project: "k1", Label: "chip8"}}, nil)
					f.apply(t, "u2", "d7", []projectnames.Name{{Project: "k7", Label: "someone else's"}}, nil)
					if got := f.ofAccount(t, "u1"); !reflect.DeepEqual(got, map[string]string{"k1": "chip8"}) {
						t.Errorf("OfAccount(u1) = %v", got)
					}
				}},
				{name: "a stored name never moves to another account", run: func(t *testing.T, f *fixture) {
					f.apply(t, "u1", "d1", []projectnames.Name{{Project: "k1", Label: "chip8"}}, nil)
					f.apply(t, "u2", "d1", []projectnames.Name{{Project: "k1", Label: "taken over"}}, nil)
					if got := f.ofAccount(t, "u1"); !reflect.DeepEqual(got, map[string]string{"k1": "chip8"}) {
						t.Errorf("OfAccount(u1) = %v, want the first label kept", got)
					}
					if got := f.ofAccount(t, "u2"); len(got) != 0 {
						t.Errorf("OfAccount(u2) = %v, want none", got)
					}
				}},
				{name: "Foreign finds a device's rows under another account, and none under its own", run: func(t *testing.T, f *fixture) {
					f.apply(t, "u1", "d1", []projectnames.Name{{Project: "k1", Label: "chip8"}}, nil)
					ctx := context.Background()
					if got, err := projectnames.Foreign(ctx, f.db.Read, "d1", "u1"); err != nil || got {
						t.Errorf("Foreign(d1, u1) = %v, %v; want false", got, err)
					}
					if got, err := projectnames.Foreign(ctx, f.db.Read, "d1", "u2"); err != nil || !got {
						t.Errorf("Foreign(d1, u2) = %v, %v; want true", got, err)
					}
				}},
				{name: "hiding a project deletes the name of every device key of it, and nothing else", run: func(t *testing.T, f *fixture) {
					f.apply(t, "u1", "d1", []projectnames.Name{{Project: "k1", Label: "chip8"}, {Project: "k3", Label: "portfolio"}}, nil)
					f.apply(t, "u1", "d2", []projectnames.Name{{Project: "k2", Label: "chip8 on the desktop"}}, nil)
					f.do(t, func(ctx context.Context, tx writer.Tx) error { return projectnames.Hide(ctx, tx, "u1", "P1") })
					if got := f.ofAccount(t, "u1"); !reflect.DeepEqual(got, map[string]string{"k3": "portfolio"}) {
						t.Errorf("OfAccount after hiding P1 = %v, want portfolio alone", got)
					}
				}},
				{name: "hiding another account's project deletes nothing", run: func(t *testing.T, f *fixture) {
					f.apply(t, "u1", "d1", []projectnames.Name{{Project: "k1", Label: "chip8"}}, nil)
					f.do(t, func(ctx context.Context, tx writer.Tx) error { return projectnames.Hide(ctx, tx, "u2", "P1") })
					if got := f.ofAccount(t, "u1"); !reflect.DeepEqual(got, map[string]string{"k1": "chip8"}) {
						t.Errorf("OfAccount(u1) = %v, want chip8 kept", got)
					}
				}},
				{name: "deleting an account deletes every one of its names and no other account's", run: func(t *testing.T, f *fixture) {
					f.apply(t, "u1", "d1", []projectnames.Name{{Project: "k1", Label: "chip8"}}, nil)
					f.apply(t, "u2", "d7", []projectnames.Name{{Project: "k7", Label: "theirs"}}, nil)
					f.do(t, func(ctx context.Context, tx writer.Tx) error { return projectnames.DeleteAccount(ctx, tx, "u1") })
					if got := f.ofAccount(t, "u1"); len(got) != 0 {
						t.Errorf("OfAccount(u1) = %v, want none", got)
					}
					if got := f.ofAccount(t, "u2"); !reflect.DeepEqual(got, map[string]string{"k7": "theirs"}) {
						t.Errorf("OfAccount(u2) = %v, want theirs kept", got)
					}
				}},
			}
			for _, tt := range tests {
				t.Run(tt.name, func(t *testing.T) { tt.run(t, newFixture(t, engine)) })
			}
		})
	}
}
