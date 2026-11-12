//go:build darwin

package main

import (
	"fmt"
	"io"
	"os"
	"sync"
	"syscall"
	"unsafe"

	"github.com/creack/pty"
	"golang.org/x/sys/unix"
)

// canPollInput limits kqueue use to terminals, pipes and sockets; regular files
// and arbitrary readers retain the ordinary io.Copy path.
func canPollInput(file *os.File, terminal bool) bool {
	if terminal {
		return true
	}
	info, err := file.Stat()
	return err == nil && info.Mode()&(os.ModeNamedPipe|os.ModeSocket) != 0
}

// copyTerminalInput waits for readiness in bounded intervals so the worker can
// stop before returning. The input must have no concurrent readers.
func copyTerminalInput(file *os.File, dst io.Writer, stop <-chan struct{}) error {
	return copyTerminalInputWithRead(file, dst, stop, syscall.Read)
}

func copyTerminalInputWithRead(file *os.File, dst io.Writer, stop <-chan struct{}, read func(int, []byte) (int, error)) error {
	kq, err := syscall.Kqueue()
	if err != nil {
		return fmt.Errorf("create input queue: %w", err)
	}
	defer syscall.Close(kq)
	raw, err := file.SyscallConn()
	if err != nil {
		return fmt.Errorf("access terminal input: %w", err)
	}
	var watchErr error
	if err := raw.Control(func(fd uintptr) {
		var change syscall.Kevent_t
		syscall.SetKevent(&change, int(fd), syscall.EVFILT_READ, syscall.EV_ADD)
		_, watchErr = syscall.Kevent(kq, []syscall.Kevent_t{change}, nil, nil)
	}); err != nil {
		return fmt.Errorf("access terminal input: %w", err)
	}
	if watchErr != nil {
		return fmt.Errorf("watch terminal input: %w", watchErr)
	}
	var events [1]syscall.Kevent_t
	buf := make([]byte, 4096)
	for {
		select {
		case <-stop:
			return nil
		default:
		}
		timeout := syscall.NsecToTimespec(50 * 1000 * 1000)
		n, err := syscall.Kevent(kq, nil, events[:], &timeout)
		if err == syscall.EINTR {
			continue
		}
		if err != nil {
			return fmt.Errorf("wait for terminal input: %w", err)
		}
		if n == 0 {
			continue
		}
		select {
		case <-stop:
			return nil
		default:
		}
		var readErr error
		if err := raw.Control(func(fd uintptr) { n, readErr = read(int(fd), buf) }); err != nil {
			return fmt.Errorf("access terminal input: %w", err)
		}
		err = readErr
		select {
		case <-stop:
			return nil
		default:
		}
		if err == syscall.EINTR || err == syscall.EAGAIN {
			continue
		}
		if err != nil {
			return err
		}
		if n == 0 {
			return nil
		}
		written, err := dst.Write(buf[:n])
		if err != nil {
			return err
		}
		if written != n {
			return io.ErrShortWrite
		}
	}
}

func terminalSize(file *os.File) (*pty.Winsize, error) {
	raw, err := file.SyscallConn()
	if err != nil {
		return nil, err
	}
	var size *unix.Winsize
	var ioctlErr error
	if err := raw.Control(func(fd uintptr) { size, ioctlErr = unix.IoctlGetWinsize(int(fd), unix.TIOCGWINSZ) }); err != nil {
		return nil, err
	}
	if ioctlErr != nil {
		return nil, ioctlErr
	}
	return &pty.Winsize{Rows: size.Row, Cols: size.Col, X: size.Xpixel, Y: size.Ypixel}, nil
}

func terminalState(file *os.File, request uintptr, state *syscall.Termios) error {
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, file.Fd(), request, uintptr(unsafe.Pointer(state)))
	if errno != 0 {
		return errno
	}
	return nil
}

func makeRaw(file *os.File) (func() error, error) {
	var original syscall.Termios
	if err := terminalState(file, syscall.TIOCGETA, &original); err != nil {
		return nil, fmt.Errorf("read terminal state: %w", err)
	}
	raw := original
	raw.Iflag &^= syscall.IGNBRK | syscall.BRKINT | syscall.PARMRK | syscall.ISTRIP | syscall.INLCR | syscall.IGNCR | syscall.ICRNL | syscall.IXON
	raw.Oflag &^= syscall.OPOST
	raw.Lflag &^= syscall.ECHO | syscall.ECHONL | syscall.ICANON | syscall.ISIG | syscall.IEXTEN
	raw.Cflag &^= syscall.CSIZE | syscall.PARENB
	raw.Cflag |= syscall.CS8
	raw.Cc[syscall.VMIN] = 1
	raw.Cc[syscall.VTIME] = 0
	if err := terminalState(file, syscall.TIOCSETA, &raw); err != nil {
		return nil, fmt.Errorf("set raw terminal: %w", err)
	}
	var mu sync.Mutex
	restored := false
	return func() error {
		mu.Lock()
		defer mu.Unlock()
		if restored {
			return nil
		}
		if err := terminalState(file, syscall.TIOCSETA, &original); err != nil {
			return fmt.Errorf("restore terminal: %w", err)
		}
		restored = true
		return nil
	}, nil
}
