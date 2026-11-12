// Package session owns an explicitly started program attached to a
// pseudoterminal. It does not modify shell configuration and does not run a
// background service.
package session

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"

	"github.com/creack/pty"
	"golang.org/x/sys/unix"
)

// ExitStatus is the terminal status of the direct child process.
type ExitStatus struct {
	Code int
}

// Success reports whether the program exited with status 0.
func (s ExitStatus) Success() bool { return s.Code == 0 }

// Session is a pseudoterminal and its direct child process. The caller owns
// the session and should call Close when finished.
type Session struct {
	pty         *os.File
	cmd         *exec.Cmd
	writer      *writer
	signalGroup func(int, syscall.Signal) error
	killChild   func(*os.Process) error
	closePTY    func() error

	terminateGate chan struct{}
	terminateMu   sync.Mutex
	terminateErr  error
	killSent      bool
	groupFailed   bool
	closeOnce     sync.Once
	closeErr      error
	done          chan struct{}
	abandon       chan struct{}
	status        ExitStatus
	waitErr       error
}

// Spawn starts program with args attached to a new pseudoterminal sized 80x24.
func Spawn(program string, args ...string) (*Session, error) {
	return SpawnSized(24, 80, program, args...)
}

// SpawnSized starts a program with the specified initial terminal dimensions.
func SpawnSized(rows, cols uint16, program string, args ...string) (*Session, error) {
	return spawnSized(rows, cols, program, args, preparePTY, (*os.Process).Kill)
}

// SpawnCommand starts a caller-prepared command attached to a new
// pseudoterminal. The caller may set Env and ExtraFiles (for example an edit
// event descriptor) before calling.
func SpawnCommand(cmd *exec.Cmd, rows, cols uint16) (*Session, error) {
	if cmd == nil {
		return nil, errors.New("nil command")
	}
	return spawnCommand(cmd, rows, cols, preparePTY, (*os.Process).Kill)
}

func spawnSized(rows, cols uint16, program string, args []string, prepare func(*os.File, *exec.Cmd) (*os.File, error), kill func(*os.Process) error) (*Session, error) {
	cmd := exec.Command(program, args...)
	return spawnCommand(cmd, rows, cols, prepare, kill)
}

func spawnCommand(cmd *exec.Cmd, rows, cols uint16, prepare func(*os.File, *exec.Cmd) (*os.File, error), kill func(*os.Process) error) (*Session, error) {
	file, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: rows, Cols: cols})
	if err != nil {
		return nil, fmt.Errorf("failed to start program %q: %w", cmd.Path, err)
	}
	prepared, err := prepare(file, cmd)
	if err != nil {
		closeErr := file.Close()
		killErr := kill(cmd.Process)
		if killErr != nil && !errors.Is(killErr, os.ErrProcessDone) {
			// Reap if the child subsequently exits, without waiting for a failed kill.
			go func() { _ = cmd.Wait() }()
			return nil, errors.Join(fmt.Errorf("prepare PTY: %w", err), closeErr, fmt.Errorf("kill direct child: %w", killErr))
		}
		waited := make(chan error, 1)
		go func() { waited <- cmd.Wait() }()
		timer := time.NewTimer(750 * time.Millisecond)
		defer timer.Stop()
		var waitErr error
		select {
		case waitErr = <-waited:
			var exitErr *exec.ExitError
			if errors.As(waitErr, &exitErr) {
				waitErr = nil
			}
		case <-timer.C:
			waitErr = errors.New("reap direct child timed out; continuing in background")
		}
		return nil, errors.Join(fmt.Errorf("prepare PTY: %w", err), closeErr, waitErr)
	}
	s := &Session{pty: prepared, cmd: cmd, writer: &writer{file: prepared}, done: make(chan struct{}), abandon: make(chan struct{}), terminateGate: make(chan struct{}, 1), signalGroup: syscall.Kill, killChild: (*os.Process).Kill, closePTY: prepared.Close}
	go s.reap()
	return s, nil
}

func preparePTY(file *os.File, _ *exec.Cmd) (*os.File, error) {
	return preparePTYWithOps(file, func(fd uintptr) (int, error) {
		return unix.FcntlInt(fd, unix.F_DUPFD_CLOEXEC, 0)
	}, func(fd int) error {
		return unix.SetNonblock(fd, true)
	}, unix.Close)
}

func preparePTYWithOps(file *os.File, duplicate func(uintptr) (int, error), setNonblock func(int) error, closeFD func(int) error) (*os.File, error) {
	return preparePTYWithClose(file, (*os.File).Close, duplicate, setNonblock, closeFD)
}

func preparePTYWithClose(file *os.File, closeOriginal func(*os.File) error, duplicate func(uintptr) (int, error), setNonblock func(int) error, closeFD func(int) error) (*os.File, error) {
	// os.NewFile registers an already-nonblocking descriptor with Go's poller.
	// Duplicate before closing the original so there is a single owned file.
	raw, err := file.SyscallConn()
	var fd int = -1
	if err == nil {
		var setupErr error
		err = raw.Control(func(original uintptr) {
			fd, setupErr = duplicate(original)
			if setupErr == nil {
				setupErr = setNonblock(fd)
			}
		})
		err = errors.Join(err, setupErr)
	}
	if err != nil {
		if fd >= 0 {
			err = errors.Join(err, closeFD(fd))
		}
		return nil, err
	}
	if err := closeOriginal(file); err != nil {
		return nil, errors.Join(fmt.Errorf("close original PTY: %w", err), closeFD(fd))
	}
	return os.NewFile(uintptr(fd), "pty-master"), nil
}

// Reader returns the output side of the pseudoterminal.
func (s *Session) Reader() io.Reader { return s.pty }

// Writer returns the input side of the pseudoterminal. Close never injects
// terminal bytes: a PTY master cannot independently half-close its input.
// Close does not wait for or cancel writes already in progress.
func (s *Session) Writer() io.WriteCloser { return s.writer }

// Resize updates the pseudoterminal character dimensions.
func (s *Session) Resize(rows, cols uint16) error {
	raw, err := s.pty.SyscallConn()
	if err != nil {
		return err
	}
	var ioctlErr error
	err = raw.Control(func(fd uintptr) {
		ioctlErr = unix.IoctlSetWinsize(int(fd), unix.TIOCSWINSZ, &unix.Winsize{Row: rows, Col: cols})
	})
	return errors.Join(err, ioctlErr)
}

// Wait returns the direct child's exit status, caching the result. A non-zero
// exit is a status, not an error; errors are reserved for wait failures.
func (s *Session) Wait() (ExitStatus, error) {
	<-s.done
	return s.status, s.waitErr
}

func (s *Session) reap() {
	err := s.cmd.Wait()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			s.status.Code = exitErr.ExitCode()
			if status, ok := exitErr.Sys().(syscall.WaitStatus); ok && status.Signaled() {
				s.status.Code = 128 + int(status.Signal())
			}
		} else {
			s.waitErr = err
		}
	}
	close(s.done)
}

// Terminate rejects new writes and sends SIGHUP to the child's process group,
// then escalates to SIGKILL after a grace period. It does not cancel writes
// already in progress. After a successful kill it waits at most 750ms for
// the direct child; on timeout the reap goroutine continues in the background.
// Descendants that escape the group are not owned.
// The first termination error remains visible to later callers, including Close.
func (s *Session) Terminate() (ExitStatus, error) {
	return s.terminateWithGrace(nil)
}

func (s *Session) terminateWithGrace(grace <-chan time.Time) (status ExitStatus, err error) {
	_ = s.writer.Close()
	select {
	case s.terminateGate <- struct{}{}:
		defer func() { <-s.terminateGate }()
	case <-s.abandon:
		return ExitStatus{}, s.abandoned()
	}
	s.terminateMu.Lock()
	defer s.terminateMu.Unlock()
	defer func() {
		if s.terminateErr == nil {
			s.terminateErr = err
		} else {
			err = errors.Join(s.terminateErr, err)
		}
	}()
	select {
	case <-s.done:
		return s.Wait()
	default:
	}
	if err := s.abandoned(); err != nil {
		return ExitStatus{}, err
	}
	if s.killSent {
		return s.waitAfterKill()
	}
	if s.groupFailed {
		return s.killChildAfterGroupFailure(nil)
	}
	pid := s.cmd.Process.Pid
	hangupErr := s.signalGroup(-pid, syscall.SIGHUP)
	if err := s.abandoned(); err != nil {
		return ExitStatus{}, errors.Join(err, hangupErr)
	}
	if hangupErr != nil {
		return s.killChildAfterGroupFailure(fmt.Errorf("hangup process group: %w", hangupErr))
	}
	if grace == nil {
		timer := time.NewTimer(500 * time.Millisecond)
		defer timer.Stop()
		grace = timer.C
	}
	select {
	case <-s.done:
		return s.Wait()
	case <-grace:
		// A reaped child and the grace timer can become ready together. Avoid
		// choosing escalation when the child's exit is already observable.
		select {
		case <-s.done:
			return s.Wait()
		default:
		}
	case <-s.abandon:
		return ExitStatus{}, s.abandoned()
	}
	if err := s.abandoned(); err != nil {
		return ExitStatus{}, err
	}
	if err := s.signalGroup(-pid, syscall.SIGKILL); err != nil {
		return s.killChildAfterGroupFailure(fmt.Errorf("kill process group: %w", err))
	}
	s.killSent = true
	return s.waitAfterKill()
}

func (s *Session) abandoned() error {
	select {
	case <-s.abandon:
		return errors.New("termination abandoned after close timeout")
	default:
		return nil
	}
}

func (s *Session) killChildAfterGroupFailure(groupErr error) (ExitStatus, error) {
	s.groupFailed = true
	if err := s.abandoned(); err != nil {
		return ExitStatus{}, errors.Join(groupErr, err)
	}
	if err := s.killChild(s.cmd.Process); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return ExitStatus{}, errors.Join(groupErr, fmt.Errorf("kill direct child: %w", err))
	}
	s.killSent = true
	status, err := s.waitAfterKill()
	return status, errors.Join(groupErr, err)
}

// The single reap goroutine continues to own Wait after a timeout.
func (s *Session) waitAfterKill() (ExitStatus, error) {
	timer := time.NewTimer(750 * time.Millisecond)
	defer timer.Stop()
	return s.waitAfterKillUntil(timer.C)
}

func (s *Session) waitAfterKillUntil(deadline <-chan time.Time) (ExitStatus, error) {
	select {
	case <-s.done:
		return s.Wait()
	case <-deadline:
		// Both notifications may be ready; prefer an already reaped child.
		select {
		case <-s.done:
			return s.Wait()
		default:
		}
		return ExitStatus{}, errors.New("reap direct child timed out; continuing in background")
	}
}

func waitCloseResult(result <-chan error, deadline <-chan time.Time) (error, bool) {
	select {
	case err := <-result:
		return err, true
	case <-deadline:
		// A completed close and the deadline can be ready at the same time.
		select {
		case err := <-result:
			return err, true
		default:
			return nil, false
		}
	}
}

// Close rejects new writes and terminates the child. It starts PTY close after
// termination completes or 750ms elapses. PTY close gets 750ms; after it
// completes, termination gets at most another 1500ms. On timeout, later
// termination steps are abandoned when observed; an already started signal
// operation may still complete.
// Closing the PTY can discard unread output. A stalled close continues in the
// background after 750ms; neither timeout guarantees resource release or reap.
func (s *Session) Close() error {
	s.closeOnce.Do(func() {
		writerErr := s.writer.Close()
		terminated := make(chan error, 1)
		go func() {
			_, err := s.Terminate()
			terminated <- err
		}()
		timer := time.NewTimer(750 * time.Millisecond)
		defer timer.Stop()
		terminateErr, finished := waitCloseResult(terminated, timer.C)
		ptyClosed := make(chan error, 1)
		go func() { ptyClosed <- s.closePTY() }()
		closeTimer := time.NewTimer(750 * time.Millisecond)
		defer closeTimer.Stop()
		ptyErr, closeFinished := waitCloseResult(ptyClosed, closeTimer.C)
		if !closeFinished {
			close(s.abandon)
			ptyErr = errors.New("PTY close timed out; continuing in background")
			if !finished {
				select {
				case terminateErr = <-terminated:
				default:
					terminateErr = errors.New("termination abandoned; continuing in background")
				}
			}
			s.closeErr = errors.Join(writerErr, terminateErr, ptyErr)
			return
		}
		if !finished {
			waitTimer := time.NewTimer(1500 * time.Millisecond)
			defer waitTimer.Stop()
			terminateErr, finished = waitCloseResult(terminated, waitTimer.C)
			if !finished {
				close(s.abandon)
				terminateErr = errors.New("termination timed out; continuing in background")
			}
		}
		s.closeErr = errors.Join(writerErr, terminateErr, ptyErr)
	})
	return s.closeErr
}

type writer struct {
	file    *os.File
	mu      sync.Mutex
	ready   *sync.Cond
	writing bool
	closed  bool
}

func (w *writer) Write(data []byte) (int, error) {
	w.mu.Lock()
	if w.ready == nil {
		w.ready = sync.NewCond(&w.mu)
	}
	for w.writing && !w.closed {
		w.ready.Wait()
	}
	if w.closed {
		w.mu.Unlock()
		return 0, os.ErrClosed
	}
	w.writing = true
	w.mu.Unlock()

	n, err := w.file.Write(data)
	w.mu.Lock()
	w.writing = false
	w.ready.Broadcast()
	w.mu.Unlock()
	return n, err
}

func (w *writer) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.closed = true
	if w.ready != nil {
		w.ready.Broadcast()
	}
	return nil
}
