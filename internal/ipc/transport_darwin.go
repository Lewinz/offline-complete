//go:build darwin

package ipc

import (
	"errors"
	"fmt"
	"net"
	"time"
)

// DialAuthenticated connects to a Unix socket and verifies its peer and
// session. timeout bounds both the dial and handshake together. Callers must
// supply a socket path established by their private-directory policy.
func DialAuthenticated(path, session string, timeout time.Duration) (*net.UnixConn, error) {
	if path == "" || session == "" || timeout <= 0 {
		return nil, errors.New("invalid connection parameters")
	}
	if err := validateSessionID(session); err != nil {
		return nil, fmt.Errorf("invalid connection session: %w", err)
	}
	deadline := time.Now().Add(timeout)
	raw, err := net.DialTimeout("unix", path, timeout)
	if err != nil {
		return nil, err
	}
	conn, ok := raw.(*net.UnixConn)
	if !ok {
		raw.Close()
		return nil, errors.New("not a Unix connection")
	}
	if err := ConnectHandshake(conn, session, time.Until(deadline)); err != nil {
		return nil, err
	}
	return conn, nil
}

// AcceptAuthenticated accepts one client and verifies its peer and session.
// timeout bounds the handshake, not the wait for an incoming connection.
// A rejected connection does not close listener; callers decide when to retry.
func AcceptAuthenticated(listener *net.UnixListener, session string, timeout time.Duration) (*net.UnixConn, error) {
	if listener == nil || session == "" || timeout <= 0 {
		return nil, errors.New("invalid connection parameters")
	}
	if err := validateSessionID(session); err != nil {
		return nil, fmt.Errorf("invalid connection session: %w", err)
	}
	conn, err := listener.AcceptUnix()
	if err != nil {
		return nil, err
	}
	if err := AcceptHandshake(conn, session, timeout); err != nil {
		return nil, err
	}
	return conn, nil
}
