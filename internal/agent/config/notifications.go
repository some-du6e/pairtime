// SPDX-License-Identifier: AGPL-3.0-or-later

package config

import (
	"errors"
	"fmt"
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

// notificationsFile is the [notifications] table as written. Like every
// setting in fileConfig, each field is nil when the file doesn't have it.
type notificationsFile struct {
	Enabled    *bool   `toml:"enabled"`
	QuietHours *string `toml:"quiet_hours"`
}

// ignored is the configuration in force when config.toml exists but can't
// be read or parsed: the defaults, except that notifications are off. The
// teen may have turned them off in that file, and a notification puts a
// project's name on the screen, so they stay off until the file is fixed.
func ignored() Config {
	cfg := Default()
	cfg.Notifications.Off = true
	return cfg
}

// parseNotifications checks the [notifications] table and puts it in cfg.
// keys are every key in the file, in its own spelling. Each rule here only
// ever turns notifications off, never back on, so no order among them, or
// with Parse's rule for unknown settings, can leave them on.
func parseNotifications(cfg *Config, file notificationsFile, keys []toml.Key) (problems []string) {
	off := func(why string) {
		cfg.Notifications.Off = true
		problems = append(problems, "config.toml: "+why+", so notifications are off until it's fixed")
	}
	// TOML keys are case-sensitive, so `enabled` and `Enabled` are two
	// settings to TOML, but the decoder fills one field from both, in no
	// fixed order. Which one counts would then be chance.
	if s := spellings(keys, "notifications", "enabled"); len(s) > 1 {
		off(fmt.Sprintf("notifications.enabled is written more than once, as %s", strings.Join(s, " and ")))
	} else if file.Enabled != nil && !*file.Enabled {
		cfg.Notifications.Off = true
	}
	if s := spellings(keys, "notifications", "quiet_hours"); len(s) > 1 {
		off(fmt.Sprintf("notifications.quiet_hours is written more than once, as %s", strings.Join(s, " and ")))
	} else if file.QuietHours != nil {
		q, err := ParseQuietHours(*file.QuietHours)
		if err != nil {
			// The teen asked for quiet at some hours, and pairtime can't
			// tell which, so it stays quiet at all of them.
			off(fmt.Sprintf("notifications.quiet_hours %q %v", *file.QuietHours, err))
		} else {
			cfg.Notifications.Quiet = q
		}
	}
	return problems
}

// spellings returns each way keys spell the setting at path, quoted, when
// its parts are compared without regard to case.
func spellings(keys []toml.Key, path ...string) []string {
	var out []string
	for _, k := range keys {
		if len(k) != len(path) {
			continue
		}
		same := true
		for i := range k {
			if !strings.EqualFold(k[i], path[i]) {
				same = false
				break
			}
		}
		if same {
			out = append(out, fmt.Sprintf("%q", k.String()))
		}
	}
	return out
}

// aboutNotifications reports whether an unknown setting seems to be about
// notifications: its table, or its own name at the top level, mentions
// notifications or quiet hours in any case, as `enable` under
// [notifications], `enabled` under [Notification], or a quiet_hours written
// above every table.
func aboutNotifications(key toml.Key) bool {
	if len(key) == 0 {
		return false
	}
	first := strings.ToLower(key[0])
	return strings.Contains(first, "notif") || strings.Contains(first, "quiet")
}
