// SPDX-License-Identifier: AGPL-3.0-or-later

package install

import (
	"errors"
	"fmt"
	"io"
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

// maxFileSize bounds a file target pairtime reads: 4 MiB, far more than
// any real settings file or login item holds. A bigger one is refused
// before it is parsed, since its parse tree would take many times its size
// in memory, and install refuses an edit that would make a file bigger,
// since uninstall couldn't read it back.
const maxFileSize = 4 << 20

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
	linfo, lerr := os.Lstat(path)
	link := lerr == nil && linfo.Mode()&fs.ModeSymlink != 0
	info, err := os.Stat(path)
	if errors.Is(err, fs.ErrNotExist) {
		if lerr == nil {
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
	f, err := os.Open(path)
	if err != nil {
		return Content{}, err
	}
	defer f.Close()
	// Reading one byte more than the limit catches a file that grew after
	// the Stat.
	data, err := io.ReadAll(io.LimitReader(f, maxFileSize+1))
	if err != nil {
		return Content{}, err
	}
	if info.Size() > maxFileSize || len(data) > maxFileSize {
		return Content{}, fmt.Errorf("%s is bigger than the %d MiB a settings file or login item could need, so pairtime leaves it alone", path, maxFileSize>>20)
	}
	return Content{Exists: true, Data: data, Mode: info.Mode().Perm(), link: link}, nil
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
		if _, lerr := os.Lstat(d); lerr == nil {
			// A link to a folder that isn't there, such as ~/.claude
			// pointing into a dotfiles checkout that's gone. Making the
			// folder would go through the link, and uninstall would then
			// take the teen's link for pairtime's folder.
			return nil, fmt.Errorf("%s is a link to a folder that isn't there, so pairtime leaves it alone", d)
		}
		dirs = append(dirs, d)
		if filepath.Dir(d) == d {
			break
		}
	}
	slices.Reverse(dirs)
	return dirs, nil
}

// addDirs returns dirs with each directory in more that it lacks, still
// outermost first. Both are folders above the same file, so a shorter
// path is an outer folder.
func addDirs(dirs, more []string) []string {
	for _, d := range more {
		if !slices.Contains(dirs, d) {
			dirs = append(dirs, d)
		}
	}
	slices.SortStableFunc(dirs, func(a, b string) int { return len(a) - len(b) })
	return dirs
}

// makeDirs makes dirs, outermost first, and returns those that were there
// already: someone else made them since the plan, such as the desktop
// making ~/.config/autostart while the teen read the consent screen, so
// they aren't pairtime's. A real directory that is already there is fine;
// anything else at its name is an error.
func makeDirs(dirs []string) ([]string, error) {
	var existed []string
	for _, d := range dirs {
		err := os.Mkdir(d, newDirMode)
		if errors.Is(err, fs.ErrExist) {
			if info, lerr := os.Lstat(d); lerr == nil && info.Mode().Type() == fs.ModeDir {
				existed = append(existed, d)
				continue
			}
			return existed, fmt.Errorf("%s was made since the plan, and isn't a folder", d)
		}
		if err != nil {
			return existed, err
		}
	}
	return existed, nil
}
