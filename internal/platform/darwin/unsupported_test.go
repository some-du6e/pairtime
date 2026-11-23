// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build !darwin

package darwin

import (
	"context"
	"errors"
	"net/netip"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/pkg/event"
)

type nopHandler struct{}

func (nopHandler) Power(time.Time, event.PowerOp) {}
func (nopHandler) Focus(time.Time, App)           {}

func TestUnsupported(t *testing.T) {
	tests := []struct {
		name string
		call func() error
	}{
		{
			name: "Presence off macOS is unsupported",
			call: func() error {
				_, err := Presence()
				return err
			},
		},
		{
			name: "Loop.Run off macOS is unsupported and closes Ready",
			call: func() error {
				l := NewLoop(nopHandler{})
				err := l.Run(context.Background())
				<-l.Ready()
				return err
			},
		},
		{
			name: "LoopbackOwner off macOS is unsupported",
			call: func() error {
				_, _, err := LoopbackOwner(netip.MustParseAddrPort("127.0.0.1:54321"), netip.MustParseAddrPort("127.0.0.1:7247"))
				return err
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.call(); !errors.Is(err, errors.ErrUnsupported) {
				t.Errorf("err = %v, want errors.ErrUnsupported", err)
			}
		})
	}
}
