package editstate

import (
	"io"
	"net"
	"testing"
	"time"
)

type delayedCycleConn struct {
	net.Conn
	entered chan struct{}
	release chan struct{}
	pause   chan struct{}
}

func (c *delayedCycleConn) Write(b []byte) (int, error) {
	select {
	case <-c.pause:
		close(c.entered)
		n, err := c.Conn.Write(b)
		<-c.release
		return n, err
	default:
		return c.Conn.Write(b)
	}
}

func TestCycleCloseDoesNotWaitForInterruptedSender(t *testing.T) {
	client, server := net.Pipe()
	defer server.Close()
	conn := &delayedCycleConn{Conn: client, entered: make(chan struct{}), release: make(chan struct{}), pause: make(chan struct{})}
	p, err := NewPublisher(conn, "session", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	cycles := NewCyclePublisher(p)
	go io.Copy(io.Discard, server)
	token, err := cycles.Begin()
	if err != nil {
		t.Fatal(err)
	}
	close(conn.pause)
	sent := make(chan error, 1)
	go func() { sent <- cycles.Update(token, "abc", 3, true) }()
	<-conn.entered
	defer func() { close(conn.release); <-sent }()
	closed := make(chan error, 1)
	go func() { closed <- cycles.Close() }()
	select {
	case err := <-closed:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("cycle close waited for sender to resume")
	}
	if _, err := cycles.Begin(); err == nil {
		t.Fatal("closed cycle accepted begin")
	}
}
