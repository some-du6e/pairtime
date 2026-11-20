// SPDX-License-Identifier: AGPL-3.0-or-later

package darwin

// macPresence reads presence from CoreGraphics and IOKit, loading each the
// first time it is used.
type macPresence struct{}

func osPresenceSource() (presenceSource, error) {
	if err := loadCF(); err != nil {
		return nil, err
	}
	return macPresence{}, nil
}

func (macPresence) cgIdle(eventType uint32) (float64, bool) {
	if loadCG() != nil {
		return 0, false
	}
	return cg.secondsSinceLastEventType(cgStateHIDSystem, eventType), true
}

// hidIdle reads HIDIdleTime from the IOHIDSystem service in the I/O
// Registry, the value `ioreg -c IOHIDSystem` prints, without starting ioreg.
func (macPresence) hidIdle() (int64, bool) {
	if loadIOKit() != nil {
		return 0, false
	}
	// IOServiceGetMatchingService consumes the matching dictionary. Port 0
	// is kIOMainPortDefault.
	svc := iokit.getMatchingService(0, iokit.serviceMatching("IOHIDSystem"))
	if svc == 0 {
		return 0, false
	}
	defer iokit.objectRelease(svc)
	prop := iokit.createCFProperty(svc, iokit.keyHIDIdleTime, 0, 0)
	if prop == 0 {
		return 0, false
	}
	defer cf.release(prop)
	return cfInt64(prop)
}

// session reads the screen lock from the login session's dictionary. A
// session that isn't on the console, because another user switched in, is
// locked as far as presence goes: its owner can't give it input.
func (macPresence) session() (locked, ok bool) {
	if loadCG() != nil {
		return false, false
	}
	dict := cg.sessionCopyCurrent()
	if dict == 0 {
		return false, false
	}
	defer cf.release(dict)
	locked = cfTrue(cf.dictGetValue(dict, cg.keyScreenIsLocked))
	if on := cf.dictGetValue(dict, cg.keyOnConsole); on != 0 && !cfTrue(on) {
		locked = true
	}
	return locked, true
}

// listenAccess reports whether this process has the Input Monitoring
// permission, without asking for it, and false when macOS can't say.
func listenAccess() (granted, known bool) {
	if loadCG() != nil || cg.preflightListen == nil {
		return false, false
	}
	return cg.preflightListen(), true
}
