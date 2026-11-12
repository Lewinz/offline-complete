//go:build darwin

package ipc

import (
	"net"
	"os"
	"syscall"
	"testing"
)

func TestVerifyPeerUIDOnBothEndsAndRejectsMismatch(t *testing.T) {
	fds, err := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_STREAM, 0)
	if err != nil {
		t.Fatal(err)
	}
	a := os.NewFile(uintptr(fds[0]), "peer-a")
	b := os.NewFile(uintptr(fds[1]), "peer-b")
	defer a.Close()
	defer b.Close()
	ac, err := net.FileConn(a)
	if err != nil {
		t.Fatal(err)
	}
	defer ac.Close()
	bc, err := net.FileConn(b)
	if err != nil {
		t.Fatal(err)
	}
	defer bc.Close()
	for _, conn := range []*net.UnixConn{ac.(*net.UnixConn), bc.(*net.UnixConn)} {
		if err := VerifyPeerUID(conn); err != nil {
			t.Fatalf("same UID rejected: %v", err)
		}
		if err := verifyPeerUID(conn, uint32(os.Geteuid())^1); err == nil {
			t.Fatal("wrong UID accepted")
		}
	}
}

func TestVerifyPeerUIDRejectsClosedConnection(t *testing.T) {
	fds, err := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_STREAM, 0)
	if err != nil {
		t.Fatal(err)
	}
	a := os.NewFile(uintptr(fds[0]), "closed-peer")
	b := os.NewFile(uintptr(fds[1]), "other-peer")
	defer a.Close()
	defer b.Close()
	conn, err := net.FileConn(a)
	if err != nil {
		t.Fatal(err)
	}
	conn.Close()
	if err := VerifyPeerUID(conn.(*net.UnixConn)); err == nil {
		t.Fatal("closed connection accepted")
	}
}
