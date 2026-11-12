package ipc

import (
	"errors"
	"net"
	"time"
)

// ReadHandshakeWithTimeout bounds the entire frame read. The caller must own
// the connection's read deadline and must not read concurrently. Any previous
// read deadline is replaced; the deadline is cleared before returning.
func ReadHandshakeWithTimeout(conn net.Conn, session string, timeout time.Duration) (message Handshake, err error) {
	if timeout <= 0 {
		return Handshake{}, errors.New("handshake timeout must be positive")
	}
	if err := conn.SetReadDeadline(time.Now().Add(timeout)); err != nil {
		return Handshake{}, err
	}
	defer func() { err = errors.Join(err, conn.SetReadDeadline(time.Time{})) }()
	return ReadHandshake(conn, session)
}

// WriteHandshakeWithTimeout bounds the entire frame write. The caller must own
// the connection's write deadline and must not write concurrently. Any previous
// write deadline is replaced; the deadline is cleared before returning.
func WriteHandshakeWithTimeout(conn net.Conn, message Handshake, timeout time.Duration) (err error) {
	if timeout <= 0 {
		return errors.New("handshake timeout must be positive")
	}
	if err := conn.SetWriteDeadline(time.Now().Add(timeout)); err != nil {
		return err
	}
	defer func() { err = errors.Join(err, conn.SetWriteDeadline(time.Time{})) }()
	return WriteHandshake(conn, message)
}
