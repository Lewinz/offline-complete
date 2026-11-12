// Command oc-manage inspects and clears the local learning data and checks the
// development installation. It never contacts the network.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"offline-complete/internal/config"
	"offline-complete/internal/diag"
	"offline-complete/internal/rules"
	"offline-complete/internal/settings"
	"offline-complete/internal/store"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		usage(stderr)
		return 2
	}
	switch args[0] {
	case "status":
		return status(stdout, stderr)
	case "learning":
		return learning(args[1:], stdout, stderr)
	case "clear":
		return clear(args[1:], stdout, stderr)
	case "doctor":
		return doctor(stdout, stderr)
	case "logging":
		return logging(args[1:], stdout, stderr)
	case "ai":
		return aiCommand(args[1:], stdout, stderr)
	case "shell":
		return shell(args[1:], stdout, stderr)
	case "help", "-h", "--help":
		usage(stdout)
		return 0
	default:
		fmt.Fprintf(stderr, "oc-manage: unknown command %q\n", args[0])
		usage(stderr)
		return 2
	}
}

func usage(writer io.Writer) {
	fmt.Fprintln(writer, `usage: oc-manage COMMAND

commands:
  status              local data, learning and rules status
  learning on|off     enable or disable local learning
  learning status     show the learning setting
  clear --yes         delete all local learning selections
  logging status      show log settings and the current file
  logging status --json
  logging on|off      enable or disable agent logging
  logging set --enabled on|off --directory PATH --retention-days N
  ai status           show optional AI settings
  ai set --enabled on|off --protocol openai|anthropic --url URL
         [--key KEY] [--clear-key] [--model MODEL] [--max-tokens N]
  shell status        show zsh auto-integration state
  shell install       make new interactive zsh windows start through oc
  shell uninstall     remove the marked zsh block
  doctor              check paths, rules and known Kiro installs`)
}

func openStore() (*store.Store, error) {
	dir, err := store.DefaultDir()
	if err != nil {
		return nil, err
	}
	return store.Open(dir)
}

func status(stdout, stderr io.Writer) int {
	local, err := openStore()
	if err != nil {
		fmt.Fprintf(stderr, "oc-manage: %v\n", err)
		return 1
	}
	stats := local.Stats()
	fmt.Fprintf(stdout, "data: %s\n", stats.Path)
	fmt.Fprintf(stdout, "learning: %t\n", stats.Learning)
	fmt.Fprintf(stdout, "selections: %d\n", stats.Selections)
	fmt.Fprintf(stdout, "data bytes: %d\n", stats.Bytes)
	printRules(stdout, stderr)
	if window := config.WindowPath(); window != "" {
		fmt.Fprintf(stdout, "window: %s\n", window)
	} else {
		fmt.Fprintln(stdout, "window: not found")
	}
	if app := config.AppPath(); app != "" {
		fmt.Fprintf(stdout, "app: %s\n", app)
	} else {
		fmt.Fprintln(stdout, "app: not found")
	}
	if cfg, err := settings.Load(); err != nil {
		fmt.Fprintf(stdout, "logging: invalid settings (%v)\n", err)
	} else {
		fmt.Fprintf(stdout, "logging: %t\n", cfg.Logging.Enabled)
		fmt.Fprintf(stdout, "log dir: %s\n", cfg.Logging.Directory)
		fmt.Fprintf(stdout, "log retention days: %d\n", cfg.Logging.RetentionDays)
	}
	if home, err := os.UserHomeDir(); err == nil {
		if path, installed, err := shellStatus(home); err == nil {
			fmt.Fprintf(stdout, "shell auto-start: %t (%s)\n", installed, path)
		}
	}
	if logPath, err := diag.Path(); err == nil {
		fmt.Fprintf(stdout, "log: %s\n", logPath)
	}
	return 0
}

func learning(args []string, stdout, stderr io.Writer) int {
	if len(args) != 1 {
		fmt.Fprintln(stderr, "usage: oc-manage learning on|off|status")
		return 2
	}
	local, err := openStore()
	if err != nil {
		fmt.Fprintf(stderr, "oc-manage: %v\n", err)
		return 1
	}
	switch args[0] {
	case "on", "off":
		if err := local.SetLearning(args[0] == "on"); err != nil {
			fmt.Fprintf(stderr, "oc-manage: %v\n", err)
			return 1
		}
		fmt.Fprintf(stdout, "learning: %t\n", local.Learning())
	case "status":
		fmt.Fprintf(stdout, "learning: %t\n", local.Learning())
	default:
		fmt.Fprintln(stderr, "usage: oc-manage learning on|off|status")
		return 2
	}
	return 0
}

func clear(args []string, stdout, stderr io.Writer) int {
	confirmed := false
	for _, argument := range args {
		if argument == "--yes" {
			confirmed = true
		}
	}
	if !confirmed {
		fmt.Fprintln(stderr, "oc-manage: clear requires --yes")
		return 2
	}
	local, err := openStore()
	if err != nil {
		fmt.Fprintf(stderr, "oc-manage: %v\n", err)
		return 1
	}
	if err := local.Clear(); err != nil {
		fmt.Fprintf(stderr, "oc-manage: %v\n", err)
		return 1
	}
	fmt.Fprintln(stdout, "learning selections cleared")
	return 0
}

func printRules(stdout, stderr io.Writer) {
	path := config.RulesPath()
	if path == "" {
		fmt.Fprintln(stdout, "rules: not found")
		return
	}
	pack, hash, err := loadRules(path)
	if err != nil {
		fmt.Fprintf(stderr, "oc-manage: rules: %v\n", err)
		fmt.Fprintf(stdout, "rules: %s (invalid)\n", path)
		return
	}
	fmt.Fprintf(stdout, "rules: %s\n", path)
	fmt.Fprintf(stdout, "rules commands: %d\n", len(pack.Commands))
	fmt.Fprintf(stdout, "rules sha256: %s\n", hash)
}

func loadRules(path string) (*rules.RulePack, string, error) {
	data, err := rules.LoadFile(path)
	if err != nil {
		return nil, "", err
	}
	if len(data) > 16<<20 {
		return nil, "", errors.New("rule pack too large")
	}
	pack, err := rules.FromJSON(data)
	if err != nil {
		return nil, "", err
	}
	sum := sha256.Sum256(data)
	return pack, hex.EncodeToString(sum[:]), nil
}

func aiCommand(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: oc-manage ai status|set ...")
		return 2
	}
	cfg, err := settings.Load()
	if err != nil {
		fmt.Fprintf(stderr, "oc-manage: settings: %v\n", err)
	}
	switch args[0] {
	case "status":
		fmt.Fprintf(stdout, "ai enabled: %t\n", cfg.AI.Enabled)
		fmt.Fprintf(stdout, "ai protocol: %s\n", cfg.AI.Protocol)
		fmt.Fprintf(stdout, "ai url: %s\n", cfg.AI.URL)
		fmt.Fprintf(stdout, "ai key: %t\n", cfg.AI.Key != "")
		fmt.Fprintf(stdout, "ai model: %s\n", cfg.AI.Model)
		fmt.Fprintf(stdout, "ai max tokens: %d\n", cfg.AI.MaxTokens)
		return 0
	case "set":
		fs := flag.NewFlagSet("ai set", flag.ContinueOnError)
		fs.SetOutput(stderr)
		enabled := fs.String("enabled", "", "on|off")
		protocol := fs.String("protocol", "", "openai|anthropic")
		url := fs.String("url", "", "endpoint URL")
		key := fs.String("key", "", "API key")
		clearKey := fs.Bool("clear-key", false, "remove the API key")
		model := fs.String("model", "", "model name")
		maxTokens := fs.Int("max-tokens", 0, "maximum output tokens")
		if err := fs.Parse(args[1:]); err != nil {
			return 2
		}
		if *enabled != "" {
			switch *enabled {
			case "on":
				cfg.AI.Enabled = true
			case "off":
				cfg.AI.Enabled = false
			default:
				fmt.Fprintln(stderr, "oc-manage: --enabled must be on or off")
				return 2
			}
		}
		if *protocol != "" {
			cfg.AI.Protocol = *protocol
		}
		if *url != "" {
			cfg.AI.URL = *url
		}
		if *clearKey {
			cfg.AI.Key = ""
		} else if *key != "" {
			cfg.AI.Key = *key
		}
		if *model != "" {
			cfg.AI.Model = *model
		}
		if *maxTokens > 0 {
			cfg.AI.MaxTokens = *maxTokens
		}
		if err := settings.Save(cfg); err != nil {
			fmt.Fprintf(stderr, "oc-manage: save AI settings: %v\n", err)
			return 1
		}
		fmt.Fprintf(stdout, "ai enabled: %t\nai protocol: %s\nai url: %s\n", cfg.AI.Enabled, cfg.AI.Protocol, cfg.AI.URL)
		return 0
	default:
		fmt.Fprintln(stderr, "usage: oc-manage ai status|set ...")
		return 2
	}
}

func doctor(stdout, stderr io.Writer) int {
	problems := 0
	local, err := openStore()
	if err != nil {
		fmt.Fprintf(stdout, "FAIL data: %v\n", err)
		problems++
	} else {
		stats := local.Stats()
		fmt.Fprintf(stdout, "OK data: %s (learning=%t selections=%d)\n", stats.Path, stats.Learning, stats.Selections)
	}

	if path := config.RulesPath(); path == "" {
		fmt.Fprintln(stdout, "FAIL rules: not found")
		problems++
	} else if pack, hash, err := loadRules(path); err != nil {
		fmt.Fprintf(stdout, "FAIL rules: %v\n", err)
		problems++
	} else {
		fmt.Fprintf(stdout, "OK rules: %s (%d commands, sha256 %s)\n", path, len(pack.Commands), hash)
	}

	if window := config.WindowPath(); window != "" {
		fmt.Fprintf(stdout, "OK window: %s\n", window)
	} else {
		fmt.Fprintln(stdout, "WARN window: not found; run make bin")
	}

	if app := config.AppPath(); app != "" {
		fmt.Fprintf(stdout, "OK app: %s\n", app)
	} else {
		fmt.Fprintln(stdout, "WARN app: not found; the main window is unavailable")
	}

	if home, err := os.UserHomeDir(); err == nil {
		if path, installed, err := shellStatus(home); err != nil {
			fmt.Fprintf(stdout, "WARN shell: %v\n", err)
		} else if installed {
			fmt.Fprintf(stdout, "OK shell: auto-start in %s\n", path)
		} else {
			fmt.Fprintf(stdout, "WARN shell: not installed; run oc-manage shell install (new zsh windows will not auto-start)\n")
		}
	}

	if _, settingsErr := settings.Load(); settingsErr != nil {
		fmt.Fprintf(stdout, "WARN logging settings: %v\n", settingsErr)
	}
	resolved, resolveErr := diag.Resolve()
	if resolveErr != nil {
		fmt.Fprintf(stdout, "WARN log: %v\n", resolveErr)
	} else {
		state := "disabled"
		if resolved.Enabled {
			state = "enabled"
		}
		fmt.Fprintf(stdout, "OK logging: %s dir=%s retention=%d days\n", state, resolved.Directory, resolved.RetentionDays)
		if resolved.Enabled {
			if info, err := os.Stat(resolved.Directory); err == nil && !info.IsDir() {
				fmt.Fprintf(stdout, "FAIL log dir: %s is not a directory\n", resolved.Directory)
				problems++
			} else if err == nil && info.Mode().Perm()&0o022 != 0 {
				fmt.Fprintf(stdout, "WARN log dir: %s is group/other writable\n", resolved.Directory)
			}
			if info, err := os.Stat(resolved.Path); err != nil {
				fmt.Fprintf(stdout, "OK log: %s (not created yet)\n", resolved.Path)
			} else {
				fmt.Fprintf(stdout, "OK log: %s (%d bytes)\n", resolved.Path, info.Size())
			}
		}
	}

	base, err := config.SocketBase()
	if err != nil {
		fmt.Fprintf(stdout, "FAIL socket base: %v\n", err)
		problems++
	} else if info, err := os.Stat(base); err != nil {
		fmt.Fprintf(stdout, "WARN socket base: %s (not created yet)\n", base)
	} else if info.Mode().Perm()&0o077 != 0 {
		fmt.Fprintf(stdout, "FAIL socket base: %s is not private (%o)\n", base, info.Mode().Perm())
		problems++
	} else {
		fmt.Fprintf(stdout, "OK socket base: %s\n", base)
	}

	for _, path := range kiroPaths() {
		if _, err := os.Stat(path); err == nil {
			fmt.Fprintf(stdout, "WARN Kiro: found %s; do not enable two completion owners in one shell\n", path)
		}
	}
	fmt.Fprintln(stdout, "INFO accessibility: run oc-window --accessibility-prompt, then grant it in System Settings")
	if problems != 0 {
		fmt.Fprintf(stderr, "oc-manage: %d problem(s)\n", problems)
		return 1
	}
	return 0
}

type loggingStatus struct {
	Enabled       bool   `json:"enabled"`
	Directory     string `json:"directory"`
	RetentionDays int    `json:"retentionDays"`
	CurrentPath   string `json:"currentPath"`
	Error         string `json:"error,omitempty"`
}

func logging(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: oc-manage logging status [--json] | on|off | set ...")
		return 2
	}
	cfg, loadErr := settings.Load()
	switch args[0] {
	case "status":
		resolved, _ := diag.Resolve()
		status := loggingStatus{
			Enabled:       cfg.Logging.Enabled,
			Directory:     cfg.Logging.Directory,
			RetentionDays: cfg.Logging.RetentionDays,
			CurrentPath:   resolved.Path,
		}
		if loadErr != nil {
			status.Error = loadErr.Error()
		}
		jsonOutput := len(args) > 1 && args[1] == "--json"
		if jsonOutput {
			data, err := json.MarshalIndent(status, "", "  ")
			if err != nil {
				fmt.Fprintf(stderr, "oc-manage: %v\n", err)
				return 1
			}
			fmt.Fprintln(stdout, string(data))
			return 0
		}
		fmt.Fprintf(stdout, "enabled: %t\n", status.Enabled)
		fmt.Fprintf(stdout, "directory: %s\n", status.Directory)
		fmt.Fprintf(stdout, "retention days: %d\n", status.RetentionDays)
		fmt.Fprintf(stdout, "current: %s\n", status.CurrentPath)
		if status.Error != "" {
			fmt.Fprintf(stderr, "oc-manage: settings: %s\n", status.Error)
			return 1
		}
		return 0
	case "on", "off":
		cfg.Logging.Enabled = args[0] == "on"
		if err := settings.Save(cfg); err != nil {
			fmt.Fprintf(stderr, "oc-manage: %v\n", err)
			return 1
		}
		fmt.Fprintf(stdout, "enabled: %t\n", cfg.Logging.Enabled)
		return 0
	case "set":
		flags := flag.NewFlagSet("logging set", flag.ContinueOnError)
		flags.SetOutput(stderr)
		enabled := flags.String("enabled", "", "on or off")
		directory := flags.String("directory", "", "log directory")
		retention := flags.Int("retention-days", 0, "retention in days")
		if err := flags.Parse(args[1:]); err != nil {
			return 2
		}
		if *enabled == "" && *directory == "" && *retention == 0 {
			fmt.Fprintln(stderr, "oc-manage: logging set requires --enabled, --directory or --retention-days")
			return 2
		}
		if *enabled != "" {
			switch *enabled {
			case "on", "true", "1":
				cfg.Logging.Enabled = true
			case "off", "false", "0":
				cfg.Logging.Enabled = false
			default:
				fmt.Fprintln(stderr, "oc-manage: --enabled must be on or off")
				return 2
			}
		}
		if *directory != "" {
			cfg.Logging.Directory = *directory
		}
		if *retention != 0 {
			cfg.Logging.RetentionDays = *retention
		}
		if err := settings.Save(cfg); err != nil {
			fmt.Fprintf(stderr, "oc-manage: %v\n", err)
			return 1
		}
		fmt.Fprintf(stdout, "enabled: %t\ndirectory: %s\nretention days: %d\n", cfg.Logging.Enabled, cfg.Logging.Directory, cfg.Logging.RetentionDays)
		return 0
	default:
		fmt.Fprintln(stderr, "usage: oc-manage logging status [--json] | on|off | set ...")
		return 2
	}
}

func kiroPaths() []string {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	return []string{
		"/Applications/Kiro CLI.app",
		filepath.Join(home, ".local", "bin", "kiro-cli"),
		filepath.Join(home, "Library", "Application Support", "kiro-cli"),
		filepath.Join(home, "Library", "Application Support", "amazon-q"),
	}
}
