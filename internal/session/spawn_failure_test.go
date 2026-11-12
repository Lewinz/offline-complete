package session

import (
	"bufio"
	"errors"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestSpawnPreparationOriginalCloseFailure(t *testing.T) {
	closeErr := errors.New("original master close failed")
	var child *exec.Cmd
	duplicate := -1
	s, err := spawnSized(24, 80, "/bin/sleep", []string{"30"}, func(f *os.File, cmd *exec.Cmd) (*os.File, error) {
		child = cmd
		return preparePTYWithClose(f, func(file *os.File) error {
			return errors.Join(file.Close(), closeErr)
		}, func(fd uintptr) (int, error) {
			var err error
			duplicate, err = unix.FcntlInt(fd, unix.F_DUPFD_CLOEXEC, 0)
			return duplicate, err
		}, func(fd int) error { return unix.SetNonblock(fd, true) }, unix.Close)
	}, (*os.Process).Kill)
	if s != nil {
		defer s.Close()
	}
	if !errors.Is(err, closeErr) {
		t.Fatalf("error = %v, want original close error", err)
	}
	if child == nil || child.ProcessState == nil {
		t.Error("started child was not reaped")
	}
	if duplicate < 0 {
		t.Fatal("descriptor was not duplicated")
	}
	if _, err := unix.FcntlInt(uintptr(duplicate), unix.F_GETFD, 0); !errors.Is(err, unix.EBADF) {
		t.Errorf("duplicate descriptor remains open: %v", err)
	}
}

func TestSpawnPreparationDescriptorFailures(t *testing.T) {
	for _, stage := range []string{"duplicate", "nonblock", "duplicate-close"} {
		t.Run(stage, func(t *testing.T) {
			setupErr := errors.New("descriptor setup failed")
			closeErr := errors.New("descriptor close failed")
			duplicate := -1
			var child *exec.Cmd
			var original *os.File
			_, err := spawnSized(24, 80, "/bin/sleep", []string{"30"}, func(f *os.File, cmd *exec.Cmd) (*os.File, error) {
				child, original = cmd, f
				return preparePTYWithOps(f, func(fd uintptr) (int, error) {
					if stage == "duplicate" {
						return -1, setupErr
					}
					var err error
					duplicate, err = unix.FcntlInt(fd, unix.F_DUPFD_CLOEXEC, 0)
					return duplicate, err
				}, func(fd int) error {
					return setupErr
				}, func(fd int) error {
					err := unix.Close(fd)
					if stage == "duplicate-close" {
						return errors.Join(err, closeErr)
					}
					return err
				})
			}, (*os.Process).Kill)
			if !errors.Is(err, setupErr) {
				t.Fatalf("error = %v, want setup error", err)
			}
			if stage == "duplicate-close" && !errors.Is(err, closeErr) {
				t.Errorf("error = %v, want duplicate close error", err)
			}
			if child == nil || child.ProcessState == nil {
				t.Fatal("started child was not reaped")
			}
			if _, err := original.Stat(); !errors.Is(err, os.ErrClosed) {
				t.Errorf("original master remains open: %v", err)
			}
			if duplicate >= 0 {
				if _, err := unix.FcntlInt(uintptr(duplicate), unix.F_GETFD, 0); !errors.Is(err, unix.EBADF) {
					t.Errorf("duplicate descriptor remains open: %v", err)
				}
			}
		})
	}
}

func TestSpawnPreparationFailureReapsChild(t *testing.T) {
	setupErr := errors.New("prepare failed")
	var child *exec.Cmd
	_, err := spawnSized(24, 80, "/bin/sleep", []string{"30"}, func(f *os.File, cmd *exec.Cmd) (*os.File, error) {
		child = cmd
		return nil, setupErr
	}, (*os.Process).Kill)
	if !errors.Is(err, setupErr) {
		t.Fatalf("error = %v, want preparation error", err)
	}
	if child == nil || child.ProcessState == nil || !child.ProcessState.Exited() && !child.ProcessState.Sys().(syscall.WaitStatus).Signaled() {
		t.Fatal("started child was not reaped")
	}
}

func TestSpawnPreparationDelayedReapReturnsError(t *testing.T) {
	setupErr := errors.New("prepare failed")
	childReady := make(chan *exec.Cmd, 1)
	done := make(chan error, 1)
	go func() {
		_, err := spawnSized(24, 80, "/bin/sh", []string{"-c", "trap '' HUP; printf 'ready\\n'; exec /bin/sleep 30"}, func(f *os.File, cmd *exec.Cmd) (*os.File, error) {
			childReady <- cmd
			if _, err := bufio.NewReader(f).ReadString('\n'); err != nil {
				return nil, err
			}
			return nil, setupErr
		}, func(*os.Process) error { return nil })
		done <- err
	}()
	child := <-childReady
	defer child.Process.Kill()
	select {
	case err := <-done:
		if !errors.Is(err, setupErr) || !strings.Contains(err.Error(), "reap direct child timed out") {
			t.Errorf("error = %v, want preparation and reap timeout errors", err)
		}
	case <-time.After(2 * time.Second):
		_ = child.Process.Kill()
		<-done
		t.Fatal("spawn waited indefinitely for direct child after successful kill")
	}
}

func TestSpawnPreparationKillFailureDoesNotWait(t *testing.T) {
	setupErr := errors.New("prepare failed")
	childReady := make(chan *exec.Cmd, 1)
	done := make(chan error, 1)
	go func() {
		_, err := spawnSized(24, 80, "/bin/sh", []string{"-c", "trap '' HUP; printf 'ready\\n'; exec /bin/sleep 30"}, func(f *os.File, cmd *exec.Cmd) (*os.File, error) {
			childReady <- cmd
			if _, err := bufio.NewReader(f).ReadString('\n'); err != nil {
				return nil, err
			}
			return nil, setupErr
		}, func(*os.Process) error { return syscall.EPERM })
		done <- err
	}()
	child := <-childReady
	defer child.Process.Kill()
	select {
	case err := <-done:
		if !errors.Is(err, setupErr) || !errors.Is(err, syscall.EPERM) {
			t.Errorf("error = %v, want preparation and kill errors", err)
		}
	case <-time.After(2 * time.Second):
		if child != nil {
			_ = child.Process.Kill()
		}
		<-done
		t.Fatal("spawn waited after direct child kill failed")
	}
}
