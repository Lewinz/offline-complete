//go:build darwin

package ipc

import (
	"errors"
	"net"
	"time"
)

// ConnectHandshake verifies the server and exchanges session handshakes.
// On failure it closes conn. The caller must exclusively own its deadlines
// and reads and writes during the exchange; previous deadlines are cleared.
func ConnectHandshake(conn *net.UnixConn, session string, timeout time.Duration) error {
	return exchangeHandshake(conn, session, timeout, true)
}

// AcceptHandshake verifies the client and exchanges session handshakes.
// On failure it closes conn. The caller must exclusively own its deadlines
// and reads and writes during the exchange; previous deadlines are cleared.
func AcceptHandshake(conn *net.UnixConn, session string, timeout time.Duration) error {
	return exchangeHandshake(conn, session, timeout, false)
}

func exchangeHandshake(conn *net.UnixConn, session string, timeout time.Duration, client bool) (err error) {
	if conn == nil {
		return errors.New("nil Unix connection")
	}
	defer func() {
		if err != nil {
			err = errors.Join(err, conn.Close())
		}
	}()
	if timeout <= 0 || session == "" {
		return errors.New("invalid handshake parameters")
	}
	if err = VerifyPeerUID(conn); err != nil {
		return err
	}
	if err = conn.SetDeadline(time.Now().Add(timeout)); err != nil {
		return err
	}
	defer func() { err = errors.Join(err, conn.SetDeadline(time.Time{})) }()
	message := Handshake{Version: 1, SessionID: session}
	if client {
		if err = WriteHandshake(conn, message); err != nil {
			return err
		}
		_, err = ReadHandshake(conn, session)
		return err
	}
	if _, err = ReadHandshake(conn, session); err != nil {
		return err
	}
	return WriteHandshake(conn, message)
}
