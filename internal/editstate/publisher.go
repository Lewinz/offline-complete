package editstate

import (
	"errors"
	"io"
	"math"
	"net"
	"strings"
	"sync"
	"time"
)

// Publisher serializes editing messages on an already authenticated and
// handshaken connection. It owns the connection and its write deadline. It
// does not retain snapshots or authenticate the supplied session ID.
type Publisher struct {
	writeGate chan struct{}
	closedCh  chan struct{}
	mu        sync.Mutex
	closeOnce sync.Once
	conn      net.Conn
	session   string
	timeout   time.Duration
	sequence  uint64
	closed    bool
	closeErr  error
}

// NewPublisher takes ownership of conn only on success. The caller must ensure
// the connection was authenticated and handshaken for session beforehand.
func NewPublisher(conn net.Conn, session string, timeout time.Duration) (*Publisher, error) {
	if conn == nil || timeout <= 0 || WriteClear(io.Discard, session, 1) != nil || WriteUpdate(io.Discard, session, 1, "", 0) != nil {
		return nil, errors.New("invalid editing publisher parameters")
	}
	p := &Publisher{conn: conn, session: strings.Clone(session), timeout: timeout, writeGate: make(chan struct{}, 1), closedCh: make(chan struct{})}
	p.writeGate <- struct{}{}
	return p, nil
}

// Update sends a complete snapshot; any failure closes the stream so the
// receiver can invalidate its previously published state.
func (p *Publisher) Update(line string, cursor int) error {
	return p.send(func(seq uint64, session string) error {
		return WriteUpdateConn(p.conn, session, seq, line, cursor, p.timeout)
	})
}

// Clear sends a versioned invalidation.
func (p *Publisher) Clear() error {
	return p.send(func(seq uint64, session string) error {
		return WriteClearConn(p.conn, session, seq, p.timeout)
	})
}

func (p *Publisher) send(write func(uint64, string) error) error {
	select {
	case <-p.closedCh:
		return errors.New("editing publisher closed")
	case <-p.writeGate:
	}
	defer func() { p.writeGate <- struct{}{} }()
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return errors.New("editing publisher closed")
	}
	if p.sequence == math.MaxUint64 {
		p.mu.Unlock()
		return errors.Join(errors.New("editing sequence exhausted"), p.Close())
	}
	p.sequence++
	sequence, session := p.sequence, p.session
	p.mu.Unlock()
	if err := write(sequence, session); err != nil {
		return errors.Join(err, p.Close())
	}
	return nil
}

// Close closes the connection once, interrupting an active write when the
// underlying connection implements the net.Conn close contract.
func (p *Publisher) Close() error {
	p.closeOnce.Do(func() {
		p.mu.Lock()
		p.closed = true
		p.session = ""
		close(p.closedCh)
		p.mu.Unlock()
		p.closeErr = p.conn.Close()
	})
	return p.closeErr
}
