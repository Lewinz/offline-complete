package diag

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"offline-complete/internal/settings"
)

func TestOpenAtWritesOneLineAndEscapesNewlines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "logs", "agent.log")
	logger, err := OpenAt(path)
	if err != nil {
		t.Fatal(err)
	}
	logger.Printf("event=test value=%q", "a\nb")
	if err := logger.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if strings.Count(text, "\n") != 1 {
		t.Fatalf("want one line, got %q", text)
	}
	if !strings.Contains(text, `value="a\nb"`) {
		t.Fatalf("newline was not escaped: %q", text)
	}
	if !strings.Contains(text, "event=test") {
		t.Fatalf("missing event: %q", text)
	}
}

func TestOpenAtRotatesOversizedFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent.log")
	if err := os.WriteFile(path, make([]byte, MaxBytes+1), 0o600); err != nil {
		t.Fatal(err)
	}
	logger, err := OpenAt(path)
	if err != nil {
		t.Fatal(err)
	}
	logger.Printf("event=fresh")
	if err := logger.Close(); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(path + ".1"); err != nil || info.Size() != MaxBytes+1 {
		t.Fatalf("rotated file: info=%v err=%v", info, err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "event=fresh") {
		t.Fatalf("new log lacks entry: %q", data)
	}
	if len(data) > 1024 {
		t.Fatalf("new log was not truncated: %d bytes", len(data))
	}
}

func TestTextFlag(t *testing.T) {
	t.Setenv("OFFLINE_COMPLETE_LOG_TEXT", "1")
	logger, err := OpenAt(filepath.Join(t.TempDir(), "agent.log"))
	if err != nil {
		t.Fatal(err)
	}
	defer logger.Close()
	if !logger.Text() {
		t.Fatal("text logging should be enabled")
	}
}

func clearLogEnv(t *testing.T) {
	t.Helper()
	t.Setenv("OFFLINE_COMPLETE_LOG", "")
	t.Setenv("OFFLINE_COMPLETE_LOG_DIR", "")
	t.Setenv("OFFLINE_COMPLETE_LOG_ENABLED", "")
	t.Setenv("OFFLINE_COMPLETE_LOG_RETENTION_DAYS", "")
}

func TestResolveDefaultsToOCLogs(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	clearLogEnv(t)
	resolved, err := Resolve()
	if err != nil {
		t.Fatal(err)
	}
	wantDir := filepath.Join(home, ".oc", "logs")
	if resolved.Directory != wantDir || !resolved.Enabled || resolved.RetentionDays != 3 {
		t.Fatalf("resolved = %+v", resolved)
	}
	if !strings.HasPrefix(resolved.Path, wantDir+string(os.PathSeparator)+"agent-") {
		t.Fatalf("path = %q", resolved.Path)
	}
}

func TestResolveDisabledByEnvironment(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	clearLogEnv(t)
	t.Setenv("OFFLINE_COMPLETE_LOG_ENABLED", "0")
	resolved, err := Resolve()
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Enabled {
		t.Fatalf("logging should be disabled: %+v", resolved)
	}
}

func TestOpenDisabledCreatesNoFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	clearLogEnv(t)
	t.Setenv("OFFLINE_COMPLETE_LOG_ENABLED", "0")
	logger, err := Open()
	if err != nil {
		t.Fatal(err)
	}
	logger.Printf("event=hidden")
	if logger.Enabled() {
		t.Fatal("disabled logger reports enabled")
	}
	if err := logger.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(home, ".oc", "logs")); !os.IsNotExist(err) {
		t.Fatalf("disabled logging created a directory: %v", err)
	}
}

func TestOpenUsesDailyFileAndPrivatePermissions(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	clearLogEnv(t)
	logger, err := Open()
	if err != nil {
		t.Fatal(err)
	}
	logger.Printf("event=daily")
	if err := logger.Close(); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(home, ".oc", "logs")
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o700 {
		t.Fatalf("dir mode = %o", info.Mode().Perm())
	}
	info, err = os.Stat(logger.Path())
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("log mode = %o", info.Mode().Perm())
	}
	data, err := os.ReadFile(logger.Path())
	if err != nil || !strings.Contains(string(data), "event=daily") {
		t.Fatalf("log = %q err=%v", data, err)
	}
}

func TestPruneKeepsThreeCalendarDays(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	keep := []string{"agent-" + now.Format(dateFmt) + ".log", "agent-" + now.AddDate(0, 0, -1).Format(dateFmt) + ".log", "agent-" + now.AddDate(0, 0, -2).Format(dateFmt) + ".log"}
	old := "agent-" + now.AddDate(0, 0, -3).Format(dateFmt) + ".log"
	for _, name := range append(append([]string{}, keep...), old, "other.txt") {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(name), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	pruneDir(dir, 3, now)
	for _, name := range keep {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Fatalf("kept file %s: %v", name, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, old)); !os.IsNotExist(err) {
		t.Fatalf("old file was not removed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "other.txt")); err != nil {
		t.Fatalf("unrelated file removed: %v", err)
	}
}

func TestLoggerReloadsSettingsForRunningAgent(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	clearLogEnv(t)
	logger, err := Open()
	if err != nil {
		t.Fatal(err)
	}
	defer logger.Close()
	logger.Printf("event=first")
	cfg, err := settings.Load()
	if err != nil {
		t.Fatal(err)
	}
	cfg.Logging.Enabled = false
	if err := settings.Save(cfg); err != nil {
		t.Fatal(err)
	}
	logger.Printf("event=while_disabled")
	cfg.Logging.Enabled = true
	if err := settings.Save(cfg); err != nil {
		t.Fatal(err)
	}
	logger.Printf("event=after_enable")
	path := logger.Path()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if !strings.Contains(text, "event=first") || !strings.Contains(text, "event=after_enable") {
		t.Fatalf("log = %q", text)
	}
	if strings.Contains(text, "event=while_disabled") {
		t.Fatalf("disabled event was logged: %q", text)
	}
}

func TestLoggerMovesToConfiguredDirectory(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	clearLogEnv(t)
	logger, err := Open()
	if err != nil {
		t.Fatal(err)
	}
	defer logger.Close()
	logger.Printf("event=before_move")
	cfg, err := settings.Load()
	if err != nil {
		t.Fatal(err)
	}
	moved := filepath.Join(home, "moved-logs")
	cfg.Logging.Directory = moved
	if err := settings.Save(cfg); err != nil {
		t.Fatal(err)
	}
	logger.Printf("event=after_move")
	if logger.Path() != filepath.Join(moved, "agent-"+time.Now().Format(dateFmt)+".log") {
		t.Fatalf("path = %q", logger.Path())
	}
	data, err := os.ReadFile(logger.Path())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "event=after_move") {
		t.Fatalf("new log = %q", data)
	}
}
