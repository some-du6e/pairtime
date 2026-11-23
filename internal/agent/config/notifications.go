// SPDX-License-Identifier: AGPL-3.0-or-later

package config

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

// Notifications is config.toml's [notifications] table: the one quiet
// notification the coach shows when a work session ends (DESIGN 3.3, 3.8).
// The zero value is the default, on with no quiet hours (DESIGN 1.2).
type Notifications struct {
	// Off is the off switch, written enabled = false in the file. A teen
	// turns notifications off with one setting (TE 11.1, risk 2).
	Off bool
	// Quiet is quiet_hours, the time of day when none are shown
	// (DESIGN 3.9).
	Quiet QuietHours
}

// QuietHours is a span of local time, the same every day, when no
// notification is shown. The zero value is no quiet hours.
type QuietHours struct {
	set bool
	// Minutes after local midnight. The span starts at from and ends just
	// before to; when to is earlier than from, it crosses midnight.
	from, to int
}

var errQuietForm = errors.New(`isn't two 24-hour times such as "22:00-07:00"`)

// ParseQuietHours reads quiet hours written as two 24-hour local times,
// "HH:MM-HH:MM", such as "22:00-07:00", which crosses midnight. The span
// includes its first minute and stops just before its second time. "" means
// no quiet hours. A span that starts and ends at the same time is refused,
// because it could mean all day or none; turning notifications off is the
// way to have none at all.
func ParseQuietHours(s string) (QuietHours, error) {
	if strings.TrimSpace(s) == "" {
		return QuietHours{}, nil
	}
	a, b, ok := strings.Cut(s, "-")
	if !ok {
		return QuietHours{}, errQuietForm
	}
	from, okFrom := minuteOfDay(strings.TrimSpace(a))
	to, okTo := minuteOfDay(strings.TrimSpace(b))
	if !okFrom || !okTo {
		return QuietHours{}, errQuietForm
	}
	if from == to {
		return QuietHours{}, errors.New("starts and ends at the same time")
	}
	return QuietHours{set: true, from: from, to: to}, nil
}

// minuteOfDay reads "HH:MM", from 00:00 to 23:59.
func minuteOfDay(s string) (int, bool) {
	if len(s) != 5 || s[2] != ':' {
		return 0, false
	}
	for _, i := range []int{0, 1, 3, 4} {
		if s[i] < '0' || s[i] > '9' {
			return 0, false
		}
	}
	h := int(s[0]-'0')*10 + int(s[1]-'0')
	m := int(s[3]-'0')*10 + int(s[4]-'0')
	if h > 23 || m > 59 {
		return 0, false
	}
	return h*60 + m, true
}

// IsZero reports whether q is no quiet hours.
func (q QuietHours) IsZero() bool {
	return !q.set
}

// Contains reports whether t falls in the quiet hours, reading t's clock
// time in t's own location. Pass the local time: quiet hours follow the
// computer's clock, daylight saving included.
func (q QuietHours) Contains(t time.Time) bool {
	if !q.set {
		return false
	}
	m := t.Hour()*60 + t.Minute()
	if q.from < q.to {
		return q.from <= m && m < q.to
	}
	return m >= q.from || m < q.to
}

// String returns q in the form ParseQuietHours reads, or "" for none.
func (q QuietHours) String() string {
	if !q.set {
		return ""
	}
	return fmt.Sprintf("%02d:%02d-%02d:%02d", q.from/60, q.from%60, q.to/60, q.to%60)
}

// notificationsFile is the [notifications] table as written. The fields
// are pointers so that parseNotifications can tell a key that is there
// from one that isn't, however the file spells it: the decoder matches
// keys to fields whatever their case, as in [Notifications] or
// Enabled = false, while MetaData.IsDefined is case-sensitive.
type notificationsFile struct {
	Enabled    *bool   `toml:"enabled"`
	QuietHours *string `toml:"quiet_hours"`
}

// ignored is the configuration in force when config.toml exists but can't
// be read or parsed: the defaults, except that notifications are off and
// no new prompt text is kept. The teen may have turned either off in that
// file, and a notification puts a project's name on the screen, so they
// stay off until the file is fixed. The text already kept stays, as the
// file may say to keep it (Privacy.TextHeld).
func ignored() Config {
	cfg := Default()
	cfg.Notifications.Off = true
	cfg.Privacy.TextHeld = true
	return cfg
}

// parseNotifications checks the [notifications] table and puts it in cfg.
// keys are every key in the file. Each rule can only turn notifications
// off, so none of them undoes another's.
func parseNotifications(cfg *Config, file notificationsFile, keys []toml.Key) (problems []string) {
	// The decoder put one of the spellings in file, and which one depends
	// on map order, so a setting written twice counts as one it can't read.
	twice := func(key string) bool {
		if spellings(keys, "notifications", key) < 2 {
			return false
		}
		cfg.Notifications.Off = true
		problems = append(problems, fmt.Sprintf("config.toml: notifications.%s is set more than once, in different upper and lower case, so notifications are off until it's fixed", key))
		return true
	}
	if !twice("enabled") && file.Enabled != nil && !*file.Enabled {
		cfg.Notifications.Off = true
	}
	if !twice("quiet_hours") && file.QuietHours != nil {
		q, err := ParseQuietHours(*file.QuietHours)
		if err != nil {
			// The teen asked for quiet at some hours, and pairtime can't
			// tell which, so it stays quiet at all of them.
			cfg.Notifications.Off = true
			problems = append(problems, fmt.Sprintf("config.toml: notifications.quiet_hours %q %v, so notifications are off until it's fixed", *file.QuietHours, err))
		} else {
			cfg.Notifications.Quiet = q
		}
	}
	return problems
}

// spellings counts the keys that name the setting at path, matched the
// way the decoder matches keys to fields, whatever their case.
func spellings(keys []toml.Key, path ...string) int {
	n := 0
	for _, k := range keys {
		if slices.EqualFunc(k, path, strings.EqualFold) {
			n++
		}
	}
	return n
}

// inNotifications reports whether key is in the [notifications] table,
// however the file spells the table's name.
func inNotifications(key toml.Key) bool {
	return len(key) > 1 && strings.EqualFold(key[0], "notifications")
}
