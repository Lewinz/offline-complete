package agent

import (
	"bufio"
	"errors"
	"io"
	"net"
	"sync"
	"time"

	"offline-complete/internal/diag"
	"offline-complete/internal/editstate"
)

const uiWriteTimeout = 2 * time.Second

// Navigation is a keyboard action taken while an offer is active.
type Navigation uint8

const (
	NavigationNext Navigation = iota + 1
	NavigationPrevious
	NavigationAccept
	NavigationInsert
	NavigationDismiss
)

// Service bridges one editing stream to one interface connection. Offers are
// pushed after each accepted edit; the interface can focus, select or dismiss.
// Writes are coalesced and bounded so a slow or stalled interface never blocks
// the shell's editing callbacks.
type Service struct {
	session *Session
	state   *editstate.State
	conn    net.Conn
	Logger  *diag.Logger

	out       chan []byte
	done      chan struct{}
	closeOnce sync.Once

	mu                sync.Mutex
	focused           bool
	lastVersion       uint64
	dismissedVersion  uint64
	offerVersion      uint64
	offerID           uint64
	selected          int
	count             int
	active            bool
	exactMatchExecute bool
	suppressUntil     time.Time
	haveContent       bool
	lastLine          string
	lastCursor        int
	lastCWD           string
	offerLine         string
	offerCursor       int
	offerCWD          string
}

// NewService starts the interface writer. The connection must be authenticated
// by the caller; this type does not verify peers or paths.
func NewService(session *Session, state *editstate.State, conn net.Conn) (*Service, error) {
	if session == nil || state == nil || conn == nil {
		return nil, errors.New("invalid completion service")
	}
	service := &Service{
		session:           session,
		state:             state,
		conn:              conn,
		out:               make(chan []byte, 1),
		done:              make(chan struct{}),
		focused:           true,
		exactMatchExecute: true,
	}
	go service.sendLoop()
	return service, nil
}

// Refresh recomputes candidates for the latest editing state and pushes an
// offer or a clear message. It is intended to run after each accepted editing
// record. A repeated version is ignored.
func (s *Service) Refresh() { s.refresh(false) }

func (s *Service) refresh(force bool) {
	version := s.state.Version()
	snapshot, present := s.state.Snapshot()
	s.mu.Lock()
	if time.Now().Before(s.suppressUntil) {
		s.lastVersion = version
		s.haveContent = present
		s.lastLine = snapshot.Line
		s.lastCursor = snapshot.Cursor
		s.lastCWD = snapshot.CWD
		s.mu.Unlock()
		s.enqueue(ClearMessage{Type: "clear", Version: version})
		return
	}
	if !force && version == s.lastVersion && version != 0 {
		s.mu.Unlock()
		return
	}
	// ZLE can redraw for an unchanged buffer. Recomputing and resending the
	// offer would reset the selected row, so keep the current UI state.
	if !force && s.haveContent && present && s.lastLine == snapshot.Line &&
		s.lastCursor == snapshot.Cursor && s.lastCWD == snapshot.CWD {
		s.lastVersion = version
		s.mu.Unlock()
		return
	}
	focused := s.focused
	s.lastVersion = version
	s.haveContent = present
	s.lastLine = snapshot.Line
	s.lastCursor = snapshot.Cursor
	s.lastCWD = snapshot.CWD
	s.mu.Unlock()

	offer, offerID, ok := s.session.Refresh(focused)
	if s.state.Version() != version {
		// The shell moved on while the generator ran. Do not show a stale
		// offer; the queued refresh will publish the newest snapshot.
		return
	}
	if !ok {
		s.mu.Lock()
		s.active = false
		s.count = 0
		s.mu.Unlock()
		s.enqueue(ClearMessage{Type: "clear", Version: version})
		return
	}
	s.mu.Lock()
	if s.dismissedVersion != 0 && version > s.dismissedVersion {
		s.dismissedVersion = 0
	}
	if version == s.dismissedVersion {
		s.active = false
		s.count = 0
		s.mu.Unlock()
		return
	}
	s.offerVersion = version
	s.offerID = offerID
	s.selected = 0
	s.count = len(offer.Candidates)
	s.offerLine = snapshot.Line
	s.offerCursor = snapshot.Cursor
	s.offerCWD = snapshot.CWD
	s.active = true
	s.mu.Unlock()

	items := make([]Item, len(offer.Candidates))
	for i, candidate := range offer.Candidates {
		items[i] = Item{Name: candidate.Name, Description: candidate.Description, Path: candidate.Path}
	}
	s.enqueue(OfferMessage{Type: "offer", Version: version, Offer: offerID, Selected: 0, Items: items})
}

func (s *Service) suppressAfterSelect() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.suppressUntil = time.Now().Add(200 * time.Millisecond)
}

// HasOffer reports whether a candidate list is currently active.
func (s *Service) HasOffer() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.active
}

// Navigate applies a keyboard action and reports whether it consumed the key.
// It returns false when no offer is active, so the caller forwards the key to
// the shell unchanged.
func (s *Service) Navigate(action Navigation) bool {
	s.mu.Lock()
	if !s.active || s.count == 0 {
		s.mu.Unlock()
		return false
	}
	// An edit can change the line between refreshes. Do not consume a key
	// (especially Enter) for an offer that no longer describes the line. The
	// shell sequence may advance for an unchanged redraw, so compare content.
	snapshot, present := s.state.Snapshot()
	if !present || snapshot.Line != s.offerLine || snapshot.Cursor != s.offerCursor || snapshot.CWD != s.offerCWD {
		current := s.state.Version()
		s.active = false
		s.count = 0
		s.mu.Unlock()
		s.enqueue(ClearMessage{Type: "clear", Version: current})
		return false
	}
	switch action {
	case NavigationNext, NavigationPrevious:
		if action == NavigationNext {
			s.selected = (s.selected + 1) % s.count
		} else {
			s.selected = (s.selected - 1 + s.count) % s.count
		}
		message := SelectionMessage{
			Type:    "selection",
			Version: s.offerVersion,
			Offer:   s.offerID,
			Index:   s.selected,
		}
		s.mu.Unlock()
		s.enqueue(message)
		return true
	case NavigationAccept, NavigationInsert:
		version, offer, index := s.offerVersion, s.offerID, s.selected
		if action == NavigationAccept && s.exactMatchExecute && s.session.ExactMatch(index) {
			// Kiro's "insert selected and execute" behavior: the selected
			// candidate only adds a slash or repeats the token, so execute the
			// line the user already typed instead of replacing it.
			current := s.state.Version()
			s.active = false
			s.count = 0
			s.mu.Unlock()
			s.dropPending()
			s.suppressAfterSelect()
			s.enqueue(ClearMessage{Type: "clear", Version: current})
			return false
		}
		s.active = false
		s.count = 0
		s.mu.Unlock()
		s.dropPending()
		err := s.session.Select(version, offer, index)
		if err != nil {
			s.Logger.Printf("event=select_error version=%d offer=%d index=%d err=%q", version, offer, index, err)
		} else {
			s.suppressAfterSelect()
		}
		result := ResultMessage{Type: "result", Version: version, Offer: offer, OK: err == nil}
		if err != nil {
			result.Error = "selection rejected"
		}
		s.enqueue(result)
		return true
	case NavigationDismiss:
		s.active = false
		s.count = 0
		s.dismissedVersion = s.offerVersion
		version := s.offerVersion
		s.mu.Unlock()
		s.dropPending()
		s.enqueue(ClearMessage{Type: "clear", Version: version})
		return true
	default:
		s.mu.Unlock()
		return false
	}
}

// Serve reads interface commands until EOF, an invalid message, or Close.
func (s *Service) Serve() error {
	scanner := bufio.NewScanner(s.conn)
	scanner.Buffer(make([]byte, 4096), MaxMessage)
	for scanner.Scan() {
		select {
		case <-s.done:
			return io.EOF
		default:
		}
		parsed, err := parseCommand(scanner.Bytes())
		if err != nil {
			return err
		}
		switch parsed.kind {
		case commandFocus:
			s.setFocus(parsed.focused)
		case commandSelect:
			s.dropPending()
			err := s.session.Select(parsed.version, parsed.offer, parsed.index)
			if err != nil {
				s.Logger.Printf("event=select_error version=%d offer=%d index=%d err=%q", parsed.version, parsed.offer, parsed.index, err)
			} else {
				s.suppressAfterSelect()
			}
			s.mu.Lock()
			s.active = false
			s.count = 0
			s.mu.Unlock()
			result := ResultMessage{Type: "result", Version: parsed.version, Offer: parsed.offer, OK: err == nil}
			if err != nil {
				result.Error = "selection rejected"
			}
			s.enqueue(result)
		case commandDismiss:
			// The interface hides its own list; the next edit refreshes it.
		}
	}
	if err := scanner.Err(); err != nil {
		if !errors.Is(err, net.ErrClosed) {
			s.Logger.Printf("event=interface_read_error err=%q", err)
		}
		return err
	}
	return io.EOF
}

// SetExactMatchExecute controls whether an exact match passes Enter through to
// the shell instead of replacing the current token.
func (s *Service) SetExactMatchExecute(enabled bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.exactMatchExecute = enabled
}

func (s *Service) setFocus(focused bool) {
	s.mu.Lock()
	s.focused = focused
	s.mu.Unlock()
	s.Logger.Printf("event=focus focused=%t", focused)
	s.refresh(true)
}

// Close invalidates offers and closes the interface connection. It does not
// terminate the shell or the editing stream.
func (s *Service) Close() error {
	s.closeOnce.Do(func() {
		close(s.done)
		s.session.Close()
		_ = s.conn.Close()
	})
	return nil
}

// dropPending discards queued messages so an accepted or dismissed offer
// cannot be delivered after its result. An in-flight write cannot be recalled.
func (s *Service) dropPending() {
	for {
		select {
		case <-s.out:
		default:
			return
		}
	}
}

func (s *Service) enqueue(message any) {
	data, err := encodeMessage(message)
	if err != nil {
		return
	}
	select {
	case s.out <- data:
	default:
		select {
		case <-s.out:
		default:
		}
		select {
		case s.out <- data:
		default:
		}
	}
}

func (s *Service) sendLoop() {
	for {
		select {
		case data := <-s.out:
			_ = s.conn.SetWriteDeadline(time.Now().Add(uiWriteTimeout))
			_, err := s.conn.Write(append(data, '\n'))
			_ = s.conn.SetWriteDeadline(time.Time{})
			if err != nil {
				_ = s.Close()
				return
			}
		case <-s.done:
			return
		}
	}
}
