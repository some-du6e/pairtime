// SPDX-License-Identifier: AGPL-3.0-or-later

package install

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"

	"github.com/some-du6e/pairtime/internal/agent/home"
)

// newFileMode is the mode of a file pairtime creates outside its own
// directories, and newDirMode that of a directory it makes for one. Both
// are for the teen's account only; the programs that read them run as the
// teen.
const (
	newFileMode fs.FileMode = 0o600
	newDirMode  fs.FileMode = 0o700
)

// read returns what t holds.
func (in *Installer) read(t Target) (Content, error) {
	if !t.isRegistry() {
		return readFile(t.Path)
	}
	if in.Registry == nil {
		return Content{}, fmt.Errorf("%s: there is no registry on this system", t)
	}
	v, typ, ok, err := in.Registry.Get(t.Key, t.Name)
	if err != nil {
		return Content{}, fmt.Errorf("reading %s: %w", t, err)
	}
	if !ok {
		return Content{}, nil
	}
	return Content{Exists: true, Data: []byte(v), Type: typ}, nil
}

// readFile returns what the file at path holds. A symbolic link is
// followed, as the program that reads the file follows it.
func readFile(path string) (Content, error) {
	info, err := os.Stat(path)
	if errors.Is(err, fs.ErrNotExist) {
		if _, lerr := os.Lstat(path); lerr == nil {
			return Content{}, fmt.Errorf("%s is a link to a file that isn't there, so pairtime leaves it alone", path)
		}
		return Content{}, nil
	}
	if err != nil {
		return Content{}, err
	}
	if !info.Mode().IsRegular() {
		return Content{}, fmt.Errorf("%s isn't a regular file, so pairtime leaves it alone", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return Content{}, err
	}
	return Content{Exists: true, Data: data, Mode: info.Mode().Perm()}, nil
}

// write makes t hold c. A file is replaced by an atomic rename with c's
// mode, so a harness reading it at that moment sees the old bytes or the
// new ones, never a mix. When the file is a symbolic link, the file it
// points to is replaced and the link stays, so dotfiles kept in a
// repository stay linked.
func (in *Installer) write(t Target, c Content) error {
	if t.isRegistry() {
		if in.Registry == nil {
			return fmt.Errorf("%s: there is no registry on this system", t)
		}
		if !c.Exists {
			return in.Registry.Delete(t.Key, t.Name)
		}
		return in.Registry.Set(t.Key, t.Name, string(c.Data), c.Type)
	}
	if !c.Exists {
		if err := os.Remove(t.Path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		return nil
	}
	path := t.Path
	if info, err := os.Lstat(path); err == nil && info.Mode()&fs.ModeSymlink != 0 {
		if path, err = filepath.EvalSymlinks(path); err != nil {
			return err
		}
	}
	if err := home.WriteFile(path, c.Data); err != nil {
		return err
	}
	mode := c.Mode
	if mode == 0 {
		mode = newFileMode
	}
	if mode == newFileMode {
		return nil // home.WriteFile's mode already
	}
	return os.Chmod(path, mode)
}

// missingDirs returns the directories that have to be made before path can
// be written, outermost first.
func missingDirs(path string) ([]string, error) {
	var dirs []string
	for d := filepath.Dir(path); ; d = filepath.Dir(d) {
		info, err := os.Stat(d)
		if err == nil {
			if !info.IsDir() {
				return nil, fmt.Errorf("%s isn't a directory", d)
			}
			break
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return nil, err
		}
		dirs = append(dirs, d)
		if filepath.Dir(d) == d {
			break
		}
	}
	slices.Reverse(dirs)
	return dirs, nil
}

// makeDirs makes dirs, outermost first. One that is already there is fine.
func makeDirs(dirs []string) error {
	for _, d := range dirs {
		if err := os.Mkdir(d, newDirMode); err != nil && !errors.Is(err, fs.ErrExist) {
			return err
		}
	}
	return nil
}
