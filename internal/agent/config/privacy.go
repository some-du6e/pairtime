// SPDX-License-Identifier: AGPL-3.0-or-later

package config

import (
	"strings"

	"github.com/BurntSushi/toml"
)

// Privacy is config.toml's [privacy] table: what the device keeps of what
// you typed (DESIGN 4.4). The zero value is the default, in which the
// vault keeps the text of your prompts, encrypted, so a spot check can
// see them (TP B8).
type Privacy struct {
	// TextOff is keep_prompt_text = false, the "keep prompt text" switch
	// turned off (DESIGN 4.4): the vault keeps no text of a prompt, a final
	// agent message or a track label, and deletes the text it kept. A spot
	// check that draws such a prompt gets "expired", and its minutes count
	// as witnessed with the evidence lost (DESIGN 7.5 step 8).
	TextOff bool
	// TextHeld is set when config.toml was ignored, or [privacy] holds a
	// setting this release doesn't know or the switch twice: the file may
	// be where the teen turned "keep prompt text" off, so the vault keeps
	// no new text, as when it is off. It deletes none, though, since the
	// file may as well say to keep it, and a typo mustn't delete what a
	// spot check could need.
	TextHeld bool
}

// KeepText reports whether the vault keeps the text of new records.
func (p Privacy) KeepText() bool { return !p.TextOff && !p.TextHeld }

// privacyFile is the [privacy] table as written. The field is a pointer
// so parsePrivacy can tell a key that is there from one that isn't,
// however the file spells it, as notificationsFile does.
type privacyFile struct {
	KeepPromptText *bool `toml:"keep_prompt_text"`
}

// parsePrivacy checks the [privacy] table and puts it in cfg. keys are
// every key in the file.
func parsePrivacy(cfg *Config, file privacyFile, keys []toml.Key) (problems []string) {
	if spellings(keys, "privacy", "keep_prompt_text") > 1 {
		// The decoder put one of the spellings in file, and which one
		// depends on map order.
		cfg.Privacy.TextHeld = true
		return []string{"config.toml: privacy.keep_prompt_text is set more than once, in different upper and lower case, so pairtime keeps no new prompt text, and deletes none, until it's fixed"}
	}
	if file.KeepPromptText != nil && !*file.KeepPromptText {
		cfg.Privacy.TextOff = true
	}
	return nil
}

// inPrivacy reports whether key is in the [privacy] table, however the
// file spells the table's name.
func inPrivacy(key toml.Key) bool {
	return len(key) > 1 && strings.EqualFold(key[0], "privacy")
}
