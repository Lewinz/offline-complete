package generator

import (
	"bufio"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"offline-complete/internal/config"
	"offline-complete/internal/rules"
)

//go:embed node_runtime.mjs
var nodeRuntime []byte

// NodeRequest is one generator request sent to the long-lived Node runtime.
// Source is the upstream Kiro/Fig spec module, not a path: the agent can keep
// the rule archive encrypted on disk and still execute custom generators.
type NodeRequest struct {
	Source         string   `json:"source"`
	Command        string   `json:"command"`
	Subcommands    []string `json:"subcommands,omitempty"`
	Kind           string   `json:"kind"`
	Slot           int      `json:"slot,omitempty"`
	Option         string   `json:"option,omitempty"`
	OptionSlot     int      `json:"optionSlot,omitempty"`
	Prefix         string   `json:"prefix,omitempty"`
	CWD            string   `json:"cwd,omitempty"`
	Tokens         []string `json:"tokens,omitempty"`
	UsedOptions    []string `json:"usedOptions,omitempty"`
	FilterStrategy string   `json:"filterStrategy,omitempty"`
}

type nodeResponse struct {
	Type       string            `json:"type,omitempty"`
	ID         uint64            `json:"id,omitempty"`
	RequestID  uint64            `json:"requestId,omitempty"`
	Name       string            `json:"name,omitempty"`
	Source     string            `json:"source,omitempty"`
	Candidates []rules.Candidate `json:"candidates,omitempty"`
	Error      string            `json:"error,omitempty"`
}

type nodeLine struct {
	line []byte
	err  error
}

// NodeRunner executes Kiro/Fig generator modules through a single long-lived
// Node process. It is safe for concurrent use; requests are serialized because
// the wire protocol is one request per line.
type NodeRunner struct {
	mu          sync.Mutex
	cmd         *exec.Cmd
	stdin       io.WriteCloser
	lines       chan nodeLine
	readerDone  chan struct{}
	runtimePath string
	tempDir     string
	nextID      uint64
	timeout     time.Duration
	// Specs resolves cross-spec loadSpec requests from the Node runtime.
	Specs *rules.SpecStore
	// Env contains private settings passed to the Node process. Keys here
	// replace inherited environment variables of the same name.
	Env []string
}

// NewNodeRunner returns a runner that starts Node on first use. An empty or
// unavailable Node executable is reported by Complete, allowing the caller to
// fall back to simple script/template generators.
func NewNodeRunner() *NodeRunner {
	return &NodeRunner{timeout: 5 * time.Second}
}

// Available reports whether a usable Node executable can be found.
func (r *NodeRunner) Available() bool {
	_, err := findNode()
	return err == nil
}

// Close terminates the Node process and removes its temporary runtime file.
func (r *NodeRunner) Close() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.stopLocked()
	if r.tempDir != "" {
		_ = os.RemoveAll(r.tempDir)
		r.tempDir = ""
	}
}

// Complete runs all generators attached to the selected argument and returns
// normalized candidates. The caller should treat every failure as non-fatal.
func (r *NodeRunner) Complete(request NodeRequest) ([]rules.Candidate, error) {
	if r == nil {
		return nil, errors.New("nil node runner")
	}
	if strings.TrimSpace(request.Source) == "" {
		return nil, errors.New("empty spec source")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.ensureLocked(); err != nil {
		return nil, err
	}
	r.nextID++
	requestID := r.nextID
	payload, err := json.Marshal(struct {
		ID uint64 `json:"id"`
		NodeRequest
	}{ID: requestID, NodeRequest: request})
	if err != nil {
		return nil, fmt.Errorf("encode generator request: %w", err)
	}
	payload = append(payload, '\n')
	if _, err := r.stdin.Write(payload); err != nil {
		r.stopLocked()
		return nil, fmt.Errorf("write generator request: %w", err)
	}

	timeout := r.timeout
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	for {
		select {
		case value := <-r.lines:
			if value.err != nil {
				r.stopLocked()
				return nil, value.err
			}
			var response nodeResponse
			if err := json.Unmarshal(value.line, &response); err != nil {
				r.stopLocked()
				return nil, fmt.Errorf("decode generator response: %w", err)
			}
			if response.Type == "spec-request" {
				reply := nodeResponse{Type: "spec-response", RequestID: response.RequestID}
				if r.Specs == nil {
					reply.Error = "spec store is not configured"
				} else if source, err := r.Specs.LoadName(response.Name); err != nil {
					reply.Error = err.Error()
				} else {
					reply.Source = string(source)
				}
				encoded, err := json.Marshal(reply)
				if err != nil {
					r.stopLocked()
					return nil, err
				}
				encoded = append(encoded, '\n')
				if _, err := r.stdin.Write(encoded); err != nil {
					r.stopLocked()
					return nil, fmt.Errorf("write spec response: %w", err)
				}
				continue
			}
			if response.ID != requestID {
				r.stopLocked()
				return nil, errors.New("generator response id mismatch")
			}
			if response.Error != "" {
				return nil, errors.New(response.Error)
			}
			return response.Candidates, nil
		case <-timer.C:
			r.stopLocked()
			return nil, context.DeadlineExceeded
		}
	}
}

// Warm starts the Node process before the first completion request. Process
// startup and first-exec verification are the expensive part of a cold
// session; keeping it off the user's first keystroke removes the visible delay.
func (r *NodeRunner) Warm() error {
	if r == nil {
		return errors.New("nil node runner")
	}
	r.mu.Lock()
	if err := r.ensureLocked(); err != nil {
		r.mu.Unlock()
		return err
	}
	r.mu.Unlock()
	// A started process is not necessarily ready; a trivial request forces the
	// runtime to import and answer before the first real completion arrives.
	_, err := r.Complete(NodeRequest{
		Source:  `export default {name:"warm"}`,
		Command: "warm",
		Kind:    "load",
	})
	if err != nil {
		return err
	}
	// Import the specs most likely to be hit first. This is intentionally only
	// module loading: no generator runs and no user file data is read.
	for _, name := range []string{"cd", "git", "ls", "docker", "kubectl", "j"} {
		if r.Specs == nil {
			break
		}
		source, loadErr := r.Specs.LoadName(name)
		if loadErr != nil {
			continue
		}
		_, _ = r.Complete(NodeRequest{
			Source:  string(source),
			Command: name,
			Kind:    "load",
		})
	}
	return nil
}

func (r *NodeRunner) ensureLocked() error {
	if r.cmd != nil && r.cmd.Process != nil {
		return nil
	}
	node, err := findNode()
	if err != nil {
		return err
	}
	if r.runtimePath == "" {
		dir, err := os.MkdirTemp("", "offline-complete-node-")
		if err != nil {
			return fmt.Errorf("create node runtime directory: %w", err)
		}
		if err := os.Chmod(dir, 0o700); err != nil {
			_ = os.RemoveAll(dir)
			return err
		}
		r.tempDir = dir
		r.runtimePath = filepath.Join(dir, "generator.mjs")
		if err := os.WriteFile(r.runtimePath, nodeRuntime, 0o600); err != nil {
			_ = os.RemoveAll(dir)
			r.tempDir = ""
			r.runtimePath = ""
			return fmt.Errorf("write node runtime: %w", err)
		}
	}
	cmd := exec.Command(node, r.runtimePath)
	cmd.Env = mergeEnv(os.Environ(), r.Env)
	cmd.Stderr = io.Discard
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("node stdin: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return fmt.Errorf("node stdout: %w", err)
	}
	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		return fmt.Errorf("start node generator: %w", err)
	}
	reader := bufio.NewReaderSize(stdout, 64*1024)
	lines := make(chan nodeLine, 8)
	done := make(chan struct{})
	r.cmd = cmd
	r.stdin = stdin
	r.lines = lines
	r.readerDone = done
	go func() {
		for {
			line, err := reader.ReadBytes('\n')
			select {
			case lines <- nodeLine{line: line, err: err}:
			case <-done:
				return
			}
			if err != nil {
				return
			}
		}
	}()
	return nil
}

func (r *NodeRunner) stopLocked() {
	if r.readerDone != nil {
		close(r.readerDone)
		r.readerDone = nil
	}
	if r.stdin != nil {
		_ = r.stdin.Close()
	}
	if r.cmd != nil && r.cmd.Process != nil {
		_ = r.cmd.Process.Kill()
		_ = r.cmd.Wait()
	}
	r.cmd = nil
	r.stdin = nil
	r.lines = nil
}

func findNode() (string, error) {
	// The installed app carries its own Node runtime. Keep the system lookup as
	// a development fallback for bin/ and source test runs.
	if bundled := config.NodePath(); bundled != "" {
		return bundled, nil
	}
	if value := os.Getenv("OFFLINE_COMPLETE_NODE"); value != "" {
		if info, err := os.Stat(value); err == nil && !info.IsDir() {
			return value, nil
		}
		return "", errors.New("OFFLINE_COMPLETE_NODE is not executable")
	}
	candidates := []string{"/opt/homebrew/bin/node", "/usr/local/bin/node", "/usr/bin/node"}
	if value, err := exec.LookPath("node"); err == nil {
		candidates = append([]string{value}, candidates...)
	}
	for _, candidate := range candidates {
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() && info.Mode()&0o111 != 0 {
			return candidate, nil
		}
	}
	return "", errors.New("node executable not found")
}

func mergeEnv(base, overrides []string) []string {
	replaced := make(map[string]bool, len(overrides))
	for _, item := range overrides {
		if index := strings.IndexByte(item, '='); index > 0 {
			replaced[item[:index]] = true
		}
	}
	result := make([]string, 0, len(base)+len(overrides)+1)
	for _, item := range base {
		if index := strings.IndexByte(item, '='); index > 0 && replaced[item[:index]] {
			continue
		}
		result = append(result, item)
	}
	result = append(result, "NODE_NO_WARNINGS=1")
	result = append(result, overrides...)
	return result
}
