package editstate

import (
	"net"
	"testing"
	"time"
)

// Hold the first write after the underlying pipe has been closed to model a
// sender that has not yet resumed after its I/O was interrupted.
type delayedReturnConn struct {
	net.Conn
	writing chan struct{}
	release chan struct{}
}

func (c *delayedReturnConn) Write(b []byte) (int, error) {
	close(c.writing)
	n, err := c.Conn.Write(b)
	<-c.release
	return n, err
}

func TestPublisherCloseReleasesQueuedSend(t *testing.T) {
	client, server := net.Pipe()
	defer server.Close()
	conn := &delayedReturnConn{Conn: client, writing: make(chan struct{}), release: make(chan struct{})}
	p, err := NewPublisher(conn, "session", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	first := make(chan error, 1)
	defer func() {
		close(conn.release)
		<-first
	}()
	go func() { first <- p.Clear() }()
	<-conn.writing
	queued := make(chan error, 1)
	go func() { queued <- p.Update("queued", 6) }()
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-queued:
		if err == nil {
			t.Fatal("queued send succeeded after close")
		}
	case <-time.After(time.Second):
		t.Fatal("queued send waited for interrupted sender")
	}
}
