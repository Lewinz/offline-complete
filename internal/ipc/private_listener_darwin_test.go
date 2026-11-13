//go:build darwin

package ipc

import (
	"context"
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func privateBase(t *testing.T) string {
	t.Helper()
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	dir, err := os.MkdirTemp(home, ".oc-ipc-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

func TestPrivateListenerRejectsSpecialModeOnPrivateBase(t *testing.T) {
	base := privateBase(t)
	if err := os.Chmod(base, 0700|os.ModeSticky); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(base)
	if err != nil || info.Mode()&os.ModeSticky == 0 {
		t.Fatalf("sticky bit not set: %v, %v", info, err)
	}
	listener, err := NewPrivateListener(base)
	if listener != nil {
		defer listener.Close()
	}
	if err == nil {
		t.Fatal("accepted base with sticky bit instead of exact 0700 mode")
	}
}

func TestPrivateListenerRejectsSpecialModeOnSessionDirectory(t *testing.T) {
	base := privateBase(t)
	var path string
	l, err := newPrivateListenerWithPermissions(base, net.ListenUnix, func(p string) error {
		path = p
		if err := setSocketPermissions(p); err != nil {
			return err
		}
		return os.Chmod(filepath.Dir(p), 0700|os.ModeSticky)
	})
	if err == nil {
		l.Close()
		t.Fatal("accepted session directory with sticky bit")
	}
	info, statErr := os.Lstat(path)
	if statErr != nil || info.Mode()&os.ModeSocket == 0 {
		t.Fatalf("unverified socket was not retained: %v, %v", info, statErr)
	}
}

func TestPrivateListenerRejectsSpecialModeOnSocket(t *testing.T) {
	base := privateBase(t)
	var path string
	l, err := newPrivateListenerWithPermissions(base, net.ListenUnix, func(p string) error {
		path = p
		return os.Chmod(p, 0600|os.ModeSticky)
	})
	if err == nil {
		l.Close()
		t.Fatal("accepted socket with sticky bit")
	}
	info, statErr := os.Lstat(path)
	if statErr != nil || info.Mode()&os.ModeSocket == 0 {
		t.Fatalf("unverified socket was not retained: %v, %v", info, statErr)
	}
}

func TestPrivateListenerRejectsRestrictedDirectoryBeforeBind(t *testing.T) {
	if os.Getenv("IPC_TEST_RESTRICTIVE_UMASK") != "1" {
		cmd := exec.Command(os.Args[0], "-test.run=^TestPrivateListenerRejectsRestrictedDirectoryBeforeBind$")
		cmd.Env = append(os.Environ(), "IPC_TEST_RESTRICTIVE_UMASK=1")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("isolated test: %v\n%s", err, out)
		}
		return
	}
	base := privateBase(t)
	syscall.Umask(0777)
	called := false
	l, err := newPrivateListener(base, func(network string, addr *net.UnixAddr) (*net.UnixListener, error) {
		called = true
		return net.ListenUnix(network, addr)
	})
	if l != nil {
		_ = l.Close()
	}
	if err == nil || called {
		t.Fatalf("restricted directory: error=%v, bind called=%t", err, called)
	}
	entries, err := os.ReadDir(base)
	if err != nil || len(entries) != 1 || !entries[0].IsDir() {
		t.Fatalf("unverified directory not retained: %v, %v", entries, err)
	}
}

func TestPrivateListenerDoesNotChangeSocketInInsecureSessionDirectory(t *testing.T) {
	var path string
	l, err := newPrivateListener(privateBase(t), func(network string, addr *net.UnixAddr) (*net.UnixListener, error) {
		listener, err := net.ListenUnix(network, addr)
		if err != nil {
			return nil, err
		}
		listener.SetUnlinkOnClose(false)
		path = addr.Name
		if err = os.Chmod(path, 0666); err == nil {
			err = os.Chmod(filepath.Dir(path), 0755)
		}
		if err != nil {
			listener.Close()
			return nil, err
		}
		return listener, nil
	})
	if err == nil {
		l.Close()
		t.Fatal("accepted insecure session directory")
	}
	info, statErr := os.Lstat(path)
	if statErr != nil || info.Mode()&os.ModeSocket == 0 || info.Mode().Perm() != 0666 {
		t.Fatalf("socket permissions changed in insecure directory: %v %v", info, statErr)
	}
}

func TestPrivateListenerDoesNotChangeSocketInReplacedSessionDirectory(t *testing.T) {
	base := privateBase(t)
	var replacement *net.UnixListener
	var path string
	l, err := newPrivateListener(base, func(network string, addr *net.UnixAddr) (*net.UnixListener, error) {
		original, err := net.ListenUnix(network, addr)
		if err != nil {
			return nil, err
		}
		original.SetUnlinkOnClose(false)
		path = addr.Name
		dir := filepath.Dir(path)
		if err = os.Rename(dir, dir+"-original"); err == nil {
			err = os.Mkdir(dir, 0700)
		}
		if err == nil {
			replacement, err = net.ListenUnix(network, addr)
		}
		if err == nil {
			replacement.SetUnlinkOnClose(false)
			err = os.Chmod(path, 0666)
		}
		if err != nil {
			original.Close()
			return nil, err
		}
		return original, nil
	})
	if replacement != nil {
		defer replacement.Close()
	}
	if err == nil {
		l.Close()
		t.Fatal("accepted replaced session directory")
	}
	info, statErr := os.Lstat(path)
	if statErr != nil || info.Mode()&os.ModeSocket == 0 || info.Mode().Perm() != 0666 {
		t.Fatalf("replacement socket permissions changed: %v %v", info, statErr)
	}
}

func TestPrivateListenerRejectsInsecureSessionDirectoryAfterPermissionSetup(t *testing.T) {
	base := privateBase(t)
	var path string
	l, err := newPrivateListenerWithPermissions(base, net.ListenUnix, func(p string) error {
		path = p
		if err := setSocketPermissions(p); err != nil {
			return err
		}
		return os.Chmod(filepath.Dir(p), 0755)
	})
	if err == nil {
		l.Close()
		t.Fatal("accepted session directory with permissions 0755")
	}
	info, statErr := os.Lstat(path)
	if statErr != nil || info.Mode()&os.ModeSocket == 0 {
		t.Fatalf("unverified socket should be preserved: %v %v", info, statErr)
	}
}

func TestPrivateListenerLifecycle(t *testing.T) {
	base := privateBase(t)
	l, err := NewPrivateListener(base)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(l.Path())
	if err != nil || info.Mode()&os.ModeSocket == 0 || info.Mode().Perm() != 0600 {
		t.Fatalf("socket: %v %v", info, err)
	}
	dir := filepath.Dir(l.Path())
	info, err = os.Lstat(dir)
	if err != nil || info.Mode().Perm() != 0700 {
		t.Fatalf("session directory: %v %v", info, err)
	}
	ch := make(chan error, 1)
	go func() {
		c, err := AcceptAuthenticated(l.Listener(), "session", time.Second)
		if err == nil {
			c.Close()
		}
		ch <- err
	}()
	c, err := DialAuthenticated(l.Path(), "session", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	c.Close()
	if err := <-ch; err != nil {
		t.Fatal(err)
	}
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(dir); !os.IsNotExist(err) {
		t.Fatalf("directory remains: %v", err)
	}
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestPrivateListenerCleanupAfterServerCancellation(t *testing.T) {
	l, err := NewPrivateListener(privateBase(t))
	if err != nil {
		t.Fatal(err)
	}
	path := l.Path()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- ServeAuthenticated(ctx, l.Listener(), "session", time.Second, 2, func(context.Context, *net.UnixConn) {})
	}()
	c, err := DialAuthenticated(path, "session", time.Second)
	if err != nil {
		cancel()
		<-done
		l.Close()
		t.Fatal(err)
	}
	c.Close()
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if err := l.Close(); err != nil {
		t.Fatalf("cleanup after server cancellation: %v", err)
	}
	if _, err := os.Lstat(filepath.Dir(path)); !os.IsNotExist(err) {
		t.Fatalf("session directory remains: %v", err)
	}
}

func TestSocketPermissionsDoNotFollowReplacementSymlink(t *testing.T) {
	base := privateBase(t)
	target := filepath.Join(base, "target")
	if err := os.WriteFile(target, []byte("keep"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(target, 0644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(base, "socket")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	_ = setSocketPermissions(link)
	info, err := os.Stat(target)
	if err != nil || info.Mode().Perm() != 0644 {
		t.Fatalf("symlink target permissions changed: %v %v", info, err)
	}
}

func TestPrivateListenerRejectsUnsafeBase(t *testing.T) {
	base := privateBase(t)
	if err := os.Chmod(base, 0755); err != nil {
		t.Fatal(err)
	}
	if l, err := NewPrivateListener(base); err == nil {
		l.Close()
		t.Fatal("accepted non-private directory")
	}
	if err := os.Chmod(base, 0700); err != nil {
		t.Fatal(err)
	}
	link := base + "-link"
	if err := os.Symlink(base, link); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Remove(link) })
	if l, err := NewPrivateListener(link); err == nil {
		l.Close()
		t.Fatal("accepted symlink")
	}
}

func TestPrivateListenerReportsFailedCreationCleanup(t *testing.T) {
	base := privateBase(t)
	listenErr := errors.New("listen failed")
	var other string
	l, err := newPrivateListener(base, func(_ string, addr *net.UnixAddr) (*net.UnixListener, error) {
		other = filepath.Join(filepath.Dir(addr.Name), "other")
		if writeErr := os.WriteFile(other, []byte("keep"), 0600); writeErr != nil {
			t.Fatal(writeErr)
		}
		return nil, listenErr
	})
	if l != nil || !errors.Is(err, listenErr) || !errors.Is(err, syscall.ENOTEMPTY) {
		t.Fatalf("want listen and nonempty cleanup errors, got listener %v, error %v", l, err)
	}
	contents, readErr := os.ReadFile(other)
	if readErr != nil || string(contents) != "keep" {
		t.Fatalf("unowned file lost: %q %v", contents, readErr)
	}
}

func TestPrivateListenerCreationDoesNotRemoveReplacement(t *testing.T) {
	base := privateBase(t)
	var path string
	l, err := newPrivateListener(base, func(network string, addr *net.UnixAddr) (*net.UnixListener, error) {
		listener, err := net.ListenUnix(network, addr)
		if err != nil {
			return nil, err
		}
		listener.SetUnlinkOnClose(false)
		path = addr.Name
		if err := os.Remove(path); err != nil {
			listener.Close()
			return nil, err
		}
		if err := os.WriteFile(path, []byte("keep"), 0600); err != nil {
			listener.Close()
			return nil, err
		}
		return listener, nil
	})
	if err == nil {
		l.Close()
		t.Fatal("accepted replaced socket")
	}
	if !errors.Is(err, syscall.ENOTEMPTY) {
		t.Fatalf("missing retained directory cleanup error: %v", err)
	}
	contents, readErr := os.ReadFile(path)
	if readErr != nil || string(contents) != "keep" {
		t.Fatalf("replacement lost: %q %v", contents, readErr)
	}
}

func TestPrivateListenerFailedPermissionsPreservesReplacementSocket(t *testing.T) {
	base := privateBase(t)
	var path string
	var replacement *net.UnixListener
	permissionErr := errors.New("permission setup failed")
	l, err := newPrivateListenerWithPermissions(base, func(network string, addr *net.UnixAddr) (*net.UnixListener, error) {
		original, err := net.ListenUnix(network, addr)
		if err != nil {
			return nil, err
		}
		original.SetUnlinkOnClose(false)
		path = addr.Name
		if err := os.Remove(path); err != nil {
			original.Close()
			return nil, err
		}
		replacement, err = net.ListenUnix(network, addr)
		if err != nil {
			original.Close()
			return nil, err
		}
		replacement.SetUnlinkOnClose(false)
		return original, nil
	}, func(string) error { return permissionErr })
	if replacement != nil {
		defer replacement.Close()
	}
	if l != nil || !errors.Is(err, permissionErr) || !errors.Is(err, syscall.ENOTEMPTY) {
		t.Fatalf("want permission and retained directory errors, got listener %v, error %v", l, err)
	}
	info, statErr := os.Lstat(path)
	if statErr != nil || info.Mode()&os.ModeSocket == 0 {
		t.Fatalf("replacement socket lost: %v %v", info, statErr)
	}
}

func TestPrivateListenerRejectsSessionDirectoryReplacedDuringPermissionSetup(t *testing.T) {
	var dir, moved string
	l, err := newPrivateListenerWithPermissions(privateBase(t), net.ListenUnix, func(name string) error {
		dir = filepath.Dir(name)
		moved = dir + "-moved"
		if err := os.Rename(dir, moved); err != nil {
			return err
		}
		if err := os.Symlink(moved, dir); err != nil {
			return err
		}
		return setSocketPermissions(name)
	})
	if err == nil {
		l.Close()
		t.Fatal("accepted replaced session directory")
	}
	info, statErr := os.Lstat(dir)
	if statErr != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("replacement symlink lost: %v %v", info, statErr)
	}
	if info, statErr = os.Lstat(filepath.Join(moved, "socket")); statErr != nil || info.Mode()&os.ModeSocket == 0 {
		t.Fatalf("moved socket lost: %v %v", info, statErr)
	}
}

func TestPrivateListenerRejectsInsecureSocketAfterPermissionSetup(t *testing.T) {
	var path string
	l, err := newPrivateListenerWithPermissions(privateBase(t), net.ListenUnix, func(name string) error {
		path = name
		if err := setSocketPermissions(name); err != nil {
			return err
		}
		return os.Chmod(name, 0666)
	})
	if err == nil {
		l.Close()
		t.Fatal("accepted socket with insecure permissions")
	}
	if !errors.Is(err, syscall.ENOTEMPTY) {
		t.Fatalf("missing retained directory cleanup error: %v", err)
	}
	if _, statErr := os.Lstat(path); statErr != nil {
		t.Fatalf("socket unexpectedly removed: %v", statErr)
	}
}

func TestPrivateListenerRejectsSocketReplacedDuringPermissionSetup(t *testing.T) {
	base := privateBase(t)
	var path string
	var replacement *net.UnixListener
	l, err := newPrivateListenerWithPermissions(base, net.ListenUnix, func(name string) error {
		path = name
		if err := os.Remove(name); err != nil {
			return err
		}
		var err error
		replacement, err = net.ListenUnix("unix", &net.UnixAddr{Name: name, Net: "unix"})
		if err != nil {
			return err
		}
		replacement.SetUnlinkOnClose(false)
		return setSocketPermissions(name)
	})
	if replacement != nil {
		defer replacement.Close()
	}
	if err == nil {
		l.Close()
		t.Fatal("accepted a socket replaced during permission setup")
	}
	if !errors.Is(err, syscall.ENOTEMPTY) {
		t.Fatalf("missing retained directory cleanup error: %v", err)
	}
	info, statErr := os.Lstat(path)
	if statErr != nil || info.Mode()&os.ModeSocket == 0 {
		t.Fatalf("replacement socket lost: %v %v", info, statErr)
	}
}

func TestPrivateListenerDoesNotRemoveReplacement(t *testing.T) {
	base := privateBase(t)
	l, err := NewPrivateListener(base)
	if err != nil {
		t.Fatal(err)
	}
	path := l.Path()
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := l.Close(); err == nil {
		t.Fatal("replacement not reported")
	}
	contents, err := os.ReadFile(path)
	if err != nil || string(contents) != "keep" {
		t.Fatalf("replacement lost: %q %v", contents, err)
	}
}

func TestPrivateListenerDoesNotRemoveOtherFiles(t *testing.T) {
	base := privateBase(t)
	l, err := NewPrivateListener(base)
	if err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(filepath.Dir(l.Path()), "other")
	if err := os.WriteFile(other, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := l.Close(); err == nil {
		t.Fatal("nonempty session directory not reported")
	}
	contents, err := os.ReadFile(other)
	if err != nil || string(contents) != "keep" {
		t.Fatalf("unowned file lost: %q %v", contents, err)
	}
}

func TestPrivateListenerRejectsLongSocketPath(t *testing.T) {
	base := privateBase(t)
	long := filepath.Join(base, "this-directory-name-makes-the-unix-domain-socket-path-far-too-long-for-darwin")
	if err := os.Mkdir(long, 0700); err != nil {
		t.Fatal(err)
	}
	if l, err := NewPrivateListener(long); err == nil {
		l.Close()
		t.Fatal("accepted long socket path")
	}
	entries, err := os.ReadDir(long)
	if err != nil || len(entries) != 0 {
		t.Fatalf("left session behind: %v %v", entries, err)
	}
}
