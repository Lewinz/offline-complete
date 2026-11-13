package settings

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultsUnderHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("OFFLINE_COMPLETE_CONFIG", "")

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Logging.Enabled || cfg.Logging.RetentionDays != 3 {
		t.Fatalf("defaults = %+v", cfg)
	}
	if cfg.Autocomplete.TabAction != "next" || cfg.Autocomplete.EnterAction != "select" || !cfg.Autocomplete.ExactMatchExecute {
		t.Fatalf("autocomplete defaults = %+v", cfg.Autocomplete)
	}
	if !cfg.MenuBar.Enabled {
		t.Fatal("menu bar should default to enabled")
	}
	wantDir := filepath.Join(home, ".oc", "logs")
	if cfg.Logging.Directory != wantDir {
		t.Fatalf("directory = %q, want %q", cfg.Logging.Directory, wantDir)
	}
	path, err := ConfigPath()
	if err != nil {
		t.Fatal(err)
	}
	if path != filepath.Join(home, ".oc", "config.json") {
		t.Fatalf("config path = %q", path)
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("OFFLINE_COMPLETE_CONFIG", "")
	cfg, err := Default()
	if err != nil {
		t.Fatal(err)
	}
	cfg.Logging.Enabled = false
	cfg.Logging.Directory = filepath.Join(home, "custom logs")
	cfg.Logging.RetentionDays = 7
	cfg.Autocomplete = Autocomplete{TabAction: "accept", EnterAction: "execute", ExactMatchExecute: false}
	cfg.MenuBar.Enabled = false
	if err := Save(cfg); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Logging != cfg.Logging {
		t.Fatalf("loaded = %+v, want %+v", loaded.Logging, cfg.Logging)
	}
	if loaded.Autocomplete != cfg.Autocomplete {
		t.Fatalf("autocomplete = %+v, want %+v", loaded.Autocomplete, cfg.Autocomplete)
	}
	if loaded.MenuBar != cfg.MenuBar {
		t.Fatalf("menu bar = %+v, want %+v", loaded.MenuBar, cfg.MenuBar)
	}
	path, _ := ConfigPath()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("config mode = %o", info.Mode().Perm())
	}
}

func TestInvalidSettingsReturnDefaultsAndError(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("OFFLINE_COMPLETE_CONFIG", "")
	path := filepath.Join(home, ".oc", "config.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"schemaVersion":1,"logging":{"enabled":true,"directory":"relative","retentionDays":3}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load()
	if err == nil {
		t.Fatal("expected an invalid-directory error")
	}
	if !cfg.Logging.Enabled || cfg.Logging.RetentionDays != 3 {
		t.Fatalf("fallback = %+v", cfg)
	}
}

func TestEnvConfigPathOverridesHome(t *testing.T) {
	path := filepath.Join(t.TempDir(), "custom.json")
	t.Setenv("OFFLINE_COMPLETE_CONFIG", path)
	got, err := ConfigPath()
	if err != nil {
		t.Fatal(err)
	}
	if got != path {
		t.Fatalf("path = %q, want %q", got, path)
	}
}

func TestAISettingsRoundTripAndEnvironment(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("OFFLINE_COMPLETE_CONFIG", "")
	cfg, err := Default()
	if err != nil {
		t.Fatal(err)
	}
	cfg.AI = AI{
		Enabled:   true,
		Protocol:  "anthropic",
		URL:       "https://api.anthropic.com/v1/messages",
		Key:       "secret",
		Model:     "claude-test",
		MaxTokens: 2048,
	}
	if err := Save(cfg); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if loaded.AI != cfg.AI {
		t.Fatalf("AI = %+v, want %+v", loaded.AI, cfg.AI)
	}
	env := AIEnvironment(cfg.AI)
	if !containsEnv(env, "OFFLINE_COMPLETE_AI_ENABLED=1") || !containsEnv(env, "OFFLINE_COMPLETE_AI_URL=https://api.anthropic.com/v1/messages") {
		t.Fatalf("env = %#v", env)
	}
}

func TestAISettingsRejectsInvalidEnabledConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("OFFLINE_COMPLETE_CONFIG", "")
	cfg, _ := Default()
	cfg.AI.Enabled = true
	cfg.AI.URL = "file:///tmp/x"
	if err := Save(cfg); err == nil {
		t.Fatal("accepted non-http AI URL")
	}
}

func containsEnv(env []string, want string) bool {
	for _, item := range env {
		if item == want {
			return true
		}
	}
	return false
}
