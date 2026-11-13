// Package history reads a bounded local shell history for completion
// candidates. It never writes or sends history anywhere.
package history

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"

	"offline-complete/internal/rules"
)

const maxHistoryBytes = 8 << 20

// Store contains at most the most recent few thousand history lines.
type Store struct {
	Path    string
	Entries []string
}

// Load reads path, or the default zsh history file when path is empty. Missing
// or unreadable files produce an empty store.
func Load(path string) *Store {
	if path == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return &Store{}
		}
		path = filepath.Join(home, ".zsh_history")
	}
	store := &Store{Path: path}
	file, err := os.Open(path)
	if err != nil {
		return store
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxHistoryBytes {
		return store
	}
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 256*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, ": ") {
			if index := strings.IndexByte(line, ';'); index >= 0 {
				line = line[index+1:]
			}
		}
		if line == "" || !utf8.ValidString(line) || strings.IndexFunc(line, unicode.IsControl) >= 0 {
			continue
		}
		store.Entries = append(store.Entries, line)
		if len(store.Entries) > 4000 {
			store.Entries = store.Entries[len(store.Entries)-4000:]
		}
	}
	return store
}

// Candidates returns recent unique tokens that start with prefix. Empty
// prefixes produce no history noise.
// traversalOnly matches tokens such as "..", "../" and "...": they repeat
// what the user already typed and should not become selectable suggestions.
func traversalOnly(token string) bool {
	return strings.Contains(token, ".") && strings.Trim(token, "./") == ""
}

func (s *Store) Candidates(prefix string, limit int) []rules.Candidate {
	if s == nil || prefix == "" || limit <= 0 {
		return nil
	}
	seen := make(map[string]bool)
	result := make([]rules.Candidate, 0, limit)
	foldedPrefix := strings.ToLower(prefix)
	for index := len(s.Entries) - 1; index >= 0; index-- {
		for _, token := range strings.Fields(s.Entries[index]) {
			if traversalOnly(token) || !strings.HasPrefix(strings.ToLower(token), foldedPrefix) || seen[token] {
				continue
			}
			seen[token] = true
			result = append(result, rules.Candidate{Name: token, Description: "历史"})
			if len(result) >= limit {
				return result
			}
		}
	}
	return result
}
