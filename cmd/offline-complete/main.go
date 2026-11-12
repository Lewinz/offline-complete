// Command offline-complete runs an explicitly named program under a local
// pseudoterminal and forwards the caller's standard streams to it.
package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"offline-complete/internal/session"
)

func main() {
	var code int
	var err error
	if rulesPath := resolveRulesPath(os.Getenv("OFFLINE_COMPLETE_RULES")); rulesPath != "" {
		code, err = runAgent(rulesPath, os.Args[1:], os.Stdin, os.Stdout)
	} else {
		code, err = run(os.Args[1:], os.Stdin, os.Stdout)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "offline-complete: %v\n", err)
		os.Exit(1)
	}
	os.Exit(code)
}

func startResize(s *session.Session, terminal *os.File, signals <-chan os.Signal, failures ...chan<- error) func() error {
	stop := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		for {
			select {
			case <-signals:
				size, err := terminalSize(terminal)
				if err != nil {
					err = fmt.Errorf("read terminal size: %w", err)
					if len(failures) > 0 {
						failures[0] <- err
						done <- s.Close()
					} else {
						done <- errors.Join(err, s.Close())
					}
					return
				}
				if size.Rows > 0 && size.Cols > 0 {
					if err := s.Resize(size.Rows, size.Cols); err != nil {
						err = fmt.Errorf("resize terminal: %w", err)
						if len(failures) > 0 {
							failures[0] <- err
							done <- s.Close()
						} else {
							done <- errors.Join(err, s.Close())
						}
						return
					}
				}
			case <-stop:
				done <- nil
				return
			}
		}
	}()
	return func() error { close(stop); return <-done }
}

func startTermination(s interface{ Close() error }, signals <-chan os.Signal, interrupted chan<- syscall.Signal, notified ...chan<- struct{}) func() error {
	stop := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		var sig os.Signal
		select {
		case sig = <-signals:
		case <-stop:
			// A signal queued before shutdown must not lose to the stop case.
			select {
			case sig = <-signals:
			default:
				done <- nil
				return
			}
		}
		interrupted <- sig.(syscall.Signal)
		if len(notified) > 0 {
			close(notified[0])
		}
		done <- s.Close()
	}()
	return func() error { close(stop); return <-done }
}

func exitCodeAfterCleanup(code int, err error, interrupted <-chan syscall.Signal) int {
	if err == nil {
		select {
		case sig := <-interrupted:
			return 128 + int(sig)
		default:
		}
	}
	return code
}

type stoppableReader struct {
	reader io.Reader
	stop   <-chan struct{}
}

func (r stoppableReader) Read(p []byte) (int, error) {
	select {
	case <-r.stop:
		return 0, io.EOF
	default:
	}
	n, err := r.reader.Read(p)
	select {
	case <-r.stop:
		return 0, io.EOF
	default:
		return n, err
	}
}

func copyInputUntilStopped(dst io.Writer, src io.Reader, stop <-chan struct{}) error {
	_, err := io.Copy(dst, stoppableReader{reader: src, stop: stop})
	return err
}

func forwardInput(s interface{ Close() error }, writer io.WriteCloser, copyInput func(io.Writer) error, inputErr chan<- error) error {
	err := copyInput(writer)
	if closeErr := writer.Close(); closeErr != nil {
		err = errors.Join(err, fmt.Errorf("close input writer: %w", closeErr))
	}
	if err != nil {
		inputErr <- err
		return s.Close()
	}
	return nil
}

type outputWriter struct {
	io.Writer
	err error
}

func (w *outputWriter) Write(p []byte) (int, error) {
	n, err := w.Writer.Write(p)
	if err == nil && n != len(p) {
		err = io.ErrShortWrite
	}
	if err != nil {
		w.err = err
	}
	return n, err
}

type progressWriter struct {
	writer   io.Writer
	progress chan<- struct{}
}

func (w progressWriter) Write(p []byte) (int, error) {
	n, err := w.writer.Write(p)
	if n > 0 {
		select {
		case w.progress <- struct{}{}:
		default:
		}
	}
	return n, err
}

// waitOutputDrain allows continued output after the direct child exits, but
// bounds an idle output worker. It cannot cancel a blocked read or write.
func waitOutputDrain(done <-chan struct{}, progress <-chan struct{}, timeout time.Duration, abort ...<-chan struct{}) error {
	var stopProgress <-chan struct{}
	if len(abort) != 0 {
		stopProgress = abort[0]
	}
	return waitOutputDrainWithFailures(done, progress, nil, nil, timeout, stopProgress)
}

func waitOutputDrainWithFailures(done <-chan struct{}, progress <-chan struct{}, inputErr, resizeErr <-chan error, timeout time.Duration, stopProgress <-chan struct{}) (resultErr error) {
	collectInput := func(err error) {
		resultErr = errors.Join(resultErr, fmt.Errorf("forward input: %w", err))
		inputErr = nil
		progress = nil
	}
	collectResize := func(err error) {
		resultErr = errors.Join(resultErr, err)
		resizeErr = nil
		progress = nil
	}
	defer func() {
		select {
		case err := <-inputErr:
			collectInput(err)
		default:
		}
		select {
		case err := <-resizeErr:
			collectResize(err)
		default:
		}
	}()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	for {
		select {
		case <-done:
			return resultErr
		case err := <-inputErr:
			collectInput(err)
		case err := <-resizeErr:
			collectResize(err)
		case <-stopProgress:
			progress = nil
			stopProgress = nil
		case <-progress:
			timer.Reset(timeout)
		case <-timer.C:
			select {
			case <-done:
				return resultErr
			case <-progress:
				timer.Reset(timeout)
			default:
				return errors.Join(resultErr, errors.New("output worker did not stop; it may still be writing"))
			}
		}
	}
}

// forwardOutput distinguishes the caller's output errors from PTY read errors.
func forwardOutput(dst io.Writer, src io.Reader, interrupted <-chan syscall.Signal, stop ...<-chan struct{}) (int, error) {
	if len(stop) > 0 {
		src = stoppableReader{reader: src, stop: stop[0]}
	}
	w := &outputWriter{Writer: dst}
	_, err := io.Copy(w, src)
	if w.err != nil {
		return 0, fmt.Errorf("forward output: %w", w.err)
	}
	select {
	case sig := <-interrupted:
		if err == nil || errors.Is(err, os.ErrClosed) {
			return 128 + int(sig), nil
		}
	default:
	}
	if err != nil {
		return 0, fmt.Errorf("forward output: %w", err)
	}
	return 0, nil
}

type childWaitResult struct {
	status session.ExitStatus
	err    error
}

func waitForChild(s interface {
	Wait() (session.ExitStatus, error)
}, interrupted <-chan syscall.Signal, inputErr <-chan error, resizeFailures ...<-chan error) (int, error) {
	var resizeErr <-chan error
	if len(resizeFailures) > 0 {
		resizeErr = resizeFailures[0]
	}
	done := make(chan childWaitResult, 1)
	go func() {
		status, err := s.Wait()
		done <- childWaitResult{status, err}
	}()
	return awaitChild(done, interrupted, inputErr, resizeErr)
}

// awaitChild collects errors already available at the time it returns without
// waiting for an uncooperative child or input reader.
func awaitChild(done <-chan childWaitResult, interrupted <-chan syscall.Signal, inputErr, resizeErr <-chan error) (code int, resultErr error) {
	waitDone := false
	defer func() {
		if !waitDone {
			select {
			case result := <-done:
				resultErr = errors.Join(resultErr, result.err)
			default:
			}
		}
		select {
		case err := <-inputErr:
			resultErr = errors.Join(resultErr, fmt.Errorf("forward input: %w", err))
		default:
		}
		select {
		case err := <-resizeErr:
			resultErr = errors.Join(resultErr, err)
		default:
		}
	}()
	var sig syscall.Signal
	var result childWaitResult
	select {
	case result = <-done:
		waitDone = true
	case err := <-inputErr:
		return 1, fmt.Errorf("forward input: %w", err)
	case err := <-resizeErr:
		return 1, err
	case sig = <-interrupted:
		timer := time.NewTimer(4 * time.Second)
		defer timer.Stop()
		select {
		case result = <-done:
			waitDone = true
		case err := <-inputErr:
			return 1, fmt.Errorf("forward input: %w", err)
		case err := <-resizeErr:
			return 1, err
		case <-timer.C:
			return 1, errors.New("waiting for direct child after signal timed out; child may still be running")
		}
	}
	if result.err != nil {
		return 1, result.err
	}
	if sig == 0 {
		select {
		case sig = <-interrupted:
		default:
		}
	}
	if sig != 0 {
		return 128 + int(sig), nil
	}
	if result.status.Code < 0 {
		return 1, nil
	}
	return result.status.Code, nil
}

// waitInputShutdown bounds cleanup if an input worker is stuck in a read or
// PTY write. A timeout does not mean that the worker has exited.
func waitInputShutdown(done <-chan struct{}, timeout time.Duration) error {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-done:
		return nil
	case <-timer.C:
		// Completion may have become ready alongside the timer.
		select {
		case <-done:
			return nil
		default:
			return errors.New("input worker did not stop; it may still be running")
		}
	}
}

var initialTerminalSize = terminalSize

func run(args []string, stdin io.Reader, stdout io.Writer) (int, error) {
	return runWithInputWriter(args, stdin, stdout, func(w io.WriteCloser) io.WriteCloser { return w })
}

type runHooks struct {
	spawn   func(rows, cols uint16) (*session.Session, error)
	started func(*session.Session, io.WriteCloser) error
	cleanup func()
}

func runWithInputWriter(args []string, stdin io.Reader, stdout io.Writer, wrapWriter func(io.WriteCloser) io.WriteCloser, hooks ...runHooks) (code int, resultErr error) {
	if len(args) == 0 {
		return 1, errors.New("usage: offline-complete PROGRAM [ARG ...]")
	}
	var hook runHooks
	if len(hooks) != 0 {
		hook = hooks[0]
	}
	rows, cols := uint16(24), uint16(80)
	var terminal *os.File
	var resizeSignals chan os.Signal
	terminationSignals := make(chan os.Signal, 1)
	signal.Notify(terminationSignals, syscall.SIGTERM, syscall.SIGHUP, syscall.SIGINT)
	defer signal.Stop(terminationSignals)
	if file, ok := stdin.(*os.File); ok {
		// Register before reading the initial dimensions so a concurrent resize
		// is either reflected in that read or queued for the resize worker.
		resizeSignals = make(chan os.Signal, 1)
		signal.Notify(resizeSignals, syscall.SIGWINCH)
		defer signal.Stop(resizeSignals)
		if size, err := initialTerminalSize(file); err == nil {
			terminal = file
			if size.Rows > 0 && size.Cols > 0 {
				rows, cols = size.Rows, size.Cols
			}
		}
	}
	if terminal != nil {
		restore, err := makeRaw(terminal)
		if err != nil {
			return 1, err
		}
		defer func() { resultErr = errors.Join(resultErr, restore()) }()
	}
	spawn := hook.spawn
	if spawn == nil {
		spawn = func(rows, cols uint16) (*session.Session, error) {
			return session.SpawnSized(rows, cols, args[0], args[1:]...)
		}
	}
	if hook.cleanup != nil {
		defer hook.cleanup()
	}
	s, err := spawn(rows, cols)
	if err != nil {
		return 1, err
	}
	writer := wrapWriter(s.Writer())
	if hook.started != nil {
		if err := hook.started(s, writer); err != nil {
			_ = s.Close()
			return 1, err
		}
	}
	interrupted := make(chan syscall.Signal, 1)
	terminationStarted := make(chan struct{})
	stopTermination := startTermination(s, terminationSignals, interrupted, terminationStarted)
	var stopResize func() error
	resizeErr := make(chan error, 1)
	if terminal != nil {
		stopResize = startResize(s, terminal, resizeSignals, resizeErr)
	}

	var pollInput *os.File
	if file, ok := stdin.(*os.File); ok && canPollInput(file, terminal != nil) {
		pollInput = file
	}
	inputErr := make(chan error, 1)
	stopInput := make(chan struct{})
	inputDone := make(chan struct{})
	go func() {
		defer close(inputDone)
		// Close caches its error; the deferred session cleanup reports it.
		_ = forwardInput(s, writer, func(writer io.Writer) error {
			if pollInput != nil {
				return copyTerminalInput(pollInput, writer, stopInput)
			}
			return copyInputUntilStopped(writer, stdin, stopInput)
		}, inputErr)
	}()
	defer func() {
		close(stopInput)
		if stopResize != nil {
			resultErr = errors.Join(resultErr, stopResize())
			select {
			case err := <-resizeErr:
				resultErr = errors.Join(resultErr, err)
			default:
			}
		}
		if stopTermination != nil {
			resultErr = errors.Join(resultErr, stopTermination())
		}
		resultErr = errors.Join(resultErr, s.Close())
		if pollInput != nil {
			resultErr = errors.Join(resultErr, waitInputShutdown(inputDone, time.Second))
		}
		select {
		case err := <-inputErr:
			resultErr = errors.Join(resultErr, fmt.Errorf("forward input: %w", err))
		default:
		}
		code = exitCodeAfterCleanup(code, resultErr, interrupted)
	}()

	type outputResult struct {
		code int
		err  error
	}
	outputDone := make(chan outputResult, 1)
	outputFinished := make(chan struct{})
	outputProgress := make(chan struct{}, 1)
	go func() {
		code, err := forwardOutput(progressWriter{stdout, outputProgress}, s.Reader(), interrupted, stopInput)
		outputDone <- outputResult{code, err}
		close(outputFinished)
	}()
	childDone := make(chan struct{})
	go func() {
		_, _ = s.Wait()
		close(childDone)
	}()
	var output outputResult
	var forwardingFailure error
	var shutdownStarted bool
	var childExited bool
	select {
	case output = <-outputDone:
	case <-childDone:
		shutdownStarted = true
		childExited = true
	case <-terminationStarted:
		shutdownStarted = true
	case err := <-inputErr:
		forwardingFailure = fmt.Errorf("forward input: %w", err)
		shutdownStarted = true
	case err := <-resizeErr:
		forwardingFailure = err
		shutdownStarted = true
	}
	if shutdownStarted {
		var progress <-chan struct{}
		if childExited {
			progress = outputProgress
		}
		if err := waitOutputDrainWithFailures(outputFinished, progress, inputErr, resizeErr, time.Second, terminationStarted); err != nil {
			// The worker may have completed with its own error while draining.
			// Do not wait for a stalled worker, but preserve a result already sent.
			select {
			case output = <-outputDone:
				return 1, errors.Join(forwardingFailure, err, output.err)
			default:
				return 1, errors.Join(forwardingFailure, err)
			}
		}
		output = <-outputDone
	}
	if forwardingFailure != nil || output.err != nil {
		return 1, errors.Join(forwardingFailure, output.err)
	}
	if output.code != 0 {
		return output.code, nil
	}
	select {
	case err := <-inputErr:
		return 1, fmt.Errorf("forward input: %w", err)
	default:
	}
	return waitForChild(s, interrupted, inputErr, resizeErr)
}

// resolveRulesPath keeps launchers generated by older builds working after the
// installed pack moved from rules.json to encrypted rules.bin.
func resolveRulesPath(path string) string {
	if path == "" {
		return ""
	}
	if _, err := os.Stat(path); err == nil {
		return path
	}
	if filepath.Ext(path) == ".json" {
		alternate := strings.TrimSuffix(path, ".json") + ".bin"
		if _, err := os.Stat(alternate); err == nil {
			return alternate
		}
	}
	return path
}
