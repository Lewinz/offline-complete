package introspect

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	defaultTimeout      = 750 * time.Millisecond
	defaultTTL          = 24 * time.Hour
	defaultRetryBackoff = 30 * time.Second
	maxHelpBytes        = 64 * 1024
)

// Cache discovers and stores help-derived specs. Discovery is asynchronous:
// Ensure starts at most one help run per command and OnReady is called when the
// result is available, so the shell's input path is never blocked by a command.
type Cache struct {
	// Dir stores per-executable JSON results. Empty keeps the cache in memory.
	Dir     string
	Timeout time.Duration
	// RetryTimeout is the second, longer attempt used automatically after a
	// timeout. Zero derives it from Timeout.
	RetryTimeout time.Duration
	TTL          time.Duration
	Enabled      bool
	OnReady      func()
	// Logf is optional and must not receive help output or command arguments.
	Logf func(format string, args ...any)
	// LookPath is overridable for tests. It defaults to exec.LookPath.
	LookPath func(string) (string, error)

	mu      sync.Mutex
	specs   map[string]*Spec
	flights map[string]bool
	retryAt map[string]time.Time
}

// NewCache returns an enabled in-memory cache with the default limits.
// OFFLINE_COMPLETE_HELP_TIMEOUT_MS overrides the per-command help timeout.
func NewCache() *Cache {
	timeout := defaultTimeout
	if value := os.Getenv("OFFLINE_COMPLETE_HELP_TIMEOUT_MS"); value != "" {
		if milliseconds, err := strconv.Atoi(value); err == nil && milliseconds >= 100 && milliseconds <= 5000 {
			timeout = time.Duration(milliseconds) * time.Millisecond
		}
	}
	return &Cache{
		Timeout: timeout,
		TTL:     defaultTTL,
		Enabled: true,
	}
}

// Clear forgets one command's cached spec and retry state. It is used by the
// candidate window's refresh action; the next lookup discovers help again.
func (c *Cache) Clear(command string) {
	if c == nil {
		return
	}
	key := cacheKey(command)
	c.mu.Lock()
	delete(c.specs, key)
	delete(c.retryAt, key)
	c.mu.Unlock()
	if path := c.cachePath(key); path != "" {
		_ = os.Remove(path)
	}
}

// Lookup returns a cached spec without running the command.
func (c *Cache) Lookup(command, cwd string) (*Spec, bool) {
	if c == nil || !c.Enabled || strings.TrimSpace(command) == "" {
		return nil, false
	}
	_, fingerprint, err := c.executable(command)
	if err != nil {
		return nil, false
	}
	key := cacheKey(command)
	c.mu.Lock()
	if spec := c.specs[key]; spec != nil && spec.Fingerprint == fingerprint && c.fresh(spec) {
		c.mu.Unlock()
		return spec, true
	}
	c.mu.Unlock()
	if spec := c.loadDisk(key, fingerprint); spec != nil {
		c.mu.Lock()
		if c.specs == nil {
			c.specs = make(map[string]*Spec)
		}
		c.specs[key] = spec
		c.mu.Unlock()
		return spec, true
	}
	return nil, false
}

// Ensure returns a cached spec, or starts a bounded help discovery in the
// background. The first call for a command normally returns false; OnReady is
// called after the result is cached so the caller can recompute candidates.
func (c *Cache) Ensure(command, cwd string) (*Spec, bool) {
	if spec, ok := c.Lookup(command, cwd); ok {
		return spec, true
	}
	if c == nil || !c.Enabled {
		return nil, false
	}
	key := cacheKey(command)
	c.mu.Lock()
	if c.specs == nil {
		c.specs = make(map[string]*Spec)
	}
	if c.flights == nil {
		c.flights = make(map[string]bool)
	}
	if c.retryAt == nil {
		c.retryAt = make(map[string]time.Time)
	}
	if c.flights[key] {
		c.mu.Unlock()
		return nil, false
	}
	if retryAt, ok := c.retryAt[key]; ok && time.Now().Before(retryAt) {
		c.mu.Unlock()
		return nil, false
	}
	c.flights[key] = true
	c.mu.Unlock()
	go c.discover(key, command, cwd)
	return nil, false
}

func (c *Cache) discover(key, command, cwd string) {
	started := time.Now()
	defer func() {
		c.mu.Lock()
		delete(c.flights, key)
		c.mu.Unlock()
	}()
	c.logf("event=help_start command=%q", command)
	path, fingerprint, err := c.executable(command)
	if err != nil {
		c.fail(key, command, err, started)
		return
	}
	output, runErr := c.runHelp(path, cwd, c.helpTimeout(), "--help")
	spec := ParseHelp(command, []byte(output))
	if specCount(spec) == 0 && errors.Is(runErr, context.DeadlineExceeded) {
		retryTimeout := c.retryTimeout()
		c.logf("event=help_retry command=%q timeout_ms=%d", command, retryTimeout.Milliseconds())
		output, runErr = c.runHelp(path, cwd, retryTimeout, "--help")
		spec = ParseHelp(command, []byte(output))
	}
	if specCount(spec) == 0 && !errors.Is(runErr, context.DeadlineExceeded) {
		if helpOutput, helpErr := c.runHelp(path, cwd, c.helpTimeout(), "help"); len(helpOutput) > 0 {
			alternative := ParseHelp(command, []byte(helpOutput))
			if specCount(alternative) > specCount(spec) {
				spec, runErr = alternative, helpErr
			}
		}
	}
	if specCount(spec) == 0 {
		if runErr == nil {
			runErr = errors.New("help output contained no subcommands or options")
		}
		c.fail(key, command, runErr, started)
		return
	}
	spec.Path = path
	spec.Fingerprint = fingerprint
	spec.CreatedAt = time.Now()
	c.mu.Lock()
	if c.specs == nil {
		c.specs = make(map[string]*Spec)
	}
	c.specs[key] = spec
	delete(c.retryAt, key)
	c.mu.Unlock()
	c.saveDisk(key, spec)
	c.logf("event=help_ready command=%q subcommands=%d options=%d ms=%.2f",
		command, len(spec.Root.Subcommands), countOptions(spec.Root), elapsedMS(started))
	if c.OnReady != nil {
		c.OnReady()
	}
}

func (c *Cache) fail(key, command string, err error, started time.Time) {
	delay := defaultRetryBackoff
	if errors.Is(err, context.DeadlineExceeded) {
		delay = 5 * time.Second
	}
	c.mu.Lock()
	if c.retryAt == nil {
		c.retryAt = make(map[string]time.Time)
	}
	c.retryAt[key] = time.Now().Add(delay)
	c.mu.Unlock()
	c.logf("event=help_error command=%q err=%q retry_ms=%d ms=%.2f", command, err, delay.Milliseconds(), elapsedMS(started))
	if c.OnReady != nil {
		c.OnReady()
	}
}

func (c *Cache) executable(command string) (string, string, error) {
	look := c.LookPath
	if look == nil {
		look = exec.LookPath
	}
	path, err := look(command)
	if err != nil {
		return "", "", err
	}
	path, err = filepath.Abs(path)
	if err != nil {
		return "", "", err
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", "", err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
		return "", "", errors.New("command is not an executable file")
	}
	fingerprint := fmt.Sprintf("%s|%d|%d", path, info.Size(), info.ModTime().UnixNano())
	if stat, ok := info.Sys().(*syscall.Stat_t); ok {
		fingerprint += fmt.Sprintf("|%d|%d", stat.Dev, stat.Ino)
	}
	return path, fingerprint, nil
}

func (c *Cache) helpTimeout() time.Duration {
	if c == nil || c.Timeout <= 0 {
		return defaultTimeout
	}
	return c.Timeout
}

func (c *Cache) retryTimeout() time.Duration {
	if c != nil && c.RetryTimeout > 0 {
		return c.RetryTimeout
	}
	timeout := c.helpTimeout() * 4
	if timeout < 3*time.Second {
		timeout = 3 * time.Second
	}
	if timeout > 10*time.Second {
		timeout = 10 * time.Second
	}
	return timeout
}

func (c *Cache) runHelp(path, cwd string, timeout time.Duration, args ...string) (string, error) {
	if timeout <= 0 {
		timeout = c.helpTimeout()
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	command := exec.CommandContext(ctx, path, args...)
	command.Dir = cwd
	command.Env = helpEnvironment()
	command.Stdin = nil
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	command.Cancel = func() error {
		if command.Process == nil {
			return nil
		}
		return syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
	}
	command.WaitDelay = 100 * time.Millisecond
	output := &limitedBuffer{limit: maxHelpBytes}
	command.Stdout = output
	command.Stderr = output
	err := command.Run()
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		err = context.DeadlineExceeded
	}
	return output.String(), err
}

// helpEnvironment forces a stable, non-interactive help rendering. Overrides
// replace inherited variables instead of being appended as duplicates.
func helpEnvironment() []string {
	overrides := []string{
		"LC_ALL=C",
		"LANG=C",
		"TERM=dumb",
		"NO_COLOR=1",
		"CLICOLOR=0",
		"PAGER=cat",
		"GIT_PAGER=cat",
		"COLUMNS=120",
	}
	keys := make(map[string]bool, len(overrides))
	for _, entry := range overrides {
		if index := strings.IndexByte(entry, '='); index >= 0 {
			keys[entry[:index]] = true
		}
	}
	env := make([]string, 0, len(os.Environ())+len(overrides))
	for _, entry := range os.Environ() {
		key := entry
		if index := strings.IndexByte(entry, '='); index >= 0 {
			key = entry[:index]
		}
		if keys[key] {
			continue
		}
		env = append(env, entry)
	}
	return append(env, overrides...)
}

func (c *Cache) fresh(spec *Spec) bool {
	if spec == nil || spec.CreatedAt.IsZero() {
		return spec != nil
	}
	ttl := c.TTL
	if ttl <= 0 {
		ttl = defaultTTL
	}
	return time.Since(spec.CreatedAt) <= ttl
}

func (c *Cache) cachePath(key string) string {
	if c.Dir == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(key))
	return filepath.Join(c.Dir, hex.EncodeToString(sum[:])+".json")
}

func (c *Cache) loadDisk(key, fingerprint string) *Spec {
	path := c.cachePath(key)
	if path == "" {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil || len(data) > 1<<20 {
		return nil
	}
	var spec Spec
	if err := json.Unmarshal(data, &spec); err != nil {
		return nil
	}
	if spec.Fingerprint != fingerprint || !c.fresh(&spec) || spec.Root == nil {
		return nil
	}
	return &spec
}

func (c *Cache) saveDisk(key string, spec *Spec) {
	path := c.cachePath(key)
	if path == "" {
		return
	}
	data, err := json.Marshal(spec)
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return
	}
	_ = os.Chmod(filepath.Dir(path), 0o700)
	temp, err := os.CreateTemp(filepath.Dir(path), ".help-*")
	if err != nil {
		return
	}
	name := temp.Name()
	defer os.Remove(name)
	if err := temp.Chmod(0o600); err != nil {
		_ = temp.Close()
		return
	}
	if _, err := temp.Write(data); err != nil {
		_ = temp.Close()
		return
	}
	if err := temp.Close(); err != nil {
		return
	}
	_ = os.Rename(name, path)
}

func (c *Cache) logf(format string, args ...any) {
	if c != nil && c.Logf != nil {
		c.Logf(format, args...)
	}
}

func cacheKey(command string) string {
	return strings.TrimSpace(command)
}

func specCount(spec *Spec) int {
	if spec == nil || spec.Root == nil {
		return 0
	}
	return len(spec.Root.Subcommands) + countOptions(spec.Root) + countValues(spec.Root)
}

func countOptions(node *Node) int {
	if node == nil {
		return 0
	}
	count := len(node.Options)
	for _, child := range node.Subcommands {
		count += countOptions(child)
	}
	return count
}

func countValues(node *Node) int {
	if node == nil {
		return 0
	}
	count := len(node.Values)
	for _, child := range node.Subcommands {
		count += countValues(child)
	}
	return count
}

func elapsedMS(start time.Time) float64 {
	return float64(time.Since(start).Microseconds()) / 1000
}

type limitedBuffer struct {
	strings.Builder
	limit int
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	remaining := b.limit - b.Len()
	if remaining <= 0 {
		return len(p), nil
	}
	if len(p) > remaining {
		_, _ = b.Builder.Write(p[:remaining])
		return len(p), nil
	}
	return b.Builder.Write(p)
}
