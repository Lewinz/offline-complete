// Package settings persists local application settings. The first version
// stores only logging options in ~/.oc/config.json; it never contacts the
// network and never changes the user's shell configuration.
package settings

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const (
	// SchemaVersion is the on-disk settings version.
	SchemaVersion = 1
	// DefaultRetentionDays keeps today and the two previous days.
	DefaultRetentionDays = 3
	maxFile              = 1 << 20
)

// Logging controls agent diagnostic logging.
type Logging struct {
	Enabled       bool   `json:"enabled"`
	Directory     string `json:"directory"`
	RetentionDays int    `json:"retentionDays"`
}

// AI controls the optional completion-time model request. The key is stored in
// the private settings file, not in the app bundle.
type AI struct {
	Enabled   bool   `json:"enabled"`
	Protocol  string `json:"protocol"`
	URL       string `json:"url"`
	Key       string `json:"key"`
	Model     string `json:"model"`
	MaxTokens int    `json:"maxTokens"`
}

// Autocomplete controls the candidate-window keyboard behavior.
type Autocomplete struct {
	TabAction         string `json:"tabAction"`         // next | accept
	EnterAction       string `json:"enterAction"`       // select | execute
	ExactMatchExecute bool   `json:"exactMatchExecute"` // Enter on an exact match executes the typed line
}

// MenuBar controls the background status item.
type MenuBar struct {
	Enabled bool `json:"enabled"`
}

// Config is the complete local settings document.
type Config struct {
	SchemaVersion int          `json:"schemaVersion"`
	Logging       Logging      `json:"logging"`
	AI            AI           `json:"ai"`
	Autocomplete  Autocomplete `json:"autocomplete"`
	MenuBar       MenuBar      `json:"menuBar"`
}

type fileConfig struct {
	SchemaVersion *int              `json:"schemaVersion"`
	Logging       *fileLogging      `json:"logging"`
	AI            *fileAI           `json:"ai"`
	Autocomplete  *fileAutocomplete `json:"autocomplete"`
	MenuBar       *fileMenuBar      `json:"menuBar"`
}

type fileMenuBar struct {
	Enabled *bool `json:"enabled"`
}

type fileAutocomplete struct {
	TabAction         *string `json:"tabAction"`
	EnterAction       *string `json:"enterAction"`
	ExactMatchExecute *bool   `json:"exactMatchExecute"`
}

type fileAI struct {
	Enabled   *bool   `json:"enabled"`
	Protocol  *string `json:"protocol"`
	URL       *string `json:"url"`
	Key       *string `json:"key"`
	Model     *string `json:"model"`
	MaxTokens *int    `json:"maxTokens"`
}

type fileLogging struct {
	Enabled       *bool   `json:"enabled"`
	Directory     *string `json:"directory"`
	RetentionDays *int    `json:"retentionDays"`
}

// ConfigPath returns OFFLINE_COMPLETE_CONFIG or ~/.oc/config.json.
func ConfigPath() (string, error) {
	if value := os.Getenv("OFFLINE_COMPLETE_CONFIG"); value != "" {
		return value, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".oc", "config.json"), nil
}

// DefaultLogDirectory returns ~/.oc/logs.
func DefaultLogDirectory() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".oc", "logs"), nil
}

// Default returns the built-in settings.
func Default() (Config, error) {
	dir, err := DefaultLogDirectory()
	if err != nil {
		return Config{}, err
	}
	return Config{
		SchemaVersion: SchemaVersion,
		Logging: Logging{
			Enabled:       true,
			Directory:     dir,
			RetentionDays: DefaultRetentionDays,
		},
		AI: AI{
			Enabled:   false,
			Protocol:  "openai",
			MaxTokens: 1024,
		},
		Autocomplete: Autocomplete{
			TabAction:         "next",
			EnterAction:       "select",
			ExactMatchExecute: true,
		},
		MenuBar: MenuBar{Enabled: true},
	}, nil
}

// Load reads settings, returning defaults for a missing file. An invalid file
// returns the defaults together with the error so callers can keep running.
func Load() (Config, error) {
	defaults, err := Default()
	if err != nil {
		return Config{}, err
	}
	path, err := ConfigPath()
	if err != nil {
		return defaults, err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return defaults, nil
	}
	if err != nil {
		return defaults, fmt.Errorf("read settings: %w", err)
	}
	if len(data) > maxFile {
		return defaults, errors.New("settings file exceeds size limit")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var raw fileConfig
	if err := decoder.Decode(&raw); err != nil {
		return defaults, fmt.Errorf("parse settings: %w", err)
	}
	var extra json.RawMessage
	if err := decoder.Decode(&extra); err != io.EOF {
		return defaults, errors.New("trailing settings data")
	}
	if raw.SchemaVersion == nil || *raw.SchemaVersion != SchemaVersion {
		return defaults, errors.New("unsupported settings schema")
	}
	cfg := defaults
	if raw.Logging != nil {
		if raw.Logging.Enabled != nil {
			cfg.Logging.Enabled = *raw.Logging.Enabled
		}
		if raw.Logging.Directory != nil {
			cfg.Logging.Directory = *raw.Logging.Directory
		}
		if raw.Logging.RetentionDays != nil {
			cfg.Logging.RetentionDays = *raw.Logging.RetentionDays
		}
	}
	if raw.AI != nil {
		if raw.AI.Enabled != nil {
			cfg.AI.Enabled = *raw.AI.Enabled
		}
		if raw.AI.Protocol != nil {
			cfg.AI.Protocol = *raw.AI.Protocol
		}
		if raw.AI.URL != nil {
			cfg.AI.URL = *raw.AI.URL
		}
		if raw.AI.Key != nil {
			cfg.AI.Key = *raw.AI.Key
		}
		if raw.AI.Model != nil {
			cfg.AI.Model = *raw.AI.Model
		}
		if raw.AI.MaxTokens != nil {
			cfg.AI.MaxTokens = *raw.AI.MaxTokens
		}
	}
	if raw.Autocomplete != nil {
		if raw.Autocomplete.TabAction != nil {
			cfg.Autocomplete.TabAction = *raw.Autocomplete.TabAction
		}
		if raw.Autocomplete.EnterAction != nil {
			cfg.Autocomplete.EnterAction = *raw.Autocomplete.EnterAction
		}
		if raw.Autocomplete.ExactMatchExecute != nil {
			cfg.Autocomplete.ExactMatchExecute = *raw.Autocomplete.ExactMatchExecute
		}
	}
	if raw.MenuBar != nil && raw.MenuBar.Enabled != nil {
		cfg.MenuBar.Enabled = *raw.MenuBar.Enabled
	}
	if err := cfg.Normalize(); err != nil {
		return defaults, err
	}
	return cfg, nil
}

// Normalize expands ~, validates the logging directory and retention.
func (c *Config) Normalize() error {
	if c.SchemaVersion == 0 {
		c.SchemaVersion = SchemaVersion
	}
	if c.SchemaVersion != SchemaVersion {
		return fmt.Errorf("unsupported settings schema: %d", c.SchemaVersion)
	}
	if c.Autocomplete.TabAction == "" {
		c.Autocomplete.TabAction = "next"
	}
	if c.Autocomplete.EnterAction == "" {
		c.Autocomplete.EnterAction = "select"
	}
	if c.Autocomplete.TabAction != "next" && c.Autocomplete.TabAction != "accept" {
		return fmt.Errorf("unsupported tab action: %q", c.Autocomplete.TabAction)
	}
	if c.Autocomplete.EnterAction != "select" && c.Autocomplete.EnterAction != "execute" {
		return fmt.Errorf("unsupported enter action: %q", c.Autocomplete.EnterAction)
	}
	if c.Logging.RetentionDays == 0 {
		c.Logging.RetentionDays = DefaultRetentionDays
	}
	if c.Logging.RetentionDays < 1 || c.Logging.RetentionDays > 365 {
		return fmt.Errorf("retention days must be between 1 and 365: %d", c.Logging.RetentionDays)
	}
	if c.Logging.Directory == "" {
		dir, err := DefaultLogDirectory()
		if err != nil {
			return err
		}
		c.Logging.Directory = dir
	}
	dir, err := expandHome(c.Logging.Directory)
	if err != nil {
		return err
	}
	if !filepath.IsAbs(dir) || filepath.Clean(dir) != dir || dir == string(filepath.Separator) {
		return fmt.Errorf("log directory must be an absolute clean non-root path: %q", dir)
	}
	if strings.ContainsAny(dir, "\r\n\x00") {
		return errors.New("log directory contains control characters")
	}
	c.Logging.Directory = dir
	if c.AI.Protocol == "" {
		c.AI.Protocol = "openai"
	}
	if c.AI.Protocol != "openai" && c.AI.Protocol != "anthropic" {
		return fmt.Errorf("unsupported AI protocol: %q", c.AI.Protocol)
	}
	if c.AI.MaxTokens == 0 {
		c.AI.MaxTokens = 1024
	}
	if c.AI.MaxTokens < 1 || c.AI.MaxTokens > 32768 {
		return fmt.Errorf("AI max tokens must be between 1 and 32768: %d", c.AI.MaxTokens)
	}
	if strings.ContainsAny(c.AI.URL+c.AI.Key+c.AI.Model, "\r\n\x00") {
		return errors.New("AI settings contain control characters")
	}
	if c.AI.Enabled {
		if strings.TrimSpace(c.AI.URL) == "" {
			return errors.New("AI URL is required when enabled")
		}
		if !strings.HasPrefix(c.AI.URL, "https://") && !strings.HasPrefix(c.AI.URL, "http://") {
			return errors.New("AI URL must be http(s)")
		}
	}
	return nil
}

// AIEnvironment returns the process environment consumed by the Node runtime.
// Values are always emitted so settings override inherited shell variables; an
// empty value disables the corresponding setting.
func AIEnvironment(ai AI) []string {
	enabled := ai.Enabled
	url := ai.URL
	protocol := ai.Protocol
	if !enabled {
		url = ""
		protocol = ""
	}
	return []string{
		"OFFLINE_COMPLETE_AI_ENABLED=" + boolString(enabled),
		"OFFLINE_COMPLETE_AI_PROTOCOL=" + protocol,
		"OFFLINE_COMPLETE_AI_URL=" + url,
		"OFFLINE_COMPLETE_AI_KEY=" + ai.Key,
		"OFFLINE_COMPLETE_AI_MODEL=" + ai.Model,
		"OFFLINE_COMPLETE_AI_MAX_TOKENS=" + fmt.Sprint(ai.MaxTokens),
	}
}

func boolString(value bool) string {
	if value {
		return "1"
	}
	return "0"
}

func expandHome(path string) (string, error) {
	if path == "~" || strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		if path == "~" {
			return home, nil
		}
		return filepath.Join(home, path[2:]), nil
	}
	return path, nil
}

// Save normalizes and atomically writes the settings with private permissions.
func Save(cfg Config) error {
	if err := cfg.Normalize(); err != nil {
		return err
	}
	path, err := ConfigPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create settings directory: %w", err)
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(path), ".config-*.tmp")
	if err != nil {
		return err
	}
	name := temp.Name()
	defer os.Remove(name)
	if err := temp.Chmod(0o600); err != nil {
		temp.Close()
		return err
	}
	if _, err := temp.Write(data); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}
