// SPDX-License-Identifier: AGPL-3.0-or-later

package agent

import (
	"testing"
	"time"

	"github.com/some-du6e/pairtime/internal/agent/home"
	"github.com/some-du6e/pairtime/internal/agent/keys"
	"github.com/some-du6e/pairtime/internal/agent/ledger"
)

// TestLedger checks that a running agent opens the device keystore and its
// ledger (WP-OS7, WP-LG5), and that the ledger reads back once it stops.
func TestLedger(t *testing.T) {
	dirs := home.Dirs{Data: t.TempDir(), Config: t.TempDir()}
	dirs.Runtime = dirs.Data
	a := startAgent(t, Options{Dirs: dirs, Ports: []int{0}, KeyStores: []keys.Store{}})

	// Both open on a goroutine of their own, after the agent is ready.
	var info ledger.ChainInfo
	deadline := time.Now().Add(20 * time.Second)
	for {
		var err error
		if info, err = ledger.ReadChain(dirs.Ledger()); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the agent didn't open its ledger within 20 s: %v; status %+v", err, getStatus(t, a))
		}
		time.Sleep(50 * time.Millisecond)
	}
	ks, err := keys.Inspect(dirs.Data)
	if err != nil {
		t.Fatal(err)
	}
	a.cancel()
	stopErr := a.waitExit(t)

	cases := []struct {
		name string
		ok   bool
	}{
		{"the ledger's chain is written under the installation's device key", info.KeyID == ks.KeyID},
		{"the chain names a device", info.DeviceID != ""},
		{"the agent stops cleanly with its ledger open", stopErr == nil},
		{"the stopped agent's ledger reads back", ledger.ReadBatches(dirs.Ledger(), 1, func(ledger.Batch) error { return nil }) == nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if !c.ok {
				t.Errorf("no: chain %+v, keystore %+v, stop %v", info, ks, stopErr)
			}
		})
	}
}
