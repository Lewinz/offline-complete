//go:build darwin

package editstate

import (
	"context"
	"net"
	"time"

	"offline-complete/internal/ipc"
)

// ServeAuthenticated owns the listener while serving authenticated connections
// for this receiver's session. The caller must supply a securely created listener.
// Per-connection failures discard the snapshot and do not stop the service.
func (r *Receiver) ServeAuthenticated(ctx context.Context, listener *net.UnixListener, handshakeTimeout time.Duration, limit int) error {
	return ipc.ServeAuthenticated(ctx, listener, r.session, handshakeTimeout, limit, func(ctx context.Context, conn *net.UnixConn) {
		_ = r.Consume(ctx, conn)
	})
}
