//go:build darwin

package main

import (
	"io"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"syscall"
	"testing"
	"time"

	"offline-complete/internal/session"
)

// This is an opt-in characterization, not a claim about all Darwin versions.
func TestDescendantPTYHangupCharacterization(t *testing.T) {
	if os.Getenv("OFFLINE_COMPLETE_PTY_DIAGNOSTIC") != "1" {
		t.Skip("set OFFLINE_COMPLETE_PTY_DIAGNOSTIC=1 to run PTY characterization")
	}
	t.Setenv("OFFLINE_COMPLETE_DESCENDANT_TEST", "parent")
	resultPath := t.TempDir() + "/result"
	t.Setenv("OFFLINE_COMPLETE_RESULT", resultPath)
	s, err := session.Spawn(os.Args[0], "-test.run=^TestDescendantPTYHelper$")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	status, err := s.Wait()
	if err != nil || status.Code != 0 {
		t.Fatalf("parent: status %v, error %v", status, err)
	}
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		result, err := os.ReadFile(resultPath)
		if err == nil {
			t.Logf("descendant write with master open: %s", result)
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("descendant did not report write result")
}

// Compare writes by the same descendant before and after its session leader exits.
// This observes the host, rather than asserting behavior on other macOS versions.
func TestDescendantPTYLeaderLifetimeCharacterization(t *testing.T) {
	if os.Getenv("OFFLINE_COMPLETE_PTY_DIAGNOSTIC") != "1" {
		t.Skip("set OFFLINE_COMPLETE_PTY_DIAGNOSTIC=1 to run PTY characterization")
	}
	t.Setenv("OFFLINE_COMPLETE_DESCENDANT_TEST", "compare-parent")
	resultPath := t.TempDir() + "/result"
	t.Setenv("OFFLINE_COMPLETE_RESULT", resultPath)
	s, err := session.Spawn(os.Args[0], "-test.run=^TestDescendantPTYHelper$")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	go func() { _, _ = io.Copy(io.Discard, s.Reader()) }()
	status, err := s.Wait()
	if err != nil || status.Code != 0 {
		t.Fatalf("parent: status %v, error %v", status, err)
	}
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		before, err1 := os.ReadFile(resultPath + ".before")
		after, err2 := os.ReadFile(resultPath + ".after")
		if err1 == nil && err2 == nil {
			t.Logf("same descendant write with leader alive: %s; after leader exit: %s", before, after)
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	before, err1 := os.ReadFile(resultPath + ".before")
	after, err2 := os.ReadFile(resultPath + ".after")
	t.Fatalf("descendant results: before %q (%v), after %q (%v)", before, err1, after, err2)
}

func TestDescendantPTYHelper(t *testing.T) {
	switch os.Getenv("OFFLINE_COMPLETE_DESCENDANT_TEST") {
	case "compare-parent":
		cmd := exec.Command(os.Args[0], "-test.run=^TestDescendantPTYHelper$")
		cmd.Env = append(os.Environ(), "OFFLINE_COMPLETE_DESCENDANT_TEST=compare-child", "OFFLINE_COMPLETE_PARENT_PID="+strconv.Itoa(os.Getpid()))
		cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
		if cmd.Start() != nil {
			os.Exit(20)
		}
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			if _, err := os.Stat(os.Getenv("OFFLINE_COMPLETE_RESULT") + ".before"); err == nil {
				os.Exit(0)
			}
			time.Sleep(10 * time.Millisecond)
		}
		os.Exit(21)
	case "compare-child":
		signal.Ignore(syscall.SIGHUP)
		resultPath := os.Getenv("OFFLINE_COMPLETE_RESULT")
		writeResult := func(suffix string) {
			_, err := os.Stdout.WriteString("descendant output " + suffix + "\n")
			result := "ok"
			if err != nil {
				result = err.Error()
			}
			if os.WriteFile(resultPath+suffix, []byte(result), 0600) != nil {
				os.Exit(22)
			}
		}
		writeResult(".before")
		parent, _ := strconv.Atoi(os.Getenv("OFFLINE_COMPLETE_PARENT_PID"))
		deadline := time.Now().Add(3 * time.Second)
		for os.Getppid() == parent && time.Now().Before(deadline) {
			time.Sleep(10 * time.Millisecond)
		}
		if os.Getppid() == parent {
			os.Exit(23)
		}
		writeResult(".after")
		os.Exit(0)
	case "parent":
		r, w, err := os.Pipe()
		if err != nil {
			os.Exit(10)
		}
		cmd := exec.Command(os.Args[0], "-test.run=^TestDescendantPTYHelper$")
		cmd.Env = append(os.Environ(), "OFFLINE_COMPLETE_DESCENDANT_TEST=child", "OFFLINE_COMPLETE_PARENT_PID="+strconv.Itoa(os.Getpid()))
		cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
		cmd.ExtraFiles = []*os.File{w}
		if cmd.Start() != nil {
			os.Exit(11)
		}
		_ = w.Close()
		var ready [1]byte
		if _, err := io.ReadFull(r, ready[:]); err != nil {
			os.Exit(12)
		}
		os.Exit(0)
	case "child":
		signal.Ignore(syscall.SIGHUP)
		ready := os.NewFile(3, "ready")
		_, _ = ready.Write([]byte{'x'})
		_ = ready.Close()
		parent, _ := strconv.Atoi(os.Getenv("OFFLINE_COMPLETE_PARENT_PID"))
		deadline := time.Now().Add(3 * time.Second)
		for os.Getppid() == parent && time.Now().Before(deadline) {
			time.Sleep(10 * time.Millisecond)
		}
		if os.Getppid() == parent {
			os.Exit(13)
		}
		time.Sleep(1400 * time.Millisecond)
		_, err := os.Stdout.WriteString("late descendant output\n")
		result := "ok"
		if err != nil {
			result = err.Error()
		}
		_ = os.WriteFile(os.Getenv("OFFLINE_COMPLETE_RESULT"), []byte(result), 0600)
		os.Exit(0)
	}
}
