//go:build darwin

package ipc

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"

	"golang.org/x/sys/unix"
)

// PrivateListener owns one randomly named session directory and its socket.
// The base must already exist and be private to the effective user.
type PrivateListener struct {
	listener            *net.UnixListener
	path, dir           string
	dirInfo, socketInfo os.FileInfo
	mu                  sync.Mutex
	closed              bool
	closeErr            error
}

func NewPrivateListener(base string) (*PrivateListener, error) {
	return newPrivateListener(base, net.ListenUnix)
}

func newPrivateListener(base string, listen func(string, *net.UnixAddr) (*net.UnixListener, error)) (*PrivateListener, error) {
	return newPrivateListenerWithPermissions(base, listen, setSocketPermissions)
}

func newPrivateListenerWithPermissions(base string, listen func(string, *net.UnixAddr) (*net.UnixListener, error), setPermissions func(string) error) (*PrivateListener, error) {
	if err := validatePrivateBase(base); err != nil {
		return nil, err
	}
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return nil, err
	}
	dir := filepath.Join(base, "s-"+base64.RawURLEncoding.EncodeToString(random[:]))
	path := filepath.Join(dir, "socket")
	// Darwin's sockaddr_un has a 104-byte sun_path including the NUL.
	if len(path) >= 104 {
		return nil, errors.New("Unix socket path too long")
	}
	if err := os.Mkdir(dir, 0700); err != nil {
		return nil, err
	}
	dirInfo, err := os.Lstat(dir)
	if err != nil {
		return nil, err
	}
	stat, ok := dirInfo.Sys().(*syscall.Stat_t)
	if !ok || !dirInfo.IsDir() || dirInfo.Mode()&os.ModeSymlink != 0 || stat.Uid != uint32(os.Geteuid()) || dirInfo.Mode()&(os.ModePerm|os.ModeSetuid|os.ModeSetgid|os.ModeSticky) != 0700 {
		// The first observation does not prove this is the directory we created.
		return nil, fmt.Errorf("unsafe session directory: %s", dir)
	}
	listener, err := listen("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		cleanupErr := removeIfSame(dir, dirInfo)
		if cleanupErr != nil {
			cleanupErr = fmt.Errorf("cleanup session directory: %w", cleanupErr)
		}
		return nil, errors.Join(err, cleanupErr)
	}
	listener.SetUnlinkOnClose(false)
	// Reject a directory already replaced during bind before changing anything
	// at the pathname. This check does not make the subsequent operations atomic.
	var socketInfo os.FileInfo
	dirCurrent, err := checkedPath(dir, dirInfo)
	if err == nil && dirCurrent.Mode()&(os.ModePerm|os.ModeSetuid|os.ModeSetgid|os.ModeSticky) != 0700 {
		err = errors.New("session directory permissions are not private")
	}
	if err == nil {
		socketInfo, err = os.Lstat(path)
	}
	if err == nil && socketInfo.Mode()&os.ModeSocket == 0 {
		err = errors.New("created path is not a socket")
	}
	if err == nil {
		err = setPermissions(path)
	}
	if err == nil {
		var info os.FileInfo
		info, err = checkedPath(dir, dirInfo)
		if err == nil && info.Mode()&(os.ModePerm|os.ModeSetuid|os.ModeSetgid|os.ModeSticky) != 0700 {
			err = errors.New("session directory permissions are not private")
		}
	}
	if err == nil {
		var info os.FileInfo
		info, err = checkedPath(path, socketInfo)
		if err == nil && info.Mode()&(os.ModePerm|os.ModeSetuid|os.ModeSetgid|os.ModeSticky) != 0600 {
			err = errors.New("socket permissions are not private")
		}
	}
	if err != nil {
		if closeErr := listener.Close(); closeErr != nil {
			err = errors.Join(err, fmt.Errorf("close listener: %w", closeErr))
		}
		// A pathname observed after bind does not prove that the socket still
		// belongs to this listener. Leave it in place on setup failure.
		if cleanupErr := removeIfSame(dir, dirInfo); cleanupErr != nil {
			err = errors.Join(err, fmt.Errorf("cleanup session directory: %w", cleanupErr))
		}
		return nil, err
	}
	return &PrivateListener{listener: listener, path: path, dir: dir, dirInfo: dirInfo, socketInfo: socketInfo}, nil
}

// setSocketPermissions never follows a replaced final-component symlink.
func setSocketPermissions(path string) error {
	return unix.Fchmodat(unix.AT_FDCWD, path, 0600, unix.AT_SYMLINK_NOFOLLOW)
}

func validatePrivateBase(base string) error {
	if !filepath.IsAbs(base) || filepath.Clean(base) != base {
		return errors.New("private base must be an absolute clean path")
	}
	current := string(filepath.Separator)
	parts := strings.Split(strings.TrimPrefix(base, current), current)
	for i := -1; i < len(parts); i++ {
		if i >= 0 {
			current = filepath.Join(current, parts[i])
		}
		info, err := os.Lstat(current)
		if err != nil {
			return err
		}
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !ok || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || (stat.Uid != 0 && stat.Uid != uint32(os.Geteuid())) || info.Mode().Perm()&0022 != 0 {
			return fmt.Errorf("unsafe directory: %s", current)
		}
		if current == base && (stat.Uid != uint32(os.Geteuid()) || info.Mode()&(os.ModePerm|os.ModeSetuid|os.ModeSetgid|os.ModeSticky) != 0700) {
			return fmt.Errorf("base is not private: %s", base)
		}
	}
	return nil
}

func (p *PrivateListener) Path() string                { return p.path }
func (p *PrivateListener) Listener() *net.UnixListener { return p.listener }

// Close never recursively removes files and refuses to remove a replacement.
func (p *PrivateListener) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return p.closeErr
	}
	p.closed = true
	p.closeErr = p.listener.Close()
	// ServeAuthenticated closes its listener on shutdown. Closing that same
	// listener here must still perform the ownership-checked path cleanup.
	if errors.Is(p.closeErr, net.ErrClosed) {
		p.closeErr = nil
	}
	if err := samePath(p.dir, p.dirInfo); err != nil {
		p.closeErr = errors.Join(p.closeErr, err)
		return p.closeErr
	}
	if err := removeIfSame(p.path, p.socketInfo); err != nil {
		p.closeErr = errors.Join(p.closeErr, err)
		return p.closeErr
	}
	p.closeErr = errors.Join(p.closeErr, removeIfSame(p.dir, p.dirInfo))
	return p.closeErr
}

func samePath(path string, original os.FileInfo) error {
	_, err := checkedPath(path, original)
	return err
}

func checkedPath(path string, original os.FileInfo) (os.FileInfo, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	a, okA := info.Sys().(*syscall.Stat_t)
	b, okB := original.Sys().(*syscall.Stat_t)
	if !okA || !okB || !os.SameFile(info, original) || info.Mode().Type() != original.Mode().Type() || a.Uid != b.Uid {
		return nil, fmt.Errorf("refusing to use replaced path: %s", path)
	}
	return info, nil
}

func removeIfSame(path string, original os.FileInfo) error {
	if err := samePath(path, original); err != nil {
		return err
	}
	return os.Remove(path)
}
