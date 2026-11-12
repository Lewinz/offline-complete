//go:build darwin

package ipc

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// EnsurePrivateBase creates one private directory beneath an existing private
// parent, or validates an existing one. It never changes or removes existing
// objects. Callers must also protect the parent from same-user modification.
func EnsurePrivateBase(base string) error {
	if !filepath.IsAbs(base) || filepath.Clean(base) != base || base == string(filepath.Separator) {
		return errors.New("private base must be an absolute clean non-root path")
	}
	parent := filepath.Dir(base)
	if err := validatePrivateBase(parent); err != nil {
		return err
	}
	parentInfo, err := os.Lstat(parent)
	if err != nil {
		return err
	}
	fd, err := unix.Open(parent, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return err
	}
	f := os.NewFile(uintptr(fd), parent)
	defer f.Close()
	openedInfo, err := f.Stat()
	if err != nil {
		return err
	}
	if !os.SameFile(parentInfo, openedInfo) {
		return errors.New("private parent changed while opening")
	}
	if err := samePath(parent, parentInfo); err != nil {
		return err
	}
	if err := unix.Mkdirat(fd, filepath.Base(base), 0700); err != nil && !errors.Is(err, unix.EEXIST) {
		return fmt.Errorf("create private base: %w", err)
	}
	childFD, err := unix.Openat(fd, filepath.Base(base), unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return fmt.Errorf("open private base: %w", err)
	}
	child := os.NewFile(uintptr(childFD), base)
	defer child.Close()
	var stat unix.Stat_t
	if err := unix.Fstat(childFD, &stat); err != nil {
		return err
	}
	if stat.Uid != uint32(os.Geteuid()) || stat.Mode&07777 != 0700 {
		return errors.New("base is not private")
	}
	info, err := child.Stat()
	if err != nil {
		return err
	}
	if err := samePath(parent, parentInfo); err != nil {
		return err
	}
	if err := samePath(base, info); err != nil {
		return err
	}
	return validatePrivateBase(base)
}
