// SPDX-License-Identifier: AGPL-3.0-or-later

package install

import (
	"errors"
	"fmt"

	"golang.org/x/sys/windows/registry"
)

// hkcu is the registry of the user pairtime runs as, through
// golang.org/x/sys/windows/registry.
type hkcu struct{}

func systemRegistry() Registry { return hkcu{} }

func (hkcu) Get(key, name string) (string, uint32, bool, error) {
	k, err := registry.OpenKey(registry.CURRENT_USER, key, registry.QUERY_VALUE)
	if errors.Is(err, registry.ErrNotExist) {
		return "", 0, false, nil
	}
	if err != nil {
		return "", 0, false, err
	}
	defer k.Close()
	v, typ, err := k.GetStringValue(name)
	if errors.Is(err, registry.ErrNotExist) {
		return "", 0, false, nil
	}
	if err != nil {
		return "", 0, false, err
	}
	return v, typ, true, nil
}

func (hkcu) Set(key, name, value string, typ uint32) error {
	k, _, err := registry.CreateKey(registry.CURRENT_USER, key, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	switch typ {
	case RegSZ:
		return k.SetStringValue(name, value)
	case RegExpandSZ:
		return k.SetExpandStringValue(name, value)
	default:
		return fmt.Errorf("pairtime doesn't write registry values of type %d", typ)
	}
}

func (hkcu) Delete(key, name string) error {
	k, err := registry.OpenKey(registry.CURRENT_USER, key, registry.SET_VALUE)
	if errors.Is(err, registry.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer k.Close()
	if err := k.DeleteValue(name); err != nil && !errors.Is(err, registry.ErrNotExist) {
		return err
	}
	return nil
}
