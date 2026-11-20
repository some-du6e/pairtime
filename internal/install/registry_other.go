// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build !windows

package install

// systemRegistry is nil: only Windows has a registry.
func systemRegistry() Registry { return nil }
