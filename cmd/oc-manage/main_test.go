package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"offline-complete/internal/settings"
)

func TestLearningStatusAndClear(t *testing.T) {
	dir := t.TempDir()
	if err := chmodPrivate(dir); err != nil {
		t.Fatal(err)
	}
	t.Setenv("OFFLINE_COMPLETE_DATA_DIR", dir)

	var stdout, stderr bytes.Buffer
	if code := run([]string{"learning", "off"}, &stdout, &stderr); code != 0 {
		t.Fatalf("learning off: code=%d stderr=%q", code, stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"status"}, &stdout, &stderr); code != 0 {
		t.Fatalf("status: code=%d stderr=%q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "learning: false") {
		t.Fatalf("status = %q", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"clear"}, &stdout, &stderr); code != 2 {
		t.Fatalf("clear without --yes = %d", code)
	}
	if code := run([]string{"clear", "--yes"}, &stdout, &stderr); code != 0 {
		t.Fatalf("clear: code=%d stderr=%q", code, stderr.String())
	}
}

func chmodPrivate(path string) error {
	return os.Chmod(path, 0o700)
}

func TestLoggingSettingsCommands(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("OFFLINE_COMPLETE_CONFIG", "")
	t.Setenv("OFFLINE_COMPLETE_LOG", "")
	t.Setenv("OFFLINE_COMPLETE_LOG_DIR", "")
	t.Setenv("OFFLINE_COMPLETE_LOG_ENABLED", "")
	t.Setenv("OFFLINE_COMPLETE_LOG_RETENTION_DAYS", "")

	var stdout, stderr bytes.Buffer
	if code := run([]string{"logging", "off"}, &stdout, &stderr); code != 0 {
		t.Fatalf("logging off: %d %q", code, stderr.String())
	}
	cfg, err := settings.Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Logging.Enabled {
		t.Fatal("logging was not disabled")
	}

	logDir := filepath.Join(home, "custom-logs")
	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"logging", "set", "--enabled", "on", "--directory", logDir, "--retention-days", "7"}, &stdout, &stderr); code != 0 {
		t.Fatalf("logging set: %d %q", code, stderr.String())
	}
	cfg, err = settings.Load()
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Logging.Enabled || cfg.Logging.Directory != logDir || cfg.Logging.RetentionDays != 7 {
		t.Fatalf("logging = %+v", cfg.Logging)
	}

	stdout.Reset()
	if code := run([]string{"logging", "status", "--json"}, &stdout, &stderr); code != 0 {
		t.Fatalf("logging status: %d %q", code, stderr.String())
	}
	var status loggingStatus
	if err := json.Unmarshal(stdout.Bytes(), &status); err != nil {
		t.Fatal(err)
	}
	if !status.Enabled || status.Directory != logDir || status.RetentionDays != 7 || status.CurrentPath == "" {
		t.Fatalf("status = %+v", status)
	}

	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"logging", "set", "--retention-days", "500"}, &stdout, &stderr); code != 1 {
		t.Fatalf("invalid retention code = %d stderr=%q", code, stderr.String())
	}

	configPath := filepath.Join(home, ".oc", "config.json")
	info, err := os.Stat(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("config mode = %o", info.Mode().Perm())
	}
}

func TestShellIntegrationInstallAndUninstall(t *testing.T) {
	home := t.TempDir()
	zshrc := filepath.Join(home, ".zshrc")
	original := "export PATH=\"$HOME/.local/bin:$PATH\"\n"
	if err := os.WriteFile(zshrc, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if code := run([]string{"shell", "install", "--home", home}, &stdout, &stderr); code != 0 {
		t.Fatalf("install: %d %q", code, stderr.String())
	}
	data, err := os.ReadFile(zshrc)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "# >>> offline-complete integration >>>") {
		t.Fatalf("missing block: %q", data)
	}
	for _, want := range []string{"# offline-complete integration v2", "app.pid", "OFFLINE_COMPLETE_AUTO=1"} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("missing %q in block: %q", want, data)
		}
	}
	if !strings.Contains(string(data), original) {
		t.Fatalf("original content lost: %q", data)
	}
	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"shell", "install", "--home", home}, &stdout, &stderr); code != 0 {
		t.Fatalf("reinstall: %d %q", code, stderr.String())
	}
	data, err = os.ReadFile(zshrc)
	if err != nil {
		t.Fatal(err)
	}
	if count := strings.Count(string(data), "# >>> offline-complete integration >>>"); count != 1 {
		t.Fatalf("block count = %d", count)
	}
	if code := run([]string{"shell", "uninstall", "--home", home}, &stdout, &stderr); code != 0 {
		t.Fatalf("uninstall: %d %q", code, stderr.String())
	}
	data, err = os.ReadFile(zshrc)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "offline-complete integration") || string(data) != original {
		t.Fatalf("uninstall left %q, want %q", data, original)
	}
}

func TestShellIntegrationFollowsZshrcSymlink(t *testing.T) {
	home := t.TempDir()
	target := filepath.Join(home, "dotfiles-zshrc")
	if err := os.WriteFile(target, []byte("export TEST=1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	zshrc := filepath.Join(home, ".zshrc")
	if err := os.Symlink(target, zshrc); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if code := run([]string{"shell", "install", "--home", home}, &stdout, &stderr); code != 0 {
		t.Fatalf("install: %d %q", code, stderr.String())
	}
	info, err := os.Lstat(zshrc)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatal(".zshrc symlink was replaced")
	}
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "offline-complete integration") {
		t.Fatalf("target missing block: %q", data)
	}
	if code := run([]string{"shell", "uninstall", "--home", home}, &stdout, &stderr); code != 0 {
		t.Fatalf("uninstall: %d %q", code, stderr.String())
	}
	data, err = os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "export TEST=1\n" {
		t.Fatalf("target = %q", data)
	}
}

func TestAutocompleteSettingsCommands(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("OFFLINE_COMPLETE_CONFIG", "")
	var stdout, stderr bytes.Buffer
	if code := run([]string{"autocomplete", "set", "--tab", "accept", "--enter", "execute", "--exact-match-execute", "off"}, &stdout, &stderr); code != 0 {
		t.Fatalf("autocomplete set: %d %q", code, stderr.String())
	}
	cfg, err := settings.Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Autocomplete.TabAction != "accept" || cfg.Autocomplete.EnterAction != "execute" || cfg.Autocomplete.ExactMatchExecute {
		t.Fatalf("autocomplete = %+v", cfg.Autocomplete)
	}
	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"autocomplete", "status"}, &stdout, &stderr); code != 0 {
		t.Fatalf("autocomplete status: %d %q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "tab action: accept") || !strings.Contains(stdout.String(), "enter action: execute") {
		t.Fatalf("status = %q", stdout.String())
	}
}
