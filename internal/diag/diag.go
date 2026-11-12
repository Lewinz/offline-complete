// Package diag writes local, rotating diagnostic logs for the agent. It uses
// daily files under ~/.oc/logs by default, keeps three days, and contains
// completion metadata rather than terminal input unless explicitly enabled
// with OFFLINE_COMPLETE_LOG_TEXT=1 for local debugging.
package diag

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"offline-complete/internal/settings"
)

const (
	// MaxBytes bounds one daily log file. On open, an oversized file is moved
	// to <name>.1 and replaced.
	MaxBytes = 2 << 20
	logPerm  = 0o600
	dirPerm  = 0o700
	dateFmt  = "2006-01-02"
)

// Resolved is the effective logging configuration after settings and
// environment overrides.
type Resolved struct {
	Enabled       bool
	Directory     string
	RetentionDays int
	Path          string
}

// Logger appends timestamped lines to one file. It is safe for concurrent use
// within a process; separate agent processes rely on O_APPEND.
type Logger struct {
	mu         sync.Mutex
	file       *os.File
	path       string
	directory  string
	retention  int
	day        string
	enabled    bool
	text       bool
	configPath string
	configSize int64
	configMod  time.Time
}

// Resolve returns the effective settings. An invalid config falls back to the
// built-in defaults so a broken settings file never stops the shell.
func Resolve() (Resolved, error) {
	cfg, _ := settings.Load()
	if cfg.Logging.Directory == "" || cfg.Logging.RetentionDays == 0 {
		cfg, _ = settings.Default()
	}
	dir := cfg.Logging.Directory
	enabled := cfg.Logging.Enabled
	retention := cfg.Logging.RetentionDays
	path := ""

	if value := os.Getenv("OFFLINE_COMPLETE_LOG_DIR"); value != "" {
		dir = value
	}
	if value := os.Getenv("OFFLINE_COMPLETE_LOG"); value != "" {
		path = value
		dir = filepath.Dir(value)
		enabled = true
	}
	if path == "" {
		path = filepath.Join(dir, "agent-"+time.Now().Format(dateFmt)+".log")
	}
	if value := os.Getenv("OFFLINE_COMPLETE_LOG_ENABLED"); value != "" {
		enabled = parseBool(value, enabled)
	}
	if value := os.Getenv("OFFLINE_COMPLETE_LOG_RETENTION_DAYS"); value != "" {
		if days, err := strconv.Atoi(value); err == nil && days >= 1 && days <= 365 {
			retention = days
		}
	}
	if dir == "" {
		dir = filepath.Dir(path)
	}
	return Resolved{
		Enabled:       enabled,
		Directory:     dir,
		RetentionDays: retention,
		Path:          path,
	}, nil
}

func parseBool(value string, fallback bool) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return fallback
	}
}

// Path returns the current daily log path, honoring settings and environment.
func Path() (string, error) {
	resolved, err := Resolve()
	if err != nil {
		return "", err
	}
	return resolved.Path, nil
}

// Open opens the configured daily log. A disabled configuration returns a
// no-op logger with the effective path.
func Open() (*Logger, error) {
	resolved, err := Resolve()
	if err != nil {
		return nil, err
	}
	if !resolved.Enabled {
		logger := &Logger{
			path:      resolved.Path,
			directory: resolved.Directory,
			retention: resolved.RetentionDays,
			enabled:   false,
			text:      os.Getenv("OFFLINE_COMPLETE_LOG_TEXT") == "1",
		}
		logger.stampConfigLocked()
		return logger, nil
	}
	if err := os.MkdirAll(resolved.Directory, dirPerm); err != nil {
		return nil, fmt.Errorf("create log directory: %w", err)
	}
	pruneDir(resolved.Directory, resolved.RetentionDays, time.Now())
	rotateSize(resolved.Path)
	file, err := os.OpenFile(resolved.Path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, logPerm)
	if err != nil {
		return nil, fmt.Errorf("open log: %w", err)
	}
	_ = file.Chmod(logPerm)
	logger := &Logger{
		file:      file,
		path:      resolved.Path,
		directory: resolved.Directory,
		retention: resolved.RetentionDays,
		day:       time.Now().Format(dateFmt),
		enabled:   true,
		text:      os.Getenv("OFFLINE_COMPLETE_LOG_TEXT") == "1",
	}
	logger.stampConfigLocked()
	return logger, nil
}

// OpenAt opens or creates an exact path for tests and custom callers. It keeps
// the legacy 2 MB rotation and does not apply daily retention.
func OpenAt(path string) (*Logger, error) {
	if path == "" {
		return nil, errors.New("empty log path")
	}
	if err := os.MkdirAll(filepath.Dir(path), dirPerm); err != nil {
		return nil, fmt.Errorf("create log directory: %w", err)
	}
	rotateSize(path)
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, logPerm)
	if err != nil {
		return nil, fmt.Errorf("open log: %w", err)
	}
	_ = file.Chmod(logPerm)
	return &Logger{
		file:      file,
		path:      path,
		directory: filepath.Dir(path),
		day:       time.Now().Format(dateFmt),
		enabled:   true,
		text:      os.Getenv("OFFLINE_COMPLETE_LOG_TEXT") == "1",
	}, nil
}

// Printf appends one line. Newlines are escaped so one event cannot forge
// additional log entries. A nil or disabled logger is a no-op.
func (l *Logger) Printf(format string, args ...any) {
	if l == nil {
		return
	}
	message := strings.ReplaceAll(fmt.Sprintf(format, args...), "\n", `\n`)
	message = strings.ReplaceAll(message, "\r", `\r`)
	line := time.Now().Format("2006-01-02T15:04:05.000Z07:00") + " " + message + "\n"
	l.mu.Lock()
	defer l.mu.Unlock()
	l.refreshConfigLocked()
	if !l.enabled {
		return
	}
	l.rollDayLocked()
	if l.file == nil {
		return
	}
	_, _ = l.file.WriteString(line)
}

// Text reports whether callers may include typed text in log events.
func (l *Logger) Text() bool {
	return l != nil && l.enabled && l.text
}

// Enabled reports whether this logger writes.
func (l *Logger) Enabled() bool {
	return l != nil && l.enabled
}

// Path returns the open file path.
func (l *Logger) Path() string {
	if l == nil {
		return ""
	}
	return l.path
}

// Close closes the file. It is safe to call more than once.
func (l *Logger) Close() error {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file == nil {
		return nil
	}
	err := l.file.Close()
	l.file = nil
	return err
}

func (l *Logger) stampConfigLocked() {
	path, err := settings.ConfigPath()
	if err != nil {
		l.configPath = ""
		return
	}
	l.configPath = path
	if info, err := os.Stat(path); err == nil {
		l.configSize = info.Size()
		l.configMod = info.ModTime()
	}
}

// refreshConfigLocked applies a settings change written by oc-manage or the
// GUI. It runs on log events, so enabling, disabling, moving or shortening
// retention affects a running agent without restarting the shell.
func (l *Logger) refreshConfigLocked() {
	if l.configPath == "" {
		return
	}
	info, err := os.Stat(l.configPath)
	if err != nil {
		return
	}
	if info.Size() == l.configSize && info.ModTime().Equal(l.configMod) {
		return
	}
	resolved, _ := Resolve()
	l.configSize = info.Size()
	l.configMod = info.ModTime()
	l.enabled = resolved.Enabled
	l.directory = resolved.Directory
	l.retention = resolved.RetentionDays
	l.text = os.Getenv("OFFLINE_COMPLETE_LOG_TEXT") == "1"
	if !l.enabled {
		if l.file != nil {
			_ = l.file.Close()
			l.file = nil
		}
		l.path = resolved.Path
		return
	}
	if l.file != nil && l.path == resolved.Path {
		if l.retention > 0 {
			pruneDir(l.directory, l.retention, time.Now())
		}
		return
	}
	if l.file != nil {
		_ = l.file.Close()
		l.file = nil
	}
	if err := os.MkdirAll(l.directory, dirPerm); err != nil {
		return
	}
	pruneDir(l.directory, l.retention, time.Now())
	rotateSize(resolved.Path)
	file, err := os.OpenFile(resolved.Path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, logPerm)
	if err != nil {
		return
	}
	_ = file.Chmod(logPerm)
	l.file = file
	l.path = resolved.Path
	l.day = time.Now().Format(dateFmt)
}

func (l *Logger) rollDayLocked() {
	if l.directory == "" {
		return
	}
	today := time.Now().Format(dateFmt)
	if today == l.day {
		return
	}
	path := filepath.Join(l.directory, "agent-"+today+".log")
	rotateSize(path)
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, logPerm)
	if err != nil {
		// Keep the previous file rather than dropping events; retry next write.
		return
	}
	_ = file.Chmod(logPerm)
	if l.file != nil {
		_ = l.file.Close()
	}
	l.file = file
	l.path = path
	l.day = today
	if l.retention > 0 {
		pruneDir(l.directory, l.retention, time.Now())
	}
}

func rotateSize(path string) {
	if info, err := os.Stat(path); err == nil && info.Size() > MaxBytes {
		_ = os.Rename(path, path+".1")
	}
}

// pruneDir removes this application's log files older than retention days.
// It ignores unrelated files and never follows directories.
func pruneDir(dir string, retention int, now time.Time) {
	if retention <= 0 {
		return
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	today := startOfDay(now)
	cutoff := today.AddDate(0, 0, -(retention - 1))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		appLog := name == "agent.log" || name == "agent.log.1" ||
			(strings.HasPrefix(name, "agent-") && (strings.HasSuffix(name, ".log") || strings.HasSuffix(name, ".log.1")))
		if !appLog {
			continue
		}
		date, ok := dateFromName(name, now.Location())
		if !ok {
			info, err := entry.Info()
			if err != nil {
				continue
			}
			date = startOfDay(info.ModTime())
		}
		if date.Before(cutoff) {
			_ = os.Remove(filepath.Join(dir, name))
		}
	}
}

func dateFromName(name string, location *time.Location) (time.Time, bool) {
	value := strings.TrimPrefix(name, "agent-")
	if len(value) < len(dateFmt) {
		return time.Time{}, false
	}
	date, err := time.ParseInLocation(dateFmt, value[:len(dateFmt)], location)
	return date, err == nil
}

func startOfDay(value time.Time) time.Time {
	return time.Date(value.Year(), value.Month(), value.Day(), 0, 0, 0, 0, value.Location())
}
