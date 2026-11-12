// Package config resolves paths for a development install. It does not create
// directories or read user configuration.
package config

import (
	"os"
	"path/filepath"
	"runtime"
)

// RulesPath returns the explicit rule pack, the bundled app resource, or the
// repository development pack.
func RulesPath() string {
	if value := os.Getenv("OFFLINE_COMPLETE_RULES"); value != "" {
		return value
	}
	if executable, err := os.Executable(); err == nil {
		for _, name := range []string{"rules.bin", "rules.json"} {
			candidate := filepath.Join(filepath.Dir(executable), "..", "Resources", name)
			if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
				return candidate
			}
		}
	}
	for _, name := range []string{"base.bin", "base.json"} {
		path := filepath.Join("rules", name)
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			if absolute, err := filepath.Abs(path); err == nil {
				return absolute
			}
		}
	}
	return ""
}

// SpecsDir returns the directory containing upstream spec modules. Installed
// builds use encrypted .bin files; development builds use plain .js files.
func SpecsDir() string {
	if value := os.Getenv("OFFLINE_COMPLETE_SPECS"); value != "" {
		return value
	}
	if executable, err := os.Executable(); err == nil {
		candidate := filepath.Join(filepath.Dir(executable), "..", "Resources", "specs")
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			return candidate
		}
	}
	candidate := filepath.Join("rules", "specs")
	if info, err := os.Stat(candidate); err == nil && info.IsDir() {
		if absolute, err := filepath.Abs(candidate); err == nil {
			return absolute
		}
	}
	return ""
}

// WindowPath returns the explicit window binary, a sibling of the agent, or
// the development build path.
func WindowPath() string {
	if value, ok := os.LookupEnv("OFFLINE_COMPLETE_WINDOW"); ok {
		if value == "0" || value == "" {
			return ""
		}
		return value
	}
	if executable, err := os.Executable(); err == nil {
		candidate := filepath.Join(filepath.Dir(executable), "oc-window")
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate
		}
	}
	candidate := filepath.Join("bin", "oc-window")
	if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
		if absolute, err := filepath.Abs(candidate); err == nil {
			return absolute
		}
	}
	return ""
}

// AppPath returns the main window executable next to this binary or in the
// development bin directory.
func AppPath() string {
	if executable, err := os.Executable(); err == nil {
		candidate := filepath.Join(filepath.Dir(executable), "oc-app")
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate
		}
	}
	candidate := filepath.Join("bin", "oc-app")
	if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
		if absolute, err := filepath.Abs(candidate); err == nil {
			return absolute
		}
	}
	return ""
}

// DataDir returns the private application data directory.
func DataDir() (string, error) {
	if value := os.Getenv("OFFLINE_COMPLETE_DATA_DIR"); value != "" {
		return value, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "Library", "Application Support", "offline-complete"), nil
}

// SocketBase returns a private directory beneath the data directory. The
// intermediate data directory must be private; the agent creates it before
// requesting the socket base. OFFLINE_COMPLETE_SOCKET_BASE overrides this for
// tests and custom installs.
func SocketBase() (string, error) {
	if value := os.Getenv("OFFLINE_COMPLETE_SOCKET_BASE"); value != "" {
		return value, nil
	}
	dir, err := DataDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "run"), nil
}

// NodePath returns the JavaScript runtime bundled with the app. Development
// builds fall back to third_party/node; callers may still provide an explicit
// OFFLINE_COMPLETE_NODE for testing.
func NodePath() string {
	if value := os.Getenv("OFFLINE_COMPLETE_NODE"); value != "" {
		if info, err := os.Stat(value); err == nil && !info.IsDir() {
			return value
		}
		return ""
	}
	name := "node-" + runtime.GOARCH
	if runtime.GOARCH == "amd64" {
		name = "node-x64"
	}
	if executable, err := os.Executable(); err == nil {
		candidate := filepath.Join(filepath.Dir(executable), "..", "Resources", "node", name)
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate
		}
	}
	candidate := filepath.Join("third_party", "node", name)
	if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
		if absolute, err := filepath.Abs(candidate); err == nil {
			return absolute
		}
	}
	return ""
}
