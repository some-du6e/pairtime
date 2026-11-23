// SPDX-License-Identifier: AGPL-3.0-or-later

package agent

import (
	"errors"
	"fmt"

	"github.com/some-du6e/pairtime/internal/agent/config"
	"github.com/some-du6e/pairtime/internal/agent/keys"
	"github.com/some-du6e/pairtime/internal/agent/ledger"
	"github.com/some-du6e/pairtime/internal/agent/vault"
)

// openVault opens the vault for the keystore ks, whose K_dev has the id
// keyID (WP-LG6), with "keep prompt text" as config.toml says now. A vault
// that doesn't open leaves the ledger to record without one: the records
// stand, and their texts aren't kept, which doctor hears about.
func (a *agent) openVault(ks *keys.Keystore, keyID string) *vault.Vault {
	v, err := vault.Open(vault.Options{
		Dir:      a.dirs.Vault(),
		Keys:     ks.Keys(),
		KeyID:    keyID,
		KeepText: a.privacy().KeepText(),
		Problem:  func(msg string) { a.setProblem("vault", msg) },
	})
	if err != nil {
		a.setProblem("vault.open", fmt.Sprintf("pairtime keeps none of your prompt text in this run, because its vault didn't open: %v", err))
		a.log.Warn("the vault didn't open", "err", err)
		return nil
	}
	// Under a source of its own, so this clears no problem the vault
	// reported as it opened, such as a file it couldn't read.
	a.setProblem("vault.open", "")
	return v
}

// privacy is config.toml's [privacy] in force, or its default before the
// agent has read config.toml, as in an agent a test makes without Run.
func (a *agent) privacy() config.Privacy {
	if cfg := a.cfg.Load(); cfg != nil {
		return cfg.Privacy
	}
	return config.Default().Privacy
}

// writerVault is v as ledger.Options.Vault takes it: no vault at all when
// v is nil, rather than an interface holding a nil pointer.
func writerVault(v *vault.Vault) ledger.Vault {
	if v == nil {
		return nil
	}
	return v
}

// publishVault makes v the agent's vault, once the ledger writer that
// keeps content in it has opened, and puts config.toml's [privacy] in
// force on it.
func (a *agent) publishVault(v *vault.Vault) {
	if v == nil {
		return
	}
	a.vaultMu.Lock()
	a.vault = v
	a.vaultMu.Unlock()
	a.applyPrivacy()
}

// applyPrivacy puts config.toml's [privacy] in force on the vault, once it
// is open: "keep prompt text" for the records from now on, and with the
// switch turned off, the text already kept goes too (DESIGN 4.4). A
// config.toml that was ignored, or whose [privacy] pairtime can't read,
// keeps no new text and deletes none, even beside the switch turned off
// (config.Privacy.TextHeld).
func (a *agent) applyPrivacy() {
	a.vaultMu.Lock()
	defer a.vaultMu.Unlock()
	v := a.vault
	if v == nil {
		return
	}
	p := a.privacy()
	v.SetKeepText(p.KeepText())
	if !p.TextOff || p.TextHeld {
		// Nothing to delete, so a failure of an earlier run of dropText no
		// longer stands.
		a.setProblem("vault.text", "")
	} else {
		// Each file of the vault is read and written again, which can take
		// a moment, so not on the caller's goroutine, which may be the
		// scheduler's. The run is counted under vaultMu while the vault is
		// set, so closeVault waits for it.
		a.vaultWork.Add(1)
		go func() {
			defer a.vaultWork.Done()
			a.dropText(v)
		}()
	}
}

// dropText deletes the text v keeps, one run at a time, while the switch
// is still off: a run that waited behind another may find it turned back
// on.
func (a *agent) dropText(v *vault.Vault) {
	a.dropMu.Lock()
	defer a.dropMu.Unlock()
	if p := a.privacy(); !p.TextOff || p.TextHeld {
		return
	}
	n, err := v.DropText()
	switch {
	case errors.Is(err, vault.ErrClosed):
	case err != nil:
		a.setProblem("vault.text", fmt.Sprintf("\"keep prompt text\" is off, and pairtime couldn't delete all the prompt text it kept, which it tries again at the next start or change of config.toml: %v", err))
		a.log.Warn("deleting the vault's text", "err", err)
	default:
		a.setProblem("vault.text", "")
		if n > 0 {
			a.log.Info("\"keep prompt text\" is off, so the vault deleted the text it kept", "texts", n)
		}
	}
}

// closeVault closes the vault, once the ledger writer that keeps content
// in it is closed, after any run of dropText has ended.
func (a *agent) closeVault() {
	a.vaultMu.Lock()
	v := a.vault
	a.vault = nil
	a.vaultMu.Unlock()
	if v == nil {
		return
	}
	a.vaultWork.Wait()
	if err := v.Close(); err != nil {
		a.log.Warn("closing the vault", "err", err)
	}
}
