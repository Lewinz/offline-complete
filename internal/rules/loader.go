package rules

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"strings"
)

// Source records provenance provided by the trusted rule pack manifest. It does
// not itself establish that a license permits redistribution.
type Source struct {
	Repository string
	Revision   string
	License    string
}

// Entry identifies a single-command rule file and its expected SHA-256.
type Entry struct {
	Path   string
	SHA256 string
	Source Source
}

// Loader reads files from an application-controlled, local rule filesystem.
// The caller must authenticate the manifest and filesystem; a hash from an
// untrusted manifest cannot establish authenticity.
type Loader struct {
	files   fs.FS
	entries map[string]Entry
}

// NewLoader validates and copies the trusted manifest without reading rules.
func NewLoader(files fs.FS, entries map[string]Entry) (*Loader, error) {
	if files == nil {
		return nil, errors.New("nil rule filesystem")
	}
	copyEntries := make(map[string]Entry, len(entries))
	for command, entry := range entries {
		if command == "" || strings.ContainsAny(command, "/\\\x00") || command == "." || command == ".." {
			return nil, fmt.Errorf("invalid command identifier %q", command)
		}
		if !fs.ValidPath(entry.Path) || entry.Path == "." || strings.Contains(entry.Path, "\\") {
			return nil, fmt.Errorf("invalid rule path for %q", command)
		}
		if len(entry.SHA256) != sha256.Size*2 {
			return nil, fmt.Errorf("invalid rule hash for %q", command)
		}
		if _, err := hex.DecodeString(entry.SHA256); err != nil {
			return nil, fmt.Errorf("invalid rule hash for %q", command)
		}
		if strings.TrimSpace(entry.Source.Repository) == "" || strings.TrimSpace(entry.Source.Revision) == "" || strings.TrimSpace(entry.Source.License) == "" {
			return nil, fmt.Errorf("missing rule source for %q", command)
		}
		copyEntries[command] = entry
	}
	return &Loader{files: files, entries: copyEntries}, nil
}

// Load reads and verifies only the requested command. No filesystem path is
// derived from the request, and no failure triggers a network fallback.
func (l *Loader) Load(command string) (*RulePack, error) {
	entry, ok := l.entries[command]
	if !ok {
		return nil, fmt.Errorf("rule %q: %w", command, fs.ErrNotExist)
	}
	file, err := l.files.Open(entry.Path)
	if err != nil {
		return nil, fmt.Errorf("open rule %q: %w", command, err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("stat rule %q: %w", command, err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("rule %q is not a regular file", command)
	}
	if info.Size() > maxRuleBytes {
		return nil, fmt.Errorf("rule %q exceeds size limit", command)
	}
	data, err := io.ReadAll(io.LimitReader(file, maxRuleBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read rule %q: %w", command, err)
	}
	if len(data) > maxRuleBytes {
		return nil, fmt.Errorf("rule %q exceeds size limit", command)
	}
	hash := sha256.Sum256(data)
	if !strings.EqualFold(hex.EncodeToString(hash[:]), entry.SHA256) {
		return nil, fmt.Errorf("rule %q hash mismatch", command)
	}
	pack, err := FromJSON(data)
	if err != nil {
		return nil, fmt.Errorf("parse rule %q: %w", command, err)
	}
	if len(pack.Commands) != 1 || pack.Commands[0].Name != command {
		return nil, fmt.Errorf("rule %q contains a different command", command)
	}
	pack.Source = entry.Source
	return pack, nil
}
