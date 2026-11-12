// Package store persists local learning selections. The first version uses a
// versioned JSON file with atomic replacement instead of SQLite; the interface
// is intentionally small so a SQLite backend can replace it later.
package store

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

const (
	// SchemaVersion is the on-disk data version.
	SchemaVersion = 1
	// Retention drops selections older than 90 days.
	Retention = 90 * 24 * time.Hour
	maxFile   = 16 << 20
)

// Selection is the local count for one context/candidate key.
type Selection struct {
	Count uint64    `json:"count"`
	Last  time.Time `json:"last"`
}

type fileData struct {
	SchemaVersion int                  `json:"schemaVersion"`
	Learning      bool                 `json:"learning"`
	Selections    map[string]Selection `json:"selections"`
}

// Store owns one learning data file. It is safe for concurrent use.
type Store struct {
	mu       sync.Mutex
	dir      string
	path     string
	data     fileData
	now      func() time.Time
	fileSize int64
	fileMod  time.Time
}

// DefaultDir returns the user data directory, honoring
// OFFLINE_COMPLETE_DATA_DIR for tests and custom installs.
func DefaultDir() (string, error) {
	if value := os.Getenv("OFFLINE_COMPLETE_DATA_DIR"); value != "" {
		return value, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "Library", "Application Support", "offline-complete"), nil
}

// Open loads or creates the learning data in dir.
func Open(dir string) (*Store, error) {
	return openAt(dir, time.Now)
}

func openAt(dir string, now func() time.Time) (*Store, error) {
	if dir == "" {
		return nil, errors.New("empty data directory")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create data directory: %w", err)
	}
	info, err := os.Stat(dir)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() || info.Mode()&0o077 != 0 {
		return nil, errors.New("data directory is not private")
	}
	store := &Store{
		dir:  dir,
		path: filepath.Join(dir, "learning.json"),
		now:  now,
		data: fileData{SchemaVersion: SchemaVersion, Learning: true, Selections: map[string]Selection{}},
	}
	data, err := os.ReadFile(store.path)
	switch {
	case errors.Is(err, os.ErrNotExist):
	case err != nil:
		return nil, fmt.Errorf("read learning data: %w", err)
	default:
		parsed, err := decodeFileData(data)
		if err != nil {
			return nil, err
		}
		store.data = parsed
	}
	store.stampLocked()
	store.pruneLocked()
	return store, nil
}

func decodeFileData(data []byte) (fileData, error) {
	var result fileData
	if len(data) > maxFile {
		return result, errors.New("learning data exceeds size limit")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&result); err != nil {
		return result, fmt.Errorf("parse learning data: %w", err)
	}
	var extra json.RawMessage
	if err := decoder.Decode(&extra); err != io.EOF {
		return result, errors.New("trailing learning data")
	}
	if result.SchemaVersion != SchemaVersion {
		return result, fmt.Errorf("unsupported learning schema: %d", result.SchemaVersion)
	}
	if result.Selections == nil {
		result.Selections = map[string]Selection{}
	}
	return result, nil
}

// Learning reports whether recording and ranking are enabled.
func (s *Store) Learning() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.refreshLocked()
	return s.data.Learning
}

// SetLearning toggles recording and ranking and persists the setting.
func (s *Store) SetLearning(enabled bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.refreshLocked()
	s.data.Learning = enabled
	return s.persistLocked()
}

// Record increments one context/candidate selection count. A disabled store
// ignores the observation without touching the file.
func (s *Store) Record(key string, at time.Time) error {
	if !validKey(key) || at.IsZero() {
		return errors.New("invalid selection")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.refreshLocked()
	if !s.data.Learning {
		return nil
	}
	s.pruneLocked()
	selection := s.data.Selections[key]
	selection.Count++
	selection.Last = at.UTC()
	s.data.Selections[key] = selection
	return s.persistLocked()
}

// Lookup returns the count and last selection time for a key.
func (s *Store) Lookup(key string) (Selection, bool) {
	if !validKey(key) {
		return Selection{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.refreshLocked()
	if !s.data.Learning {
		return Selection{}, false
	}
	s.pruneLocked()
	selection, ok := s.data.Selections[key]
	return selection, ok
}

// Clear removes every selection but keeps the learning setting.
func (s *Store) Clear() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.refreshLocked()
	s.data.Selections = map[string]Selection{}
	return s.persistLocked()
}

// Stats describes the local data without exposing keys.
type Stats struct {
	Path       string `json:"path"`
	Learning   bool   `json:"learning"`
	Selections int    `json:"selections"`
	Bytes      int64  `json:"bytes"`
}

// Stats returns counts and file metadata.
func (s *Store) Stats() Stats {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.refreshLocked()
	var size int64
	if info, err := os.Stat(s.path); err == nil {
		size = info.Size()
	}
	return Stats{Path: s.path, Learning: s.data.Learning, Selections: len(s.data.Selections), Bytes: size}
}

func (s *Store) pruneLocked() {
	cutoff := s.now().Add(-Retention)
	for key, selection := range s.data.Selections {
		if selection.Last.IsZero() || selection.Last.Before(cutoff) {
			delete(s.data.Selections, key)
		}
	}
}

// refreshLocked picks up a file replaced by another process, so an external
// learning toggle or clear takes effect in running agent sessions.
func (s *Store) refreshLocked() {
	info, err := os.Stat(s.path)
	if err != nil {
		return
	}
	if info.Size() == s.fileSize && info.ModTime().Equal(s.fileMod) {
		return
	}
	data, err := os.ReadFile(s.path)
	if err != nil {
		return
	}
	parsed, err := decodeFileData(data)
	if err != nil {
		return
	}
	s.data = parsed
	s.fileSize = info.Size()
	s.fileMod = info.ModTime()
	s.pruneLocked()
}

func (s *Store) stampLocked() {
	if info, err := os.Stat(s.path); err == nil {
		s.fileSize = info.Size()
		s.fileMod = info.ModTime()
		return
	}
	s.fileSize = 0
	s.fileMod = time.Time{}
}

func (s *Store) persistLocked() error {
	data, err := json.MarshalIndent(s.data, "", "  ")
	if err != nil {
		return err
	}
	temp, err := os.CreateTemp(s.dir, ".learning-*.tmp")
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
	if err := os.Rename(name, s.path); err != nil {
		return err
	}
	s.stampLocked()
	return nil
}

func validKey(key string) bool {
	return key != "" && len(key) <= 4096 && utf8.ValidString(key) && !strings.ContainsAny(key, "\r\n")
}
