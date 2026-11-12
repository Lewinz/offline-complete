//go:build darwin

package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"offline-complete/internal/agent"
	"offline-complete/internal/editstate"
	"offline-complete/internal/ipc"
)

type agentMessage struct {
	Type     string       `json:"type"`
	Version  uint64       `json:"version"`
	Offer    uint64       `json:"offer"`
	Selected int          `json:"selected"`
	Index    int          `json:"index"`
	Items    []agent.Item `json:"items"`
	OK       bool         `json:"ok"`
	Error    string       `json:"error"`
}

type agentOutcome struct {
	code int
	err  error
}

type testAgent struct {
	runtime    *agentRuntime
	stdin      *os.File
	output     *syncBuffer
	connection *net.UnixConn
	reader     *bufio.Reader
	result     chan agentOutcome
	outcome    agentOutcome
	stopped    bool
	logPath    string
}

func readAgentMessage(t *testing.T, conn net.Conn, reader *bufio.Reader, timeout time.Duration) agentMessage {
	t.Helper()
	if timeout <= 0 {
		t.Fatal("timed out waiting for agent message")
	}
	if err := conn.SetReadDeadline(time.Now().Add(timeout)); err != nil {
		t.Fatal(err)
	}
	line, err := reader.ReadString('\n')
	if err != nil {
		t.Fatalf("read agent message: %v", err)
	}
	var message agentMessage
	if err := json.Unmarshal([]byte(line), &message); err != nil {
		t.Fatalf("decode agent message %q: %v", line, err)
	}
	return message
}

func startTestAgent(t *testing.T, rulesJSON string) *testAgent {
	t.Helper()
	if _, err := exec.LookPath("zsh"); err != nil {
		t.Skip("zsh unavailable")
	}
	repoRoot, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	root, err := os.MkdirTemp(repoRoot, ".agent-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })

	t.Setenv("HOME", root)
	t.Setenv("TERM", "xterm")
	t.Setenv("OFFLINE_COMPLETE_NO_USER_RC", "1")
	t.Setenv("OFFLINE_COMPLETE_SOCKET_BASE", filepath.Join(root, "b"))
	t.Setenv("OFFLINE_COMPLETE_DATA_DIR", filepath.Join(root, "data"))
	t.Setenv("OFFLINE_COMPLETE_NO_WINDOW", "1")
	logPath := filepath.Join(root, "agent.log")
	t.Setenv("OFFLINE_COMPLETE_LOG", logPath)

	rulesPath := filepath.Join(root, "rules.json")
	if err := os.WriteFile(rulesPath, []byte(rulesJSON), 0o600); err != nil {
		t.Fatal(err)
	}
	hooks, runtime, err := newAgentHooks(rulesPath, []string{"zsh", "-i"})
	if err != nil {
		t.Fatal(err)
	}
	stdinReader, stdinWriter, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	output := &syncBuffer{}
	result := make(chan agentOutcome, 1)
	wrap := func(writer io.WriteCloser) io.WriteCloser {
		runtime.base = &lockedWriter{w: writer}
		return &filterWriter{upstream: runtime.base, navigator: runtime.navigator}
	}
	go func() {
		code, err := runWithInputWriter([]string{"zsh", "-i"}, stdinReader, output, wrap, hooks)
		result <- agentOutcome{code, err}
	}()

	var connection *net.UnixConn
	deadline := time.Now().Add(5 * time.Second)
	for {
		connection, err = ipc.DialAuthenticated(runtime.listener.Path(), runtime.sessionID, 300*time.Millisecond)
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("dial agent: %v", err)
		}
		time.Sleep(50 * time.Millisecond)
	}
	a := &testAgent{
		runtime:    runtime,
		stdin:      stdinWriter,
		output:     output,
		connection: connection,
		reader:     bufio.NewReader(connection),
		result:     result,
		logPath:    logPath,
	}
	t.Cleanup(func() {
		_ = connection.Close()
		_ = stdinWriter.Close()
		if !a.stopped {
			select {
			case a.outcome = <-result:
			case <-time.After(time.Second):
			}
		}
	})
	return a
}

func (a *testAgent) waitState(t *testing.T, line string, cursor int) editstate.Snapshot {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		snapshot, present := a.runtime.state.Snapshot()
		if present && snapshot.Line == line && snapshot.Cursor == cursor {
			return snapshot
		}
		if time.Now().After(deadline) {
			t.Fatalf("state = %+v present=%v, want %q/%d; output %q", snapshot, present, line, cursor, a.output.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func (a *testAgent) stop(t *testing.T) agentOutcome {
	t.Helper()
	if a.stopped {
		return a.outcome
	}
	a.stopped = true
	if _, err := a.stdin.Write([]byte("\x03")); err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)
	if _, err := a.stdin.Write([]byte("exit 0\r")); err != nil {
		t.Fatal(err)
	}
	_ = a.stdin.Close()
	select {
	case a.outcome = <-a.result:
	case <-time.After(10 * time.Second):
		t.Fatalf("agent did not exit; output %q", a.output.String())
	}
	return a.outcome
}

func TestAgentModeEndToEnd(t *testing.T) {
	const rulesJSON = `{"schemaVersion":1,"commands":[{"name":"git","subcommands":[{"name":"commit","description":"Record changes","options":[{"name":"--message"}]}]}]}`
	a := startTestAgent(t, rulesJSON)

	if _, err := fmt.Fprintln(a.connection, `{"type":"focus","focused":true}`); err != nil {
		t.Fatal(err)
	}
	if _, err := a.stdin.Write([]byte("git co")); err != nil {
		t.Fatal(err)
	}
	snapshot := a.waitState(t, "git co", 6)

	var offer agentMessage
	deadline := time.Now().Add(10 * time.Second)
	for {
		message := readAgentMessage(t, a.connection, a.reader, time.Until(deadline))
		if message.Type == "offer" && message.Version == snapshot.Version &&
			len(message.Items) == 1 && message.Items[0].Name == "commit" {
			offer = message
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("no commit offer for version %d, last %+v", snapshot.Version, message)
		}
	}
	if _, err := fmt.Fprintf(a.connection, `{"type":"select","version":%d,"offer":%d,"index":0}`+"\n", offer.Version, offer.Offer); err != nil {
		t.Fatal(err)
	}
	a.waitState(t, "git commit", 10)

	_ = a.connection.Close()
	var reconnected *net.UnixConn
	deadline = time.Now().Add(5 * time.Second)
	for {
		var err error
		reconnected, err = ipc.DialAuthenticated(a.runtime.listener.Path(), a.runtime.sessionID, 300*time.Millisecond)
		if err == nil {
			a.connection = reconnected
			a.reader = bufio.NewReader(reconnected)
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("reconnect: %v", err)
		}
		time.Sleep(50 * time.Millisecond)
	}
	if _, err := fmt.Fprintln(a.connection, `{"type":"focus","focused":true}`); err != nil {
		t.Fatal(err)
	}
	for {
		message := readAgentMessage(t, a.connection, a.reader, 5*time.Second)
		if message.Type == "offer" || message.Type == "clear" {
			break
		}
	}

	outcome := a.stop(t)
	if outcome.err != nil {
		t.Fatalf("agent exit: %v; output %q", outcome.err, a.output.String())
	}
	if outcome.code != 0 {
		t.Fatalf("agent exit code = %d; output %q", outcome.code, a.output.String())
	}
}

func TestAgentKeyboardSelection(t *testing.T) {
	const rulesJSON = `{"schemaVersion":1,"commands":[{"name":"git","subcommands":[{"name":"commit","description":"Record changes"},{"name":"checkout","description":"Switch branches"}]}]}`
	a := startTestAgent(t, rulesJSON)

	if _, err := a.stdin.Write([]byte("git c")); err != nil {
		t.Fatal(err)
	}
	snapshot := a.waitState(t, "git c", 5)

	var offer agentMessage
	deadline := time.Now().Add(10 * time.Second)
	for {
		message := readAgentMessage(t, a.connection, a.reader, time.Until(deadline))
		if message.Type == "offer" && message.Version == snapshot.Version && len(message.Items) == 2 {
			offer = message
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("no two-candidate offer, last %+v", message)
		}
	}
	if offer.Items[0].Name != "commit" || offer.Items[1].Name != "checkout" {
		t.Fatalf("items = %+v", offer.Items)
	}

	if _, err := a.stdin.Write([]byte("\x1b[B")); err != nil {
		t.Fatal(err)
	}
	selection := readAgentMessage(t, a.connection, a.reader, 5*time.Second)
	if selection.Type != "selection" || selection.Index != 1 {
		t.Fatalf("selection = %+v", selection)
	}

	if _, err := a.stdin.Write([]byte("\r")); err != nil {
		t.Fatal(err)
	}
	var result agentMessage
	for {
		message := readAgentMessage(t, a.connection, a.reader, 5*time.Second)
		if message.Type == "result" {
			result = message
			break
		}
	}
	if !result.OK {
		t.Fatalf("result = %+v", result)
	}
	a.waitState(t, "git checkout", 12)

	time.Sleep(200 * time.Millisecond)
	if snapshot, present := a.runtime.state.Snapshot(); !present || snapshot.Line != "git checkout" {
		t.Fatalf("Enter was forwarded or state cleared: %+v present=%v", snapshot, present)
	}
	outcome := a.stop(t)
	if outcome.err != nil || outcome.code != 0 {
		t.Fatalf("agent exit: %+v; output %q", outcome, a.output.String())
	}
}

func TestWindowCommandUsesDoubleDashFlags(t *testing.T) {
	cmd := windowCommand("/bin/echo", "/tmp/socket", "session-id")
	want := []string{"/bin/echo", "--socket", "/tmp/socket", "--session", "session-id"}
	if len(cmd.Args) != len(want) {
		t.Fatalf("args = %q", cmd.Args)
	}
	for index := range want {
		if cmd.Args[index] != want[index] {
			t.Fatalf("args = %q, want %q", cmd.Args, want)
		}
	}
}

func TestAgentWritesDiagnosticLog(t *testing.T) {
	const rulesJSON = `{"schemaVersion":1,"commands":[{"name":"git","subcommands":[{"name":"commit","description":"Record changes"}]}]}`
	a := startTestAgent(t, rulesJSON)
	if _, err := fmt.Fprintln(a.connection, `{"type":"focus","focused":true}`); err != nil {
		t.Fatal(err)
	}
	if _, err := a.stdin.Write([]byte("git co")); err != nil {
		t.Fatal(err)
	}
	snapshot := a.waitState(t, "git co", 6)
	var offer agentMessage
	deadline := time.Now().Add(10 * time.Second)
	for {
		message := readAgentMessage(t, a.connection, a.reader, time.Until(deadline))
		if message.Type == "offer" && message.Version == snapshot.Version && len(message.Items) == 1 {
			offer = message
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("no offer, last %+v", message)
		}
	}
	if _, err := fmt.Fprintf(a.connection, `{"type":"select","version":%d,"offer":%d,"index":0}`+"\n", offer.Version, offer.Offer); err != nil {
		t.Fatal(err)
	}
	a.waitState(t, "git commit", 10)
	outcome := a.stop(t)
	if outcome.err != nil || outcome.code != 0 {
		t.Fatalf("agent exit: %+v; output %q", outcome, a.output.String())
	}
	data, err := os.ReadFile(a.logPath)
	if err != nil {
		t.Fatal(err)
	}
	logText := string(data)
	for _, want := range []string{"event=agent_start", "event=learning_enabled", "event=offer", "event=select_applied", "event=agent_stop"} {
		if !strings.Contains(logText, want) {
			t.Fatalf("log lacks %q:\n%s", want, logText)
		}
	}
	if strings.Contains(logText, "git co") {
		t.Fatalf("typed text was logged without opt-in:\n%s", logText)
	}
}

type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (b *syncBuffer) Write(data []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.Write(data)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.String()
}

func TestPresenceAlive(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.pid")
	if presenceAlive(path) {
		t.Fatal("missing presence file is alive")
	}
	if err := os.WriteFile(path, []byte(fmt.Sprintf("%d\n", os.Getpid())), 0o600); err != nil {
		t.Fatal(err)
	}
	if !presenceAlive(path) {
		t.Fatal("current process is not alive")
	}
	if err := os.WriteFile(path, []byte("not-a-pid\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if presenceAlive(path) {
		t.Fatal("invalid pid is alive")
	}
}
