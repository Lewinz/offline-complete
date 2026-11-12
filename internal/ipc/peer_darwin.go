//go:build darwin

package ipc

import (
	"errors"
	"fmt"
	"net"
	"os"
	"syscall"
	"unsafe"
)

// VerifyPeerUID checks the effective UID of a connected Unix socket peer.
// Same-UID processes are not distinguished or authenticated by this check.
func VerifyPeerUID(conn *net.UnixConn) error {
	return verifyPeerUID(conn, uint32(os.Geteuid()))
}

func verifyPeerUID(conn *net.UnixConn, expected uint32) error {
	if conn == nil {
		return errors.New("nil Unix connection")
	}
	raw, err := conn.SyscallConn()
	if err != nil {
		return err
	}
	var peerErr error
	err = raw.Control(func(fd uintptr) {
		// Darwin's LOCAL_PEERCRED returns struct xucred (version, UID,
		// group count and groups). Reject unknown or incomplete layouts.
		var cred [19]uint32
		length := uint32(unsafe.Sizeof(cred))
		_, _, errno := syscall.Syscall6(syscall.SYS_GETSOCKOPT, fd, 0, 1,
			uintptr(unsafe.Pointer(&cred[0])), uintptr(unsafe.Pointer(&length)), 0)
		if errno != 0 {
			peerErr = fmt.Errorf("get peer credentials: %w", errno)
		} else if length != uint32(unsafe.Sizeof(cred)) || cred[0] != 0 {
			peerErr = errors.New("invalid peer credentials")
		} else if cred[1] != expected {
			peerErr = errors.New("peer UID mismatch")
		}
	})
	return errors.Join(err, peerErr)
}
