package session_test

import (
	"bufio"
	"io"
	"os/exec"
	"strings"
	"testing"
	"time"

	"offline-complete/internal/session"
)

func TestStartsAProgramAndForwardsInputOutputAndExitStatus(t *testing.T) {
	s, err := session.Spawn("/bin/sh", "-c", "IFS= read -r line; printf 'received:%s\\n' \"$line\"; exit 7")
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	defer s.Close()

	writer := s.Writer()
	if _, err := writer.Write([]byte("hello\n")); err != nil {
		t.Fatalf("write: %v", err)
	}
	output, err := io.ReadAll(s.Reader())
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(string(output), "received:hello") {
		t.Fatalf("output = %q", output)
	}
	status, err := s.Wait()
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if status.Code != 7 {
		t.Fatalf("exit code = %d, want 7", status.Code)
	}
}

func TestSignalExitStatus(t *testing.T) {
	for _, tc := range []struct {
		signal string
		code   int
	}{{"TERM", 143}, {"KILL", 137}} {
		t.Run(tc.signal, func(t *testing.T) {
			s, err := session.Spawn("/bin/sh", "-c", "kill -"+tc.signal+" $$")
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			status, err := s.Wait()
			if err != nil {
				t.Fatal(err)
			}
			if status.Code != tc.code {
				t.Fatalf("exit code = %d, want %d", status.Code, tc.code)
			}
		})
	}
}

func TestClosingWriterDoesNotSubmitUnfinishedInput(t *testing.T) {
	s, err := session.Spawn("/bin/sh", "-c", "stty -echo; printf 'ready\\n'; IFS= read -r line; printf 'submitted:%s\\n' \"$line\"")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	r := bufio.NewReader(s.Reader())
	if _, err := r.ReadString('\n'); err != nil {
		t.Fatal(err)
	}
	w := s.Writer()
	if _, err := w.Write([]byte("unfinished")); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	lines := make(chan string, 1)
	go func() { line, _ := r.ReadString('\n'); lines <- line }()
	select {
	case line := <-lines:
		t.Fatalf("closing input submitted an unfinished line: %q", line)
	case <-time.After(300 * time.Millisecond):
	}
}

func TestExplicitTerminalEOFReachesTheProgram(t *testing.T) {
	s, err := session.Spawn("/bin/sh", "-c", "IFS= read -r line || printf 'saw-eof\\n'")
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	defer s.Close()

	if _, err := s.Writer().Write([]byte{4}); err != nil {
		t.Fatalf("write EOF: %v", err)
	}
	output, err := io.ReadAll(s.Reader())
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(string(output), "saw-eof") {
		t.Fatalf("output = %q", output)
	}
	status, err := s.Wait()
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if !status.Success() {
		t.Fatalf("exit code = %d", status.Code)
	}
}

func TestClosingASessionStopsAndReapsItsDirectChild(t *testing.T) {
	s, err := session.Spawn("/bin/sh", "-c", "printf '%s\\n' \"$$\"; exec /bin/sleep 30")
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	reader := bufio.NewReader(s.Reader())
	line, err := reader.ReadString('\n')
	if err != nil {
		t.Fatalf("read pid: %v", err)
	}
	pid := strings.TrimSpace(line)
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	status := exec.Command("/bin/kill", "-0", pid).Run()
	if status == nil {
		t.Fatalf("child %s still exists", pid)
	}
}

func TestTerminatingARunningProgramReapsIt(t *testing.T) {
	s, err := session.Spawn("/bin/sleep", "30")
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	defer s.Close()

	status, err := s.Terminate()
	if err != nil {
		t.Fatalf("Terminate: %v", err)
	}
	if status.Success() {
		t.Fatalf("terminated program reported success")
	}
	again, err := s.Wait()
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if again.Code != status.Code {
		t.Fatalf("cached status = %d, want %d", again.Code, status.Code)
	}
}

func TestTerminateAllowsHangupCleanupWhileWaitIsPending(t *testing.T) {
	s, err := session.Spawn("/bin/sh", "-c", "trap 'exit 42' HUP; printf 'ready\\n'; while :; do :; done")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := bufio.NewReader(s.Reader()).ReadString('\n'); err != nil {
		t.Fatal(err)
	}
	waited := make(chan session.ExitStatus, 1)
	go func() { status, _ := s.Wait(); waited <- status }()
	terminated := make(chan error, 1)
	go func() { _, err := s.Terminate(); terminated <- err }()
	select {
	case err := <-terminated:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(4 * time.Second):
		t.Fatal("Terminate blocked behind Wait")
	}
	if status := <-waited; status.Code != 42 {
		t.Fatalf("exit code = %d, want graceful cleanup 42", status.Code)
	}
}

func TestTerminateEscalatesWhenHangupIsIgnored(t *testing.T) {
	s, err := session.Spawn("/bin/sh", "-c", "trap '' HUP; printf 'ready\\n'; exec /bin/sleep 30")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := bufio.NewReader(s.Reader()).ReadString('\n'); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := s.Terminate(); done <- err }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(4 * time.Second):
		t.Fatal("Terminate did not escalate")
	}
}

func TestPropagatesWindowSizeToTheChild(t *testing.T) {
	s, err := session.Spawn("/bin/sh", "-c", "IFS= read -r line; stty size")
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	defer s.Close()

	if err := s.Resize(37, 111); err != nil {
		t.Fatalf("Resize: %v", err)
	}
	if _, err := s.Writer().Write([]byte("go\n")); err != nil {
		t.Fatalf("write: %v", err)
	}
	output, err := io.ReadAll(s.Reader())
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(string(output), "37 111") {
		t.Fatalf("output = %q", output)
	}
	status, err := s.Wait()
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if !status.Success() {
		t.Fatalf("exit code = %d", status.Code)
	}
}

func TestReportsAnErrorWhenTheProgramDoesNotExist(t *testing.T) {
	if _, err := session.Spawn("/this/path/does/not/exist/offline-complete"); err == nil {
		t.Fatal("Spawn succeeded for a missing program")
	} else if !strings.Contains(err.Error(), "failed to start program") {
		t.Fatalf("error = %v", err)
	}
}
