//go:build darwin

package editstate

import (
	"errors"
	"io"
	"time"

	"offline-complete/internal/ipc"
)

// DialPublisher authenticates a Unix connection before handing it to a
// publisher. The caller must supply a path protected by its private-directory
// policy; peer UID and session IDs do not authenticate a same-user process.
func DialPublisher(path, session string, handshakeTimeout, writeTimeout time.Duration) (*Publisher, error) {
	if writeTimeout <= 0 || WriteClear(io.Discard, session, 1) != nil || WriteUpdate(io.Discard, session, 1, "", 0) != nil {
		return nil, errors.New("invalid editing publisher parameters")
	}
	conn, err := ipc.DialAuthenticated(path, session, handshakeTimeout)
	if err != nil {
		return nil, err
	}
	p, err := NewPublisher(conn, session, writeTimeout)
	if err != nil {
		return nil, errors.Join(err, conn.Close())
	}
	return p, nil
}
