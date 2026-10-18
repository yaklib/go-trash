// Package trash moves files and directories into the trash as described by
// the FreeDesktop.org Trash specification, version 1.0.
package trash

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Trash moves the file or directory at path into the trash.
//
// Files on the same filesystem as $XDG_DATA_HOME go to the home trash
// ($XDG_DATA_HOME/Trash). Files on other filesystems go to $topdir/.Trash/$uid
// or, failing that, $topdir/.Trash-$uid, where $topdir is the mount point.
// Files are never copied across filesystems: if no trash directory can be
// used, an error is returned and the file is left untouched.
//
// A symbolic link is trashed itself; its target is not followed.
func Trash(path string) error {
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	fi, err := os.Lstat(abs)
	if err != nil {
		return err
	}

	home, err := homeTrash()
	if err != nil {
		return err
	}
	if err := mkTrashDir(home); err != nil {
		return err
	}
	homeFi, err := os.Stat(home)
	if err != nil {
		return err
	}

	trashDir, origPath := home, abs
	if device(fi) != device(homeFi) {
		top := topDir(filepath.Dir(abs), device(fi))
		if trashDir, err = topDirTrash(top, os.Getuid()); err != nil {
			return err
		}
		if origPath, err = filepath.Rel(top, abs); err != nil {
			return err
		}
	}
	return moveToTrash(trashDir, abs, origPath)
}

// homeTrash returns $XDG_DATA_HOME/Trash, defaulting $XDG_DATA_HOME to
// ~/.local/share when unset or not absolute.
func homeTrash() (string, error) {
	data := os.Getenv("XDG_DATA_HOME")
	if !filepath.IsAbs(data) {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		data = filepath.Join(home, ".local", "share")
	}
	return filepath.Join(data, "Trash"), nil
}

// topDir walks up from dir and returns the highest ancestor on device dev,
// i.e. the mount point.
func topDir(dir string, dev uint64) string {
	for {
		parent := filepath.Dir(dir)
		if parent == dir {
			return dir
		}
		fi, err := os.Lstat(parent)
		if err != nil || device(fi) != dev {
			return dir
		}
		dir = parent
	}
}

// topDirTrash returns the trash directory to use under the mount point top:
// $top/.Trash/$uid if $top/.Trash is a sticky, non-symlink directory we can
// create our subdirectory in, otherwise $top/.Trash-$uid.
func topDirTrash(top string, uid int) (string, error) {
	shared := filepath.Join(top, ".Trash")
	if fi, err := os.Lstat(shared); err == nil && fi.IsDir() && fi.Mode()&os.ModeSticky != 0 {
		dir := filepath.Join(shared, strconv.Itoa(uid))
		if err := mkTrashDir(dir); err == nil {
			return dir, nil
		}
	}
	dir := filepath.Join(top, ".Trash-"+strconv.Itoa(uid))
	if err := mkTrashDir(dir); err != nil {
		return "", err
	}
	return dir, nil
}

func mkTrashDir(dir string) error {
	for _, sub := range []string{"files", "info"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o700); err != nil {
			return err
		}
	}
	return nil
}

// moveToTrash creates the .trashinfo file first (atomically, picking a unique
// name) and then renames src into $trash/files.
func moveToTrash(trashDir, src, origPath string) error {
	info, name, err := createInfo(trashDir, filepath.Base(src))
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(info, "[Trash Info]\nPath=%s\nDeletionDate=%s\n",
		escape(filepath.ToSlash(origPath)), time.Now().Format("2006-01-02T15:04:05"))
	if cerr := info.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(src, filepath.Join(trashDir, "files", name))
	}
	if err != nil {
		os.Remove(info.Name())
		return err
	}
	return nil
}

// createInfo opens $trash/info/<name>.trashinfo with O_EXCL, trying
// base, base.2, base.3, ... until a name is free in both info and files.
func createInfo(trashDir, base string) (*os.File, string, error) {
	name := base
	for i := 2; ; i++ {
		if _, err := os.Lstat(filepath.Join(trashDir, "files", name)); err == nil {
			name = base + "." + strconv.Itoa(i)
			continue
		}
		f, err := os.OpenFile(filepath.Join(trashDir, "info", name+".trashinfo"),
			os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err == nil {
			return f, name, nil
		}
		if !errors.Is(err, fs.ErrExist) {
			return nil, "", err
		}
		name = base + "." + strconv.Itoa(i)
	}
}

// escape percent-encodes s as in RFC 2396: every byte except unreserved
// characters and "/" is written as %XX. Non-ASCII and control bytes
// (including newline) are therefore always encoded.
func escape(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case 'A' <= c && c <= 'Z', 'a' <= c && c <= 'z', '0' <= c && c <= '9',
			strings.IndexByte("-_.!~*'()/", c) >= 0:
			b.WriteByte(c)
		default:
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}
