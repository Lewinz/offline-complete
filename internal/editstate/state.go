// Package editstate holds a bounded, in-memory editing snapshot. It does not
// authenticate its caller or connect to a shell or transport.
package editstate

import (
	"errors"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"
)

// Snapshot is one editing state; Cursor is a UTF-8 byte offset.
type Snapshot struct {
	Version uint64
	Line    string
	Cursor  int
	// CWD is the shell's current directory at the time of the snapshot. It is
	// empty when the transport does not provide a working directory.
	CWD string
}

// State stores the latest snapshot for one caller-owned session. Its zero value
// is ready to use. Clearing drops references but does not securely erase memory.
type State struct {
	mu      sync.Mutex
	version uint64
	current Snapshot
	present bool
	closed  bool
}

// Update accepts a newer version and replaces the complete editing snapshot.
// An invalid newer update clears the snapshot and advances the version; stale
// updates never change the current state.
func (s *State) Update(version uint64, line string, cursor int) error {
	return s.UpdateWithCWD(version, line, cursor, "")
}

// UpdateWithCWD replaces the complete snapshot and records the shell's current
// directory. The directory is advisory metadata for local generators; it is
// never used as a path without the caller's own validation.
func (s *State) UpdateWithCWD(version uint64, line string, cursor int, cwd string) error {
	invalid := len(line) > 64*1024 || !utf8.ValidString(line) || cursor < 0 || cursor > len(line) || (cursor < len(line) && !utf8.RuneStart(line[cursor]))
	if len(cwd) > 4096 || !utf8.ValidString(cwd) || strings.IndexFunc(cwd, unicode.IsControl) >= 0 {
		invalid = true
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return errors.New("editing state closed")
	}
	if version <= s.version {
		return errors.New("stale editing version")
	}
	s.version = version
	if invalid {
		s.current = Snapshot{}
		s.present = false
		return errors.New("invalid editing snapshot")
	}
	s.current = Snapshot{Version: version, Line: strings.Clone(line), Cursor: cursor, CWD: strings.Clone(cwd)}
	s.present = true
	return nil
}

// Clear invalidates the snapshot and advances the version. A delayed update
// from before the clear cannot restore the old editing state.
func (s *State) Clear(version uint64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return errors.New("editing state closed")
	}
	if version <= s.version {
		return errors.New("stale editing version")
	}
	s.version = version
	s.current = Snapshot{}
	s.present = false
	return nil
}

// Close irreversibly drops the current snapshot and rejects future updates.
// Call it when the owning session ends, even if the version is exhausted.
// Previously returned snapshots and historical memory are not erased.
func (s *State) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	s.current = Snapshot{}
	s.present = false
}

// Snapshot returns the current state, or a zero value if none is present.
func (s *State) Snapshot() (Snapshot, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.current, s.present
}

// Version returns the latest accepted version, including after a clear or an
// invalid update. Callers use it to invalidate UI state without reading the
// editing buffer.
func (s *State) Version() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.version
}
