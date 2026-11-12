package editstate

import (
	"context"
	"errors"
	"io"
	"net"
	"strings"
	"sync"
	"time"
)

// Receiver publishes editing state from at most one authenticated connection.
// Authentication and session handshake must be completed by the caller before
// Consume is invoked. A new connection is rejected while another is active.
// Snapshots are transient and are not securely erased from memory.
type Receiver struct {
	mu            sync.Mutex
	session       string
	timeout       time.Duration
	active        *State
	activeContext context.Context
}

// NewReceiver creates a receiver for an already-authenticated session. The
// timeout bounds each frame, including the wait for its first byte.
func NewReceiver(session string, timeout time.Duration) (*Receiver, error) {
	if timeout <= 0 || WriteClear(io.Discard, session, 1) != nil || WriteUpdate(io.Discard, session, 1, "", 0) != nil {
		return nil, errors.New("invalid editing receiver parameters")
	}
	return &Receiver{session: strings.Clone(session), timeout: timeout}, nil
}

// Consume takes ownership of conn, which must already have been authenticated
// and handshaken for this receiver's session. It closes conn on every return.
// Cancellation closes conn to interrupt a blocked frame read. A reconnect gets
// a fresh state and can start its sequence again after this call has returned.
// Close errors are returned alongside any stream error.
func (r *Receiver) Consume(ctx context.Context, conn net.Conn) (err error) {
	if conn == nil {
		return errors.New("nil editing connection")
	}
	var state *State
	var closeOnce sync.Once
	var closeErr error
	closeConn := func() { closeOnce.Do(func() { closeErr = conn.Close() }) }
	defer func() {
		if state != nil {
			state.Close()
		}
		closeConn()
		err = errors.Join(err, closeErr)
		if state != nil {
			r.mu.Lock()
			r.active = nil
			r.activeContext = nil
			r.mu.Unlock()
		}
	}()
	if ctx == nil {
		return errors.New("nil editing context")
	}
	r.mu.Lock()
	if r.active != nil || ctx.Err() != nil {
		r.mu.Unlock()
		return errors.New("editing receiver unavailable")
	}
	state = &State{}
	r.active = state
	r.activeContext = ctx
	r.mu.Unlock()
	stop := context.AfterFunc(ctx, func() {
		state.Close()
		closeConn()
	})
	defer stop()
	return ConsumeConn(conn, r.session, state, r.timeout)
}

// Snapshot returns the current connection's latest state, if any. A snapshot is
// hidden as soon as the connection's context reports cancellation, even if the
// asynchronous cancellation callback has not run yet.
func (r *Receiver) Snapshot() (Snapshot, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.active == nil || r.activeContext.Err() != nil {
		return Snapshot{}, false
	}
	return r.active.Snapshot()
}
