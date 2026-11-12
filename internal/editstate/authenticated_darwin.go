//go:build darwin

package editstate

import (
	"errors"
	"io"
	"net"
	"time"

	"offline-complete/internal/ipc"
)

// ConsumeAuthenticatedConn owns an accepted Unix connection and a state used
// exclusively for that connection. The caller must use a fresh state and
// coordinate its publication with the connection lifecycle. It verifies the
// peer and session before consuming frames. Every return closes the connection
// and state; reconnects require a new state and a fresh snapshot. It does not
// manage a listener or coordinate state publication between connections.
func ConsumeAuthenticatedConn(conn *net.UnixConn, session string, state *State, handshakeTimeout, frameTimeout time.Duration) error {
	if state != nil {
		defer state.Close()
	}
	if conn != nil {
		defer conn.Close()
	}
	if conn == nil || state == nil || handshakeTimeout <= 0 || frameTimeout <= 0 {
		return errors.New("invalid authenticated editing connection")
	}
	if WriteClear(io.Discard, session, 1) != nil || WriteUpdate(io.Discard, session, 1, "", 0) != nil {
		return errors.New("invalid editing stream session")
	}
	if err := ipc.AcceptHandshake(conn, session, handshakeTimeout); err != nil {
		return err
	}
	return ConsumeConn(conn, session, state, frameTimeout)
}
