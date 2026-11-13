package agent

import (
	"errors"
	"io"
	"strings"
	"sync"
	"time"

	"offline-complete/internal/completion"
	"offline-complete/internal/editstate"
	"offline-complete/internal/shellparse"
)

// Recorder receives one explicit selection. Implementations must not block the
// shell for long and must not persist free text or argument values.
type Recorder interface {
	Record(shellparse.Context, string)
}

// Session coordinates one editing session: it computes an offer for the
// current snapshot, gates selection, and writes the resulting line replacement
// to the shell. It does not authenticate peers or decide when the terminal has
// focus; callers must supply trustworthy focus/version values and close the
// session when the shell exits.
type Session struct {
	session   string
	state     *editstate.State
	guard     *completion.Guard
	completer *Completer
	writer    io.Writer
	recorder  Recorder

	mu            sync.Mutex
	snapshot      editstate.Snapshot
	offer         Offer
	offerID       uint64
	hasOffer      bool
	lastVersion   uint64
	haveVersion   bool
	lastSelection time.Time
}

// NewSession binds a shell writer, its editing state and a completer. The
// writer must be the session's PTY input; no other goroutine may write a line
// replacement concurrently.
func NewSession(session string, state *editstate.State, completer *Completer, writer io.Writer) (*Session, error) {
	if session == "" || state == nil || completer == nil || writer == nil {
		return nil, errors.New("invalid completion session")
	}
	return &Session{
		session:   session,
		state:     state,
		guard:     completion.NewGuard(session),
		completer: completer,
		writer:    writer,
	}, nil
}

// Refresh recomputes candidates for the latest snapshot. It returns false when
// the context is uncertain, there are no candidates, the state was cleared or
// the session is unfocused. Each accepted refresh invalidates older offers.
// Focus changes at the same editing version are allowed: losing focus drops the
// offer, and regaining it can re-offer the same snapshot.
func (s *Session) Refresh(focused bool) (Offer, uint64, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	version := s.state.Version()
	if s.haveVersion && version == s.lastVersion {
		s.guard.SetFocused(focused)
	} else {
		s.guard.Update(version, focused)
		s.lastVersion = version
		s.haveVersion = true
	}
	s.hasOffer = false
	s.offer = Offer{}
	s.offerID = 0
	if !focused {
		return Offer{}, 0, false
	}
	snapshot, present := s.state.Snapshot()
	if !present {
		return Offer{}, 0, false
	}
	offer, ok := s.completer.Complete(snapshot)
	if !ok {
		return Offer{}, 0, false
	}
	names := make([]string, len(offer.Candidates))
	for i, candidate := range offer.Candidates {
		names[i] = candidate.Name
	}
	offerID, err := s.guard.Offer(snapshot.Version, names)
	if err != nil {
		return Offer{}, 0, false
	}
	s.snapshot = snapshot
	s.offer = offer
	s.offerID = offerID
	s.hasOffer = true
	return offer, offerID, true
}

// LastOffer returns the most recent candidate list. It is used to attribute a
// manual submission to the candidate the user chose not to accept.
func (s *Session) LastOffer() (Offer, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.hasOffer {
		return Offer{}, false
	}
	return s.offer, true
}

// Select applies one explicit candidate to the current offer and writes the
// replacement through the PTY. The offer is consumed even if writing fails;
// callers must refresh after any error.
// SetRecorder installs a local selection recorder. It must be called before
// selections are accepted.
func (s *Session) SetRecorder(recorder Recorder) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.recorder = recorder
}

// ExactMatch reports whether the selected candidate adds nothing beyond the
// current token. Kiro treats this case as "insert selected and execute": the
// shell should receive Enter instead of replacing the word. Keeping the check
// here means the service can pass Enter through without duplicating the
// shell's path semantics.
func (s *Session) ExactMatch(index int) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.hasOffer || index < 0 || index >= len(s.offer.Candidates) {
		return false
	}
	prefix := s.offer.Context.Prefix
	name := s.offer.Candidates[index].Name
	if strings.EqualFold(name, prefix) {
		return true
	}
	return strings.HasSuffix(name, "/") && strings.EqualFold(strings.TrimSuffix(name, "/"), prefix)
}

func (s *Session) Select(version, offer uint64, index int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.hasOffer || s.offer.Version != version || s.offerID != offer {
		s.logf("event=select_rejected reason=stale version=%d offer=%d index=%d", version, offer, index)
		return errors.New("stale candidate offer")
	}
	candidate, ok := s.guard.Select(s.session, version, offer, index)
	if !ok {
		s.logf("event=select_rejected reason=guard version=%d offer=%d index=%d", version, offer, index)
		return errors.New("candidate selection rejected")
	}
	s.hasOffer = false
	insert := candidate
	if index >= 0 && index < len(s.offer.Candidates) && s.offer.Candidates[index].InsertValue != "" {
		insert = s.offer.Candidates[index].InsertValue
	}
	edited, err := Apply(s.snapshot, insert)
	if err != nil {
		s.logf("event=select_rejected reason=apply candidate=%q err=%q", candidate, err)
		return err
	}
	if err := WriteLineReplacement(s.writer, edited.Line, edited.Cursor); err != nil {
		s.logf("event=select_rejected reason=write candidate=%q err=%q", candidate, err)
		return err
	}
	s.lastSelection = time.Now()
	if s.recorder != nil {
		s.recorder.Record(s.offer.Context, candidate)
	}
	if s.completer.Logger != nil && s.completer.Logger.Text() {
		s.logf("event=select_applied command=%q subcommands=%q candidate=%q", s.offer.Context.Command, strings.Join(s.offer.Context.Subcommands, "/"), candidate)
	} else {
		s.logf("event=select_applied command=%q subcommands=%q candidate_len=%d", s.offer.Context.Command, strings.Join(s.offer.Context.Subcommands, "/"), len(candidate))
	}
	return nil
}

// LastSelection reports when a candidate was last written into the shell.
func (s *Session) LastSelection() time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastSelection
}

func (s *Session) logf(format string, args ...any) {
	if s != nil && s.completer != nil {
		s.completer.Logger.Printf(format, args...)
	}
}

// Close permanently invalidates outstanding offers.
func (s *Session) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.hasOffer = false
	s.offer = Offer{}
	s.offerID = 0
	s.guard.Close()
}
