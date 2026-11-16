package introspect

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeScript(t *testing.T, dir, name, body string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}

func waitReady(t *testing.T, ready <-chan struct{}) {
	t.Helper()
	select {
	case <-ready:
	case <-time.After(3 * time.Second):
		t.Fatal("help discovery did not finish")
	}
}

func TestCacheDiscoversOnceAndReusesDisk(t *testing.T) {
	dir := t.TempDir()
	counter := filepath.Join(dir, "runs")
	script := writeScript(t, dir, "fakecmd", `#!/bin/sh
echo run >> "$COUNT_FILE"
cat <<'EOF'
Usage: fakecmd [command]

Commands:
  alpha    Alpha command
  beta     Beta command

Options:
  --file <FILE>   Read from a file
EOF
`)
	ready := make(chan struct{}, 1)
	cache := &Cache{
		Dir:     filepath.Join(dir, "cache"),
		Timeout: time.Second,
		TTL:     time.Minute,
		Enabled: true,
		OnReady: func() { ready <- struct{}{} },
	}
	t.Setenv("COUNT_FILE", counter)
	if spec, ok := cache.Ensure(script, dir); ok || spec != nil {
		t.Fatal("first lookup should be asynchronous")
	}
	waitReady(t, ready)
	spec, ok := cache.Lookup(script, dir)
	if !ok || spec == nil {
		t.Fatal("spec was not cached")
	}
	if got := childNames(spec.Root); len(got) != 2 || got[0] != "alpha" || got[1] != "beta" {
		t.Fatalf("commands = %v", got)
	}
	if _, ok := optionNamesFor(spec.Root)["--file"]; !ok {
		t.Fatalf("options = %#v", spec.Root.Options)
	}
	data, err := os.ReadFile(counter)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(data), "run") != 1 {
		t.Fatalf("help ran %d times, want 1", strings.Count(string(data), "run"))
	}
	if spec, ok := cache.Ensure(script, dir); !ok || spec == nil {
		t.Fatal("cached Ensure should return a spec")
	}

	second := &Cache{
		Dir:     cache.Dir,
		Timeout: time.Second,
		TTL:     time.Minute,
		Enabled: true,
	}
	if spec, ok := second.Lookup(script, dir); !ok || spec == nil {
		t.Fatal("disk cache was not reused")
	}
	if data, _ := os.ReadFile(counter); strings.Count(string(data), "run") != 1 {
		t.Fatalf("disk cache triggered another help run: %q", data)
	}
}

func TestCacheTimeoutDoesNotRetryImmediately(t *testing.T) {
	dir := t.TempDir()
	counter := filepath.Join(dir, "runs")
	script := writeScript(t, dir, "slowcmd", `#!/bin/sh
echo run >> "$COUNT_FILE"
sleep 5
`)
	ready := make(chan struct{}, 1)
	cache := &Cache{
		Timeout: time.Second,
		TTL:     time.Minute,
		Enabled: true,
		OnReady: func() { ready <- struct{}{} },
	}
	t.Setenv("COUNT_FILE", counter)
	if _, ok := cache.Ensure(script, dir); ok {
		t.Fatal("slow command should not return a spec immediately")
	}
	waitReady(t, ready)
	if spec, ok := cache.Lookup(script, dir); ok || spec != nil {
		t.Fatal("timed out help must not be cached as a spec")
	}
	if _, ok := cache.Ensure(script, dir); ok {
		t.Fatal("retry must respect backoff")
	}
	time.Sleep(100 * time.Millisecond)
	data, _ := os.ReadFile(counter)
	if strings.Count(string(data), "run") != 1 {
		t.Fatalf("help ran %d times, want 1", strings.Count(string(data), "run"))
	}
}

func TestCacheDisabledDoesNotRunHelp(t *testing.T) {
	dir := t.TempDir()
	counter := filepath.Join(dir, "runs")
	script := writeScript(t, dir, "fakecmd", `#!/bin/sh
echo run >> "$COUNT_FILE"
echo "Commands:"
echo "  alpha  Alpha"
`)
	cache := &Cache{Enabled: false, Timeout: time.Second}
	t.Setenv("COUNT_FILE", counter)
	if _, ok := cache.Ensure(script, dir); ok {
		t.Fatal("disabled cache returned a spec")
	}
	time.Sleep(100 * time.Millisecond)
	if _, err := os.Stat(counter); !os.IsNotExist(err) {
		t.Fatal("disabled cache executed the command")
	}
}
