// Command inventory-rules prints a read-only manifest of rule directories.
// With no arguments it checks the known local Kiro and Amazon Q rule paths.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"unicode/utf8"

	"offline-complete/internal/inventory"
)

func main() {
	roots := os.Args[1:]
	if len(roots) == 0 {
		home, err := os.UserHomeDir()
		if err != nil {
			fmt.Fprintf(os.Stderr, "inventory-rules: %v\n", err)
			os.Exit(1)
		}
		base := filepath.Join(home, "Library", "Application Support")
		roots = []string{
			filepath.Join(base, "kiro-cli", "autocomplete", "specs"),
			filepath.Join(base, "amazon-q", "autocomplete", "specs"),
		}
	}

	for _, root := range roots {
		if !utf8.ValidString(root) {
			fmt.Fprintln(os.Stderr, "inventory-rules: root path is not valid UTF-8")
			os.Exit(1)
		}
	}
	result := inventory.Scan(roots)
	encoded, err := encodeManifest(result)
	if err != nil {
		fmt.Fprintf(os.Stderr, "inventory-rules: %v\n", err)
		os.Exit(1)
	}
	if _, err := fmt.Println(string(encoded)); err != nil {
		fmt.Fprintf(os.Stderr, "inventory-rules: write manifest: %v\n", err)
		os.Exit(1)
	}

	for _, root := range result.Roots {
		if root.Status == "error" {
			os.Exit(1)
		}
	}
}

// JSON replaces invalid UTF-8 rather than reporting it. Refuse a lossy
// inventory so a manifest never silently names a different filesystem entry.
func encodeManifest(result inventory.Result) ([]byte, error) {
	valid := func(values ...string) bool {
		for _, value := range values {
			if !utf8.ValidString(value) {
				return false
			}
		}
		return true
	}
	for _, root := range result.Roots {
		if !valid(root.Root, root.Status, root.Error) {
			return nil, fmt.Errorf("manifest contains invalid UTF-8")
		}
		for _, file := range root.Files {
			if !valid(file.Path, file.SHA256) {
				return nil, fmt.Errorf("manifest contains invalid UTF-8")
			}
		}
		for _, path := range root.SkippedSymlinks {
			if !valid(path) {
				return nil, fmt.Errorf("manifest contains invalid UTF-8")
			}
		}
	}
	return json.MarshalIndent(result, "", "  ")
}
