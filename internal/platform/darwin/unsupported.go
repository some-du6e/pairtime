// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build !darwin

package darwin

import "errors"

// The package's logic builds everywhere, so ci.yml's Linux job tests it
// with fakes; only the calls into Apple's frameworks need macOS.

func newBackend() (backend, error) { return nil, errors.ErrUnsupported }

func osPresenceSource() (presenceSource, error) { return nil, errors.ErrUnsupported }
