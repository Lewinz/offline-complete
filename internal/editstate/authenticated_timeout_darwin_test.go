//go:build darwin

package editstate

import (
	"net"
	"path/filepath"
	"testing"
	"time"

	"offline-complete/internal/ipc"
)

func TestEditingTimeout(t *testing.T) {
	for _, phase := range []string{"handshake", "frame"} {
		t.Run(phase, func(t *testing.T) {
			listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: filepath.Join(t.TempDir(), "s"), Net: "unix"})
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			state := &State{}
			done := make(chan error, 1)
			go func() {
				conn, err := listener.AcceptUnix()
				if err == nil {
					err = ConsumeAuthenticatedConn(conn, "s", state, 200*time.Millisecond, 200*time.Millisecond)
				}
				done <- err
			}()
			client, err := net.DialUnix("unix", nil, listener.Addr().(*net.UnixAddr))
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close()
			if phase == "frame" {
				if err := ipc.ConnectHandshake(client, "s", time.Second); err != nil {
					t.Fatal(err)
				}
				if _, err := client.Write([]byte{0, 0}); err != nil {
					t.Fatal(err)
				}
			}
			select {
			case err := <-done:
				if err == nil {
					t.Fatal("stalled connection accepted")
				}
			case <-time.After(2 * time.Second):
				t.Fatal("stalled connection did not time out")
			}
			if err := state.Update(1, "late", 4); err == nil {
				t.Fatal("timeout allowed late update")
			}
			if err := client.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
				t.Fatal(err)
			}
			if _, err := client.Read(make([]byte, 1)); err == nil {
				t.Fatal("timeout did not close connection")
			} else if ne, ok := err.(net.Error); ok && ne.Timeout() {
				t.Fatalf("connection remained open: %v", err)
			}
		})
	}
}
