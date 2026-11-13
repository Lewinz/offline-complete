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
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"offline-complete/internal/learning"
)

const (
	// SchemaVersion is the on-disk data version.
	SchemaVersion = 2
	// Retention drops observations older than 90 days.
	Retention = 90 * 24 * time.Hour
	maxFile   = 16 << 20

	// Personal rules need repeated evidence before they add candidates.
	MinPersonalWins = 3
	MinPersonalDays = 2
)

// Selection is the local count for one context/candidate key.
type Selection struct {
	Count uint64    `json:"count"`
	Last  time.Time `json:"last"`
}

// Correction records evidence that the user replaced a generated/static
// candidate with another value in one completion context. A correction becomes
// an additional candidate after enough independent observations.
type Correction struct {
	Wins   uint64    `json:"wins"`
	Losses uint64    `json:"losses"`
	Days   []string  `json:"days,omitempty"`
	Last   time.Time `json:"last"`
}

// PersonalCandidate is one learned value ready to be merged into a candidate
// list. Confidence is a Wilson lower bound, not a raw win rate.
type PersonalCandidate struct {
	Value      string
	Wins       uint64
	Losses     uint64
	Confidence float64
	Last       time.Time
}

type fileData struct {
	SchemaVersion int                              `json:"schemaVersion"`
	ExportedAt    time.Time                        `json:"exportedAt,omitempty"`
	Learning      bool                             `json:"learning"`
	Selections    map[string]Selection             `json:"selections"`
	Corrections   map[string]map[string]Correction `json:"corrections,omitempty"`
}

type exportData struct {
	SchemaVersion int                              `json:"schemaVersion"`
	ExportedAt    time.Time                        `json:"exportedAt"`
	Learning      bool                             `json:"learning"`
	Selections    map[string]Selection             `json:"selections"`
	Corrections   map[string]map[string]Correction `json:"corrections,omitempty"`
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
	return filepath.Join(home, ".oc"), nil
}

// Open loads or creates the learning data in dir.
func Open(dir string) (*Store, error) {
	if err := migrateLegacy(dir); err != nil {
		return nil, err
	}
	return openAt(dir, time.Now)
}

// migrateLegacy copies the pre-2.0 learning file from Application Support
// once. It never deletes the old file, so a failed migration is recoverable.
func migrateLegacy(dir string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	if dir != filepath.Join(home, ".oc") {
		return nil
	}
	path := filepath.Join(dir, "learning.json")
	if _, err := os.Stat(path); err == nil || !errors.Is(err, os.ErrNotExist) {
		return nil
	}
	legacy := filepath.Join(home, "Library", "Application Support", "offline-complete", "learning.json")
	data, err := os.ReadFile(legacy)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read legacy learning data: %w", err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create learning directory: %w", err)
	}
	temp, err := os.CreateTemp(dir, ".learning-migrate-*.tmp")
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
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
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
		data: fileData{SchemaVersion: SchemaVersion, Learning: true, Selections: map[string]Selection{}, Corrections: map[string]map[string]Correction{}},
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
	if result.SchemaVersion < 1 || result.SchemaVersion > SchemaVersion {
		return result, fmt.Errorf("unsupported learning schema: %d", result.SchemaVersion)
	}
	if result.Selections == nil {
		result.Selections = map[string]Selection{}
	}
	if result.Corrections == nil {
		result.Corrections = map[string]map[string]Correction{}
	}
	result.SchemaVersion = SchemaVersion
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

// RecordCorrection records that the user replaced rejected with candidate in
// one context. A correction is also evidence for a personal candidate even
// when no static/generator candidate was offered.
func (s *Store) RecordCorrection(contextKey, candidate, rejected string, at time.Time) error {
	if !validKey(contextKey) || !validKey(candidate) || at.IsZero() {
		return errors.New("invalid correction")
	}
	if isParentValue(candidate) {
		return nil
	}
	if rejected != "" && !validKey(rejected) {
		return errors.New("invalid rejected candidate")
	}
	if isParentValue(rejected) {
		rejected = ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.refreshLocked()
	if !s.data.Learning {
		return nil
	}
	s.pruneLocked()
	context := s.data.Corrections[contextKey]
	if context == nil {
		context = map[string]Correction{}
		s.data.Corrections[contextKey] = context
	}
	entry := context[candidate]
	entry.Wins++
	entry.Last = at.UTC()
	entry.Days = appendDay(entry.Days, at.UTC().Format("2006-01-02"))
	context[candidate] = entry
	if rejected != "" && rejected != candidate {
		loser := context[rejected]
		loser.Losses++
		loser.Last = at.UTC()
		context[rejected] = loser
	}
	return s.persistLocked()
}

// Correction returns evidence for one context/candidate pair.
func (s *Store) Correction(contextKey, candidate string) (Correction, bool) {
	if !validKey(contextKey) || !validKey(candidate) {
		return Correction{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.refreshLocked()
	if !s.data.Learning {
		return Correction{}, false
	}
	context := s.data.Corrections[contextKey]
	entry, ok := context[candidate]
	return entry, ok
}

// PersonalCandidates returns learned values that have crossed the evidence
// threshold and match prefix. Results are ordered by confidence.
func (s *Store) PersonalCandidates(contextKey, prefix string, limit int) []PersonalCandidate {
	if !validKey(contextKey) || limit <= 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.refreshLocked()
	if !s.data.Learning {
		return nil
	}
	folded := strings.ToLower(prefix)
	result := make([]PersonalCandidate, 0, limit)
	for value, entry := range s.data.Corrections[contextKey] {
		if !validKey(value) || isParentValue(value) || entry.Wins < MinPersonalWins || len(entry.Days) < MinPersonalDays {
			continue
		}
		if !strings.HasPrefix(strings.ToLower(value), folded) {
			continue
		}
		result = append(result, PersonalCandidate{
			Value:      value,
			Wins:       entry.Wins,
			Losses:     entry.Losses,
			Confidence: learning.WilsonLower(entry.Wins, entry.Losses, 1.645),
			Last:       entry.Last,
		})
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Confidence != result[j].Confidence {
			return result[i].Confidence > result[j].Confidence
		}
		if result[i].Wins != result[j].Wins {
			return result[i].Wins > result[j].Wins
		}
		return result[i].Last.After(result[j].Last)
	})
	if len(result) > limit {
		result = result[:limit]
	}
	return result
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
	s.data.Corrections = map[string]map[string]Correction{}
	return s.persistLocked()
}

// Stats describes the local data without exposing keys.
type Stats struct {
	Path        string `json:"path"`
	Learning    bool   `json:"learning"`
	Selections  int    `json:"selections"`
	Corrections int    `json:"corrections"`
	Bytes       int64  `json:"bytes"`
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
	return Stats{Path: s.path, Learning: s.data.Learning, Selections: len(s.data.Selections), Corrections: len(s.data.Corrections), Bytes: size}
}

func (s *Store) pruneLocked() {
	cutoff := s.now().Add(-Retention)
	for key, selection := range s.data.Selections {
		if selection.Last.IsZero() || selection.Last.Before(cutoff) {
			delete(s.data.Selections, key)
		}
	}
	for context, values := range s.data.Corrections {
		for value, correction := range values {
			if correction.Last.IsZero() || correction.Last.Before(cutoff) {
				delete(values, value)
			}
		}
		if len(values) == 0 {
			delete(s.data.Corrections, context)
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

// Export returns a portable snapshot. The output includes the learning toggle,
// selections and personal corrections so another machine can restore all
// learned state.
func (s *Store) Export() ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.refreshLocked()
	snapshot := exportData{
		SchemaVersion: SchemaVersion,
		ExportedAt:    s.now().UTC(),
		Learning:      s.data.Learning,
		Selections:    s.data.Selections,
		Corrections:   s.data.Corrections,
	}
	data, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

// Import replaces all learned state from an export or a learning.json file.
// Callers should make a backup before calling this for an interactive import.
func (s *Store) Import(data []byte) error {
	if len(data) == 0 || len(data) > maxFile {
		return errors.New("learning import has invalid size")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var result fileData
	if err := decoder.Decode(&result); err != nil {
		return fmt.Errorf("parse learning import: %w", err)
	}
	var extra json.RawMessage
	if err := decoder.Decode(&extra); err != io.EOF {
		return errors.New("trailing learning import data")
	}
	if result.SchemaVersion < 1 || result.SchemaVersion > SchemaVersion {
		return fmt.Errorf("unsupported learning schema: %d", result.SchemaVersion)
	}
	if result.Selections == nil {
		result.Selections = map[string]Selection{}
	}
	for key := range result.Selections {
		if !validKey(key) {
			return errors.New("invalid selection key in import")
		}
	}
	if result.Corrections == nil {
		result.Corrections = map[string]map[string]Correction{}
	}
	for context, values := range result.Corrections {
		if !validKey(context) {
			return errors.New("invalid correction context in import")
		}
		for value, correction := range values {
			if !validKey(value) || correction.Last.IsZero() {
				return errors.New("invalid correction in import")
			}
		}
	}
	result.SchemaVersion = SchemaVersion
	result.ExportedAt = time.Time{}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data = result
	s.pruneLocked()
	return s.persistLocked()
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

func appendDay(days []string, day string) []string {
	for _, existing := range days {
		if existing == day {
			return days
		}
	}
	days = append(days, day)
	if len(days) > 7 {
		days = days[len(days)-7:]
	}
	return days
}

func isParentValue(value string) bool {
	return strings.TrimSuffix(strings.TrimSpace(value), "/") == ".."
}

func validKey(key string) bool {
	return key != "" && len(key) <= 4096 && utf8.ValidString(key) && !strings.ContainsAny(key, "\r\n")
}
