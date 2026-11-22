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
// P1 of u1 on devices d1 and d2 (keys k1 and k2), P2 of u1 on d1 (k3), and
// P7 of u2 on d7 (k7).
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
	for _, r := range [][4]string{{"u1", "d1", "k1", "P1"}, {"u1", "d2", "k2", "P1"}, {"u1", "d1", "k3", "P2"}, {"u2", "d7", "k7", "P7"}} {
		f.exec(t, `INSERT INTO rollup_projects (user_id, device_id, project_key, project_id) VALUES ($1, $2, $3, $4)`, r[0], r[1], r[2], r[3])
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

func (f *fixture) hide(t *testing.T, account, project string) {
	t.Helper()
	f.do(t, func(ctx context.Context, tx writer.Tx) error {
		return projectnames.Hide(ctx, tx, account, project, 2000)
	})
}

// device is what OfDevice answers for a device: its names, and the keys
// of those hidden on the dashboard.
type device struct {
	names  []projectnames.Name
	hidden []string
}

func (f *fixture) ofDevice(t *testing.T, id string) device {
	t.Helper()
	names, hidden, err := projectnames.OfDevice(context.Background(), f.db.Read, id)
	if err != nil {
		t.Fatal(err)
	}
	if names == nil || hidden == nil {
		t.Errorf("OfDevice(%s) = %#v, %#v; want empty lists, not nil", id, names, hidden)
	}
	return device{names: names, hidden: hidden}
}

func (f *fixture) ofAccount(t *testing.T, account string) map[string]string {
	t.Helper()
	labels, err := projectnames.OfAccount(context.Background(), f.db.Read, account)
	if err != nil {
		t.Fatal(err)
	}
	return labels
}

func (f *fixture) count(t *testing.T, q string, args ...any) int {
	t.Helper()
	var n int
	if err := f.db.Read.QueryRow(q, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestNames(t *testing.T) {
	none := []projectnames.Name{}
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
					if got, want := f.ofDevice(t, "d1"), (device{names: []projectnames.Name{{Project: "k1", Label: "chip8"}, {Project: "k3", Label: "portfolio"}}, hidden: []string{}}); !reflect.DeepEqual(got, want) {
						t.Errorf("OfDevice = %v, want %v", got, want)
					}
				}},
				{name: "a device with no names reads back empty lists, not nil", run: func(t *testing.T, f *fixture) {
					if got := f.ofDevice(t, "d9"); len(got.names) != 0 || len(got.hidden) != 0 {
						t.Errorf("OfDevice = %v, want none", got)
					}
				}},
				{name: "setting a named key again changes its label and adds no row", run: func(t *testing.T, f *fixture) {
					f.apply(t, "u1", "d1", []projectnames.Name{{Project: "k1", Label: "chip8"}}, nil)
					if n := f.apply(t, "u1", "d1", []projectnames.Name{{Project: "k1", Label: "chip8-emulator"}}, nil); n != 1 {
						t.Errorf("Apply counted %d names, want 1", n)
					}
					if got := f.ofDevice(t, "d1"); !reflect.DeepEqual(got.names, []projectnames.Name{{Project: "k1", Label: "chip8-emulator"}}) {
						t.Errorf("OfDevice = %v", got)
					}
				}},
				{name: "a delete drops the name at once, and a delete of a key with no name is no error", run: func(t *testing.T, f *fixture) {
					f.apply(t, "u1", "d1", []projectnames.Name{{Project: "k1", Label: "chip8"}}, nil)
					if n := f.apply(t, "u1", "d1", nil, []string{"k1", "k_none"}); n != 0 {
						t.Errorf("Apply counted %d names, want 0", n)
					}
					if got := f.ofDevice(t, "d1"); len(got.names) != 0 {
						t.Errorf("OfDevice = %v, want none", got)
					}
				}},
				{name: "an account's names are read by project id, and no other account's", run: func(t *testing.T, f *fixture) {
					f.apply(t, "u1", "d1", []projectnames.Name{{Project: "k1", Label: "chip8"}}, nil)
					f.apply(t, "u2", "d7", []projectnames.Name{{Project: "k7", Label: "someone else's"}}, nil)
					if got := f.ofAccount(t, "u1"); !reflect.DeepEqual(got, map[string]string{"P1": "chip8"}) {
						t.Errorf("OfAccount(u1) = %v", got)
					}
				}},
				{name: "a project is called by the name of its first device key, in device and key order, that has one", run: func(t *testing.T, f *fixture) {
					f.apply(t, "u1", "d2", []projectnames.Name{{Project: "k2", Label: "chip8 on the desktop"}}, nil)
					if got := f.ofAccount(t, "u1"); !reflect.DeepEqual(got, map[string]string{"P1": "chip8 on the desktop"}) {
						t.Errorf("OfAccount(u1) = %v, want the desktop's name while it is the only one", got)
					}
					f.apply(t, "u1", "d1", []projectnames.Name{{Project: "k1", Label: "chip8"}}, nil)
					if got := f.ofAccount(t, "u1"); !reflect.DeepEqual(got, map[string]string{"P1": "chip8"}) {
						t.Errorf("OfAccount(u1) = %v, want d1's name first", got)
					}
				}},
				{name: "a name a device stored for a key it never uploaded minutes of shows nowhere, as one from a config.toml copied from another computer", run: func(t *testing.T, f *fixture) {
					f.apply(t, "u1", "d2", []projectnames.Name{{Project: "k1", Label: "copied"}}, nil)
					if got := f.ofAccount(t, "u1"); len(got) != 0 {
						t.Errorf("OfAccount(u1) = %v, want none: d2 never uploaded k1", got)
					}
				}},
				{name: "a stored name never moves to another account", run: func(t *testing.T, f *fixture) {
					f.apply(t, "u1", "d1", []projectnames.Name{{Project: "k1", Label: "chip8"}}, nil)
					f.apply(t, "u2", "d1", []projectnames.Name{{Project: "k1", Label: "taken over"}}, nil)
					if got := f.ofAccount(t, "u1"); !reflect.DeepEqual(got, map[string]string{"P1": "chip8"}) {
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
				{name: "hiding a project takes the label off the name of every device key of it at once, and each device's answer lists its key as hidden", run: func(t *testing.T, f *fixture) {
					f.apply(t, "u1", "d1", []projectnames.Name{{Project: "k1", Label: "chip8"}, {Project: "k3", Label: "portfolio"}}, nil)
					f.apply(t, "u1", "d2", []projectnames.Name{{Project: "k2", Label: "chip8 on the desktop"}}, nil)
					f.hide(t, "u1", "P1")
					if got := f.ofAccount(t, "u1"); !reflect.DeepEqual(got, map[string]string{"P2": "portfolio"}) {
						t.Errorf("OfAccount after hiding P1 = %v, want portfolio alone", got)
					}
					if got, want := f.ofDevice(t, "d1"), (device{names: []projectnames.Name{{Project: "k3", Label: "portfolio"}}, hidden: []string{"k1"}}); !reflect.DeepEqual(got, want) {
						t.Errorf("OfDevice(d1) = %v, want %v", got, want)
					}
					if got, want := f.ofDevice(t, "d2"), (device{names: none, hidden: []string{"k2"}}); !reflect.DeepEqual(got, want) {
						t.Errorf("OfDevice(d2) = %v, want %v", got, want)
					}
					if n := f.count(t, `SELECT COUNT(*) FROM project_names WHERE label IS NOT NULL AND project_key IN ('k1', 'k2')`); n != 0 {
						t.Errorf("%d of P1's labels stayed, want none", n)
					}
					if n := f.count(t, `SELECT COUNT(*) FROM project_names WHERE hidden_at_ms = 2000`); n != 2 {
						t.Errorf("%d rows say when they were hidden, want 2", n)
					}
				}},
				{name: "a hidden name stays a row, counted toward the device's limit, until the device's delete removes it", run: func(t *testing.T, f *fixture) {
					f.apply(t, "u1", "d1", []projectnames.Name{{Project: "k1", Label: "chip8"}, {Project: "k3", Label: "portfolio"}}, nil)
					f.hide(t, "u1", "P1")
					if n := f.apply(t, "u1", "d1", nil, nil); n != 2 {
						t.Errorf("Apply counted %d rows after the hide, want 2", n)
					}
					if n := f.apply(t, "u1", "d1", nil, []string{"k1"}); n != 1 {
						t.Errorf("Apply counted %d rows after the delete, want 1", n)
					}
					if got, want := f.ofDevice(t, "d1"), (device{names: []projectnames.Name{{Project: "k3", Label: "portfolio"}}, hidden: []string{}}); !reflect.DeepEqual(got, want) {
						t.Errorf("OfDevice(d1) = %v, want %v", got, want)
					}
				}},
				{name: "a hidden name turned on again on the computer gets its label back and is no longer hidden", run: func(t *testing.T, f *fixture) {
					f.apply(t, "u1", "d1", []projectnames.Name{{Project: "k1", Label: "chip8"}}, nil)
					f.hide(t, "u1", "P1")
					f.apply(t, "u1", "d1", []projectnames.Name{{Project: "k1", Label: "chip8 again"}}, nil)
					if got, want := f.ofDevice(t, "d1"), (device{names: []projectnames.Name{{Project: "k1", Label: "chip8 again"}}, hidden: []string{}}); !reflect.DeepEqual(got, want) {
						t.Errorf("OfDevice(d1) = %v, want %v", got, want)
					}
					if n := f.count(t, `SELECT COUNT(*) FROM project_names WHERE hidden_at_ms IS NOT NULL`); n != 0 {
						t.Errorf("%d rows still say when they were hidden, want none", n)
					}
					if got := f.ofAccount(t, "u1"); !reflect.DeepEqual(got, map[string]string{"P1": "chip8 again"}) {
						t.Errorf("OfAccount(u1) = %v", got)
					}
				}},
				{name: "hiding another account's project changes nothing", run: func(t *testing.T, f *fixture) {
					f.apply(t, "u1", "d1", []projectnames.Name{{Project: "k1", Label: "chip8"}}, nil)
					f.hide(t, "u2", "P1")
					if got := f.ofAccount(t, "u1"); !reflect.DeepEqual(got, map[string]string{"P1": "chip8"}) {
						t.Errorf("OfAccount(u1) = %v, want chip8 kept", got)
					}
					if got := f.ofDevice(t, "d1"); len(got.hidden) != 0 {
						t.Errorf("OfDevice(d1) = %v, want nothing hidden", got)
					}
				}},
				{name: "deleting an account deletes every one of its names, hidden ones too, and no other account's", run: func(t *testing.T, f *fixture) {
					f.apply(t, "u1", "d1", []projectnames.Name{{Project: "k1", Label: "chip8"}, {Project: "k3", Label: "portfolio"}}, nil)
					f.hide(t, "u1", "P1")
					f.apply(t, "u2", "d7", []projectnames.Name{{Project: "k7", Label: "theirs"}}, nil)
					f.do(t, func(ctx context.Context, tx writer.Tx) error { return projectnames.DeleteAccount(ctx, tx, "u1") })
					if n := f.count(t, `SELECT COUNT(*) FROM project_names WHERE user_id = 'u1'`); n != 0 {
						t.Errorf("u1 has %d rows left, want none", n)
					}
					if got := f.ofAccount(t, "u2"); !reflect.DeepEqual(got, map[string]string{"P7": "theirs"}) {
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
