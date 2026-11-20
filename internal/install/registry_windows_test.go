// SPDX-License-Identifier: AGPL-3.0-or-later

package install

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestRunValueOnThisAccount writes and removes a Run value under the real
// HKEY_CURRENT_USER of the account the test runs as, which needs no admin
// rights (CP 2.1). The value has a name of its own, so a pairtime set up
// on the same account is left alone.
func TestRunValueOnThisAccount(t *testing.T) {
	reg := systemRegistry()
	name := fmt.Sprintf("pairtime-test-%d-%d", os.Getpid(), time.Now().UnixNano())
	t.Cleanup(func() { reg.Delete(RunKey, name) })
	tests := []struct {
		name   string
		before *regValue // the value before install; nil for none
		// earlier means before is an earlier pairtime's Run value, for
		// another binary path, which uninstall takes away.
		earlier bool
	}{
		{"a Run value pairtime adds is gone after uninstall", nil, false},
		{"a Run value of the same name for another program comes back after uninstall, with its type", &regValue{value: `"%LOCALAPPDATA%\Other\other.exe" --tray`, typ: RegExpandSZ}, false},
		{"a Run value an earlier pairtime wrote for another binary path is gone after uninstall", &regValue{value: `"%LOCALAPPDATA%\old\pairtime.exe" agent`, typ: RegExpandSZ}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := reg.Delete(RunKey, name); err != nil {
				t.Fatal(err)
			}
			if tt.before != nil {
				if err := reg.Set(RunKey, name, tt.before.value, tt.before.typ); err != nil {
					t.Fatal(err)
				}
			}
			cmd, err := runCommand(filepath.Join(t.TempDir(), "pairtime.exe"))
			if err != nil {
				t.Fatal(err)
			}
			item := Item{ID: ItemAutostart, Edits: []Edit{ownedEdit{target: RegistryValue(RunKey, name), data: []byte(cmd), typ: RegSZ, claim: isAgentRunValue}}}
			in := &Installer{Dir: filepath.Join(t.TempDir(), "install"), Registry: reg}

			install(t, in, item)
			v, typ, ok, err := reg.Get(RunKey, name)
			if err != nil || !ok || v != cmd || typ != RegSZ {
				t.Fatalf("after install the Run value is %q (type %d, there: %v, %v), want %q (type %d)", v, typ, ok, err, cmd, RegSZ)
			}
			again, err := in.PlanInstall([]Item{item})
			if err != nil {
				t.Fatal(err)
			}
			if !again.Empty() {
				t.Errorf("a second install plans changes:\n%s", again.Diff())
			}

			uninstall(t, in)
			v, typ, ok, err = reg.Get(RunKey, name)
			switch {
			case err != nil:
				t.Fatal(err)
			case (tt.before == nil || tt.earlier) && ok:
				t.Errorf("after uninstall the Run value is still there: %q", v)
			case tt.before != nil && !tt.earlier && (!ok || v != tt.before.value || typ != tt.before.typ):
				t.Errorf("after uninstall the Run value is %q (type %d, there: %v), want %q (type %d)", v, typ, ok, tt.before.value, tt.before.typ)
			}
		})
	}
	if _, _, ok, err := reg.Get(`Software\pairtime-test-no-such-key`, name); ok || err != nil {
		t.Errorf("a value under a key that isn't there reads as there: %v, %v", ok, err)
	}
}
