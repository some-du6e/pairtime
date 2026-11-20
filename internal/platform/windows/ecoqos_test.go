// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build windows

package windows

import "testing"

// TestEcoQoS is WP-OS1's check that the opt-in reaches Windows: the value
// is read back from Win32, not from anything pairtime keeps.
func TestEcoQoS(t *testing.T) {
	tests := []struct {
		name string
	}{
		{"EcoQoS reads back as set through GetProcessInformation"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := SetEcoQoS(); err != nil {
				t.Fatal(err)
			}
			on, err := EcoQoS()
			if err != nil {
				t.Fatal(err)
			}
			if !on {
				t.Error("GetProcessInformation(ProcessPowerThrottling) shows the execution-speed throttle off after SetEcoQoS")
			}
		})
	}
}
